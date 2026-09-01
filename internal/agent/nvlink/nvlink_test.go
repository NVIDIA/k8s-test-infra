// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvlink

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const doc = `domains:
  - uuid: 6f0e1b8a-0000-4000-8000-000000000001
    cliques:
      - id: 1
        nodes: [worker-0, worker-1]
`

// withSource points the simulator at a fixture instead of the chart's mount.
func withSource(t *testing.T, contents string) {
	t.Helper()
	orig := sourcePath
	t.Cleanup(func() { sourcePath = orig })

	if contents == "" {
		sourcePath = filepath.Join(t.TempDir(), "absent.yaml")
		return
	}
	p := filepath.Join(t.TempDir(), "topology.yaml")
	require.NoError(t, os.WriteFile(p, []byte(contents), 0o644))
	sourcePath = p
}

// The NRI plugin bind-mounts the overlay root and points workloads at this
// relative path, so the layout is a contract rather than a detail.
func TestStage_CopiesDocumentToTheNRIPath(t *testing.T) {
	withSource(t, doc)
	h := host.New(t.TempDir())
	s := New()

	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))
	require.True(t, s.Ready())

	got, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)
	require.Equal(t, doc, string(got), "the document must be staged verbatim")
}

// The engine selects this node's clique by NODE_NAME at load time, so every
// node stages the whole cluster document unchanged.
func TestStage_StagesEveryNodesEntry(t *testing.T) {
	withSource(t, doc)
	h := host.New(t.TempDir())

	require.NoError(t, New().Stage(context.Background(), h, &agent.State{}))
	got, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(got), "worker-0")
	require.Contains(t, string(got), "worker-1")
}

// The chart mounts the ConfigMap only when topology is enabled.
func TestStage_NoOpWithoutTopology(t *testing.T) {
	withSource(t, "")
	h := host.New(t.TempDir())
	s := New()

	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))
	require.True(t, s.Ready(), "a node without topology is ready, not pending")
	require.NoFileExists(t, h.RootPath("topology/topology.yaml"))
}

func TestStage_IsIdempotent(t *testing.T) {
	withSource(t, doc)
	h := host.New(t.TempDir())
	s := New()

	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))
	first, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)

	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))
	second, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// A ConfigMap edit must reach workloads without a pod restart.
func TestStage_PicksUpAnEditedDocument(t *testing.T) {
	withSource(t, doc)
	h := host.New(t.TempDir())
	s := New()
	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))

	updated := doc + "      - id: 2\n        nodes: [worker-2]\n"
	require.NoError(t, os.WriteFile(sourcePath, []byte(updated), 0o644))
	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))

	got, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)
	require.Equal(t, updated, string(got))
}

func TestDiscard_RemovesTheDocument(t *testing.T) {
	withSource(t, doc)
	h := host.New(t.TempDir())
	s := New()
	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))

	require.NoError(t, s.Discard(context.Background(), h))
	require.NoFileExists(t, h.RootPath("topology/topology.yaml"))
}

func TestDiscard_NoOpBeforeStage(t *testing.T) {
	require.NoError(t, New().Discard(context.Background(), host.New(t.TempDir())))
}

// The overlay is on a host mount that outlives the pod, so a topology withdrawn
// between two agent lifetimes must not leave workloads on an obsolete clique.
func TestStage_RetractsAWithdrawnDocument(t *testing.T) {
	withSource(t, doc)
	h := host.New(t.TempDir())
	s := New()
	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))

	require.NoError(t, os.Remove(sourcePath))
	require.NoError(t, s.Stage(context.Background(), h, &agent.State{}))

	require.True(t, s.Ready(), "a retracted topology is ready, not pending")
	require.NoFileExists(t, h.RootPath("topology/topology.yaml"))
}

// A source that cannot be stat'd at all is a broken mount, not a node without
// topology; reporting ready would hide it behind plausible clique defaults.
func TestStage_PropagatesAnUnreadableSource(t *testing.T) {
	orig := sourcePath
	t.Cleanup(func() { sourcePath = orig })
	notADir := filepath.Join(t.TempDir(), "topology.yaml")
	require.NoError(t, os.WriteFile(notADir, nil, 0o644))
	sourcePath = filepath.Join(notADir, "topology.yaml")

	s := New()
	require.Error(t, s.Stage(context.Background(), host.New(t.TempDir()), &agent.State{}))
	require.False(t, s.Ready())
}
