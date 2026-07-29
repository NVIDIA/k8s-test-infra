// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/containerd/nri/pkg/stub"
)

// wedgeFactor multiplies the runtime's own request timeout to get the
// threshold past which an in-flight request counts as wedged. Once a request
// exceeds the runtime's timeout the runtime has already abandoned it, so the
// container it belonged to was created without injection whatever happens
// next. The factor buys one whole extra timeout of tolerance before the
// kubelet is asked to restart the plugin, so a single slow-but-completing
// request cannot cause a restart.
const wedgeFactor = 2

// timeoutSource reports the request timeout the runtime is applying to this
// plugin. The NRI stub satisfies it: the value is whatever containerd sent in
// the Configure request, not something the plugin chooses.
type timeoutSource interface {
	RequestTimeout() time.Duration
}

// probe is the outcome of one health question.
type probe struct {
	ok     bool
	reason string
}

// health tracks the two things that separate a serving plugin from a plugin
// that is merely running.
//
// Registration state answers "is containerd still sending us containers?".
// The plugin can be alive, holding an open socket, and no longer registered —
// containerd unregisters a plugin it has given up on. Injection stops silently
// at that moment, because injection is baked into the OCI spec at container
// creation time: pods already running keep theirs, and only pods created
// afterwards come up unmocked.
//
// In-flight duration answers "is the handler still answering?". A handler
// blocked forever leaves the process alive and the connection up, so neither a
// process check nor a plain TCP listener notices. Tracking how long the oldest
// unfinished request has been running does, and — unlike a "time since the
// last request" watchdog — it stays quiet on an idle node, which is the normal
// steady state for this plugin.
type health struct {
	mu         sync.Mutex
	registered bool
	inFlight   map[uint64]time.Time
	next       uint64
	timeouts   timeoutSource

	now    func() time.Time
	factor int
}

func newHealth(now func() time.Time, factor int) *health {
	return &health{
		inFlight: make(map[uint64]time.Time),
		now:      now,
		factor:   factor,
	}
}

// setTimeoutSource attaches the stub once it exists. The stub needs the plugin
// (and so the health tracker) to be constructed first, so the wiring cannot be
// done in newHealth.
func (h *health) setTimeoutSource(t timeoutSource) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timeouts = t
}

// setRegistered records whether the runtime currently has this plugin
// registered. Configure sets it; the stub's OnClose callback clears it.
func (h *health) setRegistered(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.registered = v
}

// begin marks a request as started and returns the function that marks it
// finished. The returned function is safe to call once; callers defer it.
func (h *health) begin() func() {
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.next
	h.next++
	h.inFlight[id] = h.now()

	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.inFlight, id)
	}
}

// wedgeLimit is the in-flight duration past which the handler is considered
// stuck. Callers hold h.mu.
func (h *health) wedgeLimit() time.Duration {
	timeout := stub.DefaultRequestTimeout
	if h.timeouts != nil {
		if reported := h.timeouts.RequestTimeout(); reported > 0 {
			timeout = reported
		}
	}
	return timeout * time.Duration(h.factor)
}

// wedged reports the age of the oldest unfinished request and whether it has
// passed the threshold. Callers hold h.mu.
func (h *health) wedged() (time.Duration, bool) {
	limit := h.wedgeLimit()
	now := h.now()

	var oldest time.Duration
	for _, start := range h.inFlight {
		if age := now.Sub(start); age > oldest {
			oldest = age
		}
	}
	return oldest, oldest > limit
}

// liveness fails only for a wedged handler, because that is the one state the
// plugin cannot leave on its own. Losing the connection is deliberately not a
// liveness failure: the stub's Run returns when the connection drops and the
// process exits on its own, and failing liveness on "not registered yet" would
// restart the plugin every time containerd is slow to come up.
func (h *health) liveness() probe {
	h.mu.Lock()
	defer h.mu.Unlock()

	if age, stuck := h.wedged(); stuck {
		return probe{false, h.wedgeReason(age)}
	}
	return probe{true, "ok"}
}

// readiness is the detectable half of the fail-open posture. It reports the
// plugin as serving only while it is registered with the runtime and its
// handler is answering, so every window in which injection is silently not
// happening shows up as a NotReady pod.
func (h *health) readiness() probe {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.registered {
		return probe{false, "not registered with the container runtime; new containers are not being injected"}
	}
	if age, stuck := h.wedged(); stuck {
		return probe{false, h.wedgeReason(age)}
	}
	return probe{true, "ok"}
}

// wedgeReason renders the failure text. Callers hold h.mu.
func (h *health) wedgeReason(age time.Duration) string {
	return fmt.Sprintf("wedged: a container request has been in flight for %s, past the %s threshold",
		age.Round(time.Millisecond), h.wedgeLimit())
}

// handler serves the two probe endpoints. Both write the reason in the body so
// a failure is legible in `kubectl describe pod` and to a curl from a debug
// pod, rather than being a bare status code.
func (h *health) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", writeProbe(h.liveness))
	mux.HandleFunc("GET /readyz", writeProbe(h.readiness))
	return mux
}

func writeProbe(check func() probe) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		result := check()
		status := http.StatusOK
		if !result.ok {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		_, _ = fmt.Fprintln(w, result.reason)
	}
}
