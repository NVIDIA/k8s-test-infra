//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package pod renders test pod manifests for `kubectl apply -f -`.
//
// Rendering goes through the typed API objects rather than through concatenated
// YAML text. Hand-assembled manifests put indentation and quoting on every call
// site and both fail quietly: a field indented one level too far is still valid
// YAML that the API server accepts with the setting silently dropped, and an
// unquoted "true" arrives as a bool where a label or annotation value must be a
// string. The typed form also makes field names a compile-time concern.
//
// Consistent with framework/kube, this adds no dependency: every import here is
// already vendored, so go.mod/go.sum/vendor stay untouched.
package pod

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// DefaultContainerName is the single container's name when Spec leaves it empty.
const DefaultContainerName = "app"

// DefaultGracePeriodSeconds caps teardown for every rendered pod. It is
// load-bearing for suite runtime, not cosmetic: test pods typically run `sleep`
// as PID 1, which installs no SIGTERM handler, and PID 1 never receives a
// signal it does not handle — so kubelet's SIGTERM is discarded and the pod only
// dies on the post-grace SIGKILL. At Kubernetes' 30s default that makes every
// `kubectl delete` block for the full grace period, which is pure dead time in
// a suite that deletes a pod per spec.
const DefaultGracePeriodSeconds int64 = 1

// Spec describes a single-container test pod. The zero value renders a valid
// pod once Name and Image are set.
type Spec struct {
	Name      string
	Namespace string
	// ContainerName defaults to DefaultContainerName.
	ContainerName string
	Image         string
	// Command runs as PID 1 in the container.
	Command []string
	// NodeName pins the pod, bypassing the scheduler. Callers that want
	// placement by label set NodeSelector instead.
	NodeName     string
	NodeSelector map[string]string
	Labels       map[string]string
	Annotations  map[string]string
	// GPUs, when positive, is requested as an nvidia.com/gpu limit.
	GPUs int
	// GracePeriodSeconds overrides DefaultGracePeriodSeconds. Set it only for a
	// pod that genuinely needs time to shut down.
	GracePeriodSeconds *int64
}

// Render marshals the spec to YAML.
func (s Spec) Render() []byte {
	container := corev1.Container{
		Name:    s.ContainerName,
		Image:   s.Image,
		Command: s.Command,
	}
	if container.Name == "" {
		container.Name = DefaultContainerName
	}
	if s.GPUs > 0 {
		container.Resources.Limits = corev1.ResourceList{
			kube.GPUResourceName: *resource.NewQuantity(int64(s.GPUs), resource.DecimalSI),
		}
	}

	grace := DefaultGracePeriodSeconds
	if s.GracePeriodSeconds != nil {
		grace = *s.GracePeriodSeconds
	}

	manifest, err := yaml.Marshal(&corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Labels:      s.Labels,
			Annotations: s.Annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: &grace,
			NodeName:                      s.NodeName,
			NodeSelector:                  s.NodeSelector,
			Containers:                    []corev1.Container{container},
		},
	})
	if err != nil {
		// Unreachable: a Pod of strings, ints and maps always marshals.
		panic(fmt.Sprintf("render pod %s: %v", s.Name, err))
	}
	return manifest
}
