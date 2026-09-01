// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvlink

import (
	"context"
	"os"
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

// stateWith returns the State the source compiles for a node whose cluster
// declares topology; an empty document is a node whose cluster does not.
func stateWith(contents string) *agent.State {
	if contents == "" {
		return &agent.State{}
	}
	return &agent.State{TopologyRaw: []byte(contents)}
}

// The NRI plugin bind-mounts the overlay root and points workloads at this
// relative path, so the layout is a contract rather than a detail.
func TestStage_WritesDocumentToTheNRIPath(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	s := New()

	require.NoError(t, s.Stage(context.Background(), h, stateWith(doc)))
	require.True(t, s.Ready())

	got, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)
	require.Equal(t, doc, string(got), "the document must be staged verbatim")
}

// The engine selects this node's clique by NODE_NAME at load time, so every
// node stages the whole cluster document unchanged.
func TestStage_StagesEveryNodesEntry(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())

	require.NoError(t, New().Stage(context.Background(), h, stateWith(doc)))
	got, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(got), "worker-0")
	require.Contains(t, string(got), "worker-1")
}

// The chart mounts the ConfigMap only when topology is enabled.
func TestStage_NoOpWithoutTopology(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	s := New()

	require.NoError(t, s.Stage(context.Background(), h, stateWith("")))
	require.True(t, s.Ready(), "a node without topology is ready, not pending")
	require.NoFileExists(t, h.RootPath("topology/topology.yaml"))
}

func TestStage_IsIdempotent(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	s := New()

	require.NoError(t, s.Stage(context.Background(), h, stateWith(doc)))
	first, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)

	require.NoError(t, s.Stage(context.Background(), h, stateWith(doc)))
	second, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// A ConfigMap edit reaches workloads through a reconcile, without a pod restart.
func TestStage_PicksUpAnEditedDocument(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	s := New()
	require.NoError(t, s.Stage(context.Background(), h, stateWith(doc)))

	updated := doc + "      - id: 2\n        nodes: [worker-2]\n"
	require.NoError(t, s.Stage(context.Background(), h, stateWith(updated)))

	got, err := os.ReadFile(h.RootPath("topology/topology.yaml"))
	require.NoError(t, err)
	require.Equal(t, updated, string(got))
}

// The overlay is on a host mount that outlives the pod, so a topology withdrawn
// from the cluster must not leave workloads on an obsolete clique.
func TestStage_RetractsAWithdrawnDocument(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	s := New()
	require.NoError(t, s.Stage(context.Background(), h, stateWith(doc)))

	require.NoError(t, s.Stage(context.Background(), h, stateWith("")))

	require.True(t, s.Ready(), "a retracted topology is ready, not pending")
	require.NoFileExists(t, h.RootPath("topology/topology.yaml"))
}

func TestDiscard_RemovesTheDocument(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	s := New()
	require.NoError(t, s.Stage(context.Background(), h, stateWith(doc)))

	require.NoError(t, s.Discard(context.Background(), h))
	require.NoFileExists(t, h.RootPath("topology/topology.yaml"))
}

func TestDiscard_NoOpBeforeStage(t *testing.T) {
	t.Parallel()
	require.NoError(t, New().Discard(context.Background(), host.New(t.TempDir())))
}
