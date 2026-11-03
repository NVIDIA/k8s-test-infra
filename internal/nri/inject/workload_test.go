// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeWorkload claims the containers named after it and marks what it adds
// with its name, so routing is observable apart from any real workload.
type fakeWorkload struct {
	name string
}

func (w fakeWorkload) Matches(container Container) bool { return container.Name == w.name }

func (w fakeWorkload) Adjust(Container) (Adjustment, bool) {
	return Adjustment{Env: []string{"WORKLOAD=" + w.name}}, true
}

// A claimed container gets its workload's adjustment alone. It needs no
// opt-in, since the part of the stack that authors it adds none of Mokka's
// annotations, and one that carries an opt-in still gets no overlay.
func TestAdjustRoutesAClaimedContainerToItsWorkload(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Workloads = []Workload{fakeWorkload{name: "claimed"}}
	annotated := deviceOptIn()
	annotated.Name = "claimed"

	for _, container := range []Container{{Name: "claimed", Namespace: "default"}, annotated} {
		_, skipped := Skip(cfg, container)
		require.False(t, skipped)

		adjustment, ok := Adjust(cfg, container)
		require.True(t, ok)
		require.Equal(t, Adjustment{Env: []string{"WORKLOAD=claimed"}}, adjustment)
	}
}

// The opt-out annotation and the excluded namespaces apply to a claimed
// container as to any other.
func TestSkipAppliesTheScopeToClaimedContainers(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Workloads = []Workload{fakeWorkload{name: "claimed"}}
	tests := map[string]Container{
		"opt-out annotation": {
			Name:           "claimed",
			Namespace:      "default",
			PodAnnotations: map[string]string{cfg.OptOutAnnotation: "false"},
		},
		"excluded namespace": {Name: "claimed", Namespace: "kube-system"},
	}

	for want, container := range tests {
		reason, skipped := Skip(cfg, container)
		require.True(t, skipped)
		require.Equal(t, want, reason)
	}
}
