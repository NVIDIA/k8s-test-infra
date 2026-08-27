// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package health serves the /healthz and /readyz probes shared by every Mokka
// binary. A probe answers with its reason in the body, not just a status code,
// so a failing pod explains itself in `kubectl describe pod` instead of leaving
// an operator to guess which of its parts went red.
//
// Binaries with no other HTTP surface run a Server; ones that already serve
// HTTP mount Handler on their own router.
package health

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Probe is the outcome of one health question.
//
// The zero value is a failure, so a probe that forgets to say it is well fails
// closed.
type Probe struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	// Components attributes a composite probe to its parts, so a red /readyz
	// names which part is red rather than only reporting that one is.
	Components map[string]Probe `json:"components,omitempty"`
}

// OK reports a passing probe.
func OK() Probe { return Probe{OK: true} }

// Unhealthy reports a failing probe with the reason served in the body.
func Unhealthy(format string, args ...any) Probe {
	return Probe{Reason: fmt.Sprintf(format, args...)}
}

// Checker answers one health question.
type Checker func() Probe

// Handler serves a single probe: 200 when it passes, 503 otherwise.
func Handler(check Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		probe := check()

		status := http.StatusOK
		if !probe.OK {
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(probe)
	}
}
