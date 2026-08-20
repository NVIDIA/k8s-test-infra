//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package pod renders the single-container test pod the scenarios apply.
//
// The manifest lives in pod.tpl.yaml, so what the API server receives reads as a
// manifest instead of being assembled field by field at each call site. It is
// embedded rather than read from disk so the harness binary stays
// cwd-independent, the same reason framework assets are embedded. Callers fill
// only what varies for their pod and leave the rest zero; an omitted field is
// left out of the manifest entirely rather than emitted empty.
//
// The template caps terminationGracePeriodSeconds, which is load-bearing for
// suite runtime. A pod whose PID 1 ignores SIGTERM dies only on the post-grace
// SIGKILL, so at Kubernetes' 30s default every `kubectl delete` blocks for the
// full period — minutes across a suite that deletes a pod per spec. The cap is
// the backstop, not the fix: a long-lived pod should trap the signal and exit,
// which the cap then bounds if the trap fails to install.
package pod

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// DefaultContainerName is the container's name when Spec leaves it unset.
const DefaultContainerName = "app"

// DefaultRestartPolicy suits a pod a spec either execs into or runs once; in
// neither case should the kubelet restart it under the suite.
const DefaultRestartPolicy = "Never"

// DefaultGracePeriodSeconds is the cap applied unless a spec asks for longer.
// See the package doc for why it is not left at the Kubernetes default.
const DefaultGracePeriodSeconds = 1

//go:embed pod.tpl.yaml
var manifest string

// Spec describes the pod to render. The zero value renders a valid pod once
// Name and Image are set.
type Spec struct {
	Name      string
	Namespace string
	Labels    map[string]string
	// Annotations carries the injection opt-ins the NRI plugin reads.
	Annotations map[string]string
	// ContainerName defaults to DefaultContainerName.
	ContainerName string
	Image         string
	// Command runs as PID 1 in the container. Left empty, the image's own
	// entrypoint runs.
	Command []string
	// Args are passed to Command. Keeping the shell in Command and its script in
	// Args is what lets a script contain the quoting a signal trap needs without
	// escaping it through the manifest.
	Args []string
	// Env is set on the container.
	Env map[string]string
	// Node pins the pod, bypassing the scheduler. Specs that read node-local
	// state need this, because the runtime override file lives on a per-node
	// hostPath and an unpinned observer can land on a different node from the
	// workload it is meant to watch. Specs asserting on scheduling must leave it
	// empty, since a pin means the scheduler never weighs in.
	Node string
	// NodeSelector places the pod by label, for callers that need some qualifying
	// node rather than a specific one.
	NodeSelector map[string]string
	// GPUs, when positive, is requested as a GPU resource limit.
	GPUs int
	// RestartPolicy defaults to DefaultRestartPolicy.
	RestartPolicy string
	// GracePeriodSeconds defaults to DefaultGracePeriodSeconds. Raise it only for
	// a pod that genuinely needs time to shut down, and read the package doc
	// first: the default is what keeps teardown off the suite's critical path.
	GracePeriodSeconds int
}

// view is what the template sees: the caller's spec with defaults resolved and
// the GPU resource name injected, so the manifest need not restate a constant
// the framework already owns.
type view struct {
	Spec
	GPUResourceName string
}

var funcs = template.FuncMap{
	// quote emits a value as a double-quoted YAML scalar, which keys and values
	// both need: YAML 1.1 resolves bare true, y, on and ~ as bools and nulls,
	// which the API server rejects where a string belongs. A JSON string literal
	// is a valid quoted scalar, YAML 1.2 being a JSON superset. HTML escaping is
	// off so a script's & reads as itself rather than \u0026.
	"quote": func(value string) (string, error) {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return "", err
		}
		// Encode terminates every value with a newline.
		return strings.TrimSuffix(encoded.String(), "\n"), nil
	},
}

var tmpl = template.Must(template.New("pod.tpl.yaml").Funcs(funcs).Parse(manifest))

// Render returns the spec as a manifest for `kubectl apply -f -`.
//
// A failure here means the embedded template is wrong, which is a defect in this
// package rather than a condition a spec can act on, so it panics instead of
// returning an error every call site would ignore.
func (s Spec) Render() []byte {
	if s.ContainerName == "" {
		s.ContainerName = DefaultContainerName
	}
	if s.RestartPolicy == "" {
		s.RestartPolicy = DefaultRestartPolicy
	}
	if s.GracePeriodSeconds == 0 {
		s.GracePeriodSeconds = DefaultGracePeriodSeconds
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, view{
		Spec:            s,
		GPUResourceName: string(kube.GPUResourceName),
	}); err != nil {
		panic(fmt.Sprintf("render pod %s: %v", s.Name, err))
	}
	return rendered.Bytes()
}
