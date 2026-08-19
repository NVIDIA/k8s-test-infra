//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// nodesJSON builds a `kubectl get nodes -o json` payload.
func nodesJSON(items ...string) string {
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

// nodeJSON renders one node. providerID is omitted when empty, which is how a
// non-Kind cluster looks.

func nodeJSON(name, providerID string, controlPlane bool) string {
	labels := `"kubernetes.io/hostname":"` + name + `"`
	if controlPlane {
		labels += `,"node-role.kubernetes.io/control-plane":""`
	}
	spec := `{}`
	if providerID != "" {
		spec = `{"providerID":"` + providerID + `"}`
	}
	return `{"metadata":{"name":"` + name + `","labels":{` + labels + `}},"spec":` + spec + `}`
}

// The renamed case is the whole point: nodeRegistration.name decouples the
// Kubernetes node name from the container name, and kubectl-vs-docker call
// sites need different ones.
func TestParseNodesResolvesRenamedNodesToTheirContainers(t *testing.T) {
	in := nodesJSON(
		nodeJSON("mokka-control-plane", "kind://docker/mokka/mokka-control-plane", true),
		nodeJSON("worker-0", "kind://docker/mokka/mokka-worker", false),
		nodeJSON("worker-1", "kind://docker/mokka/mokka-worker2", false),
	)

	got, err := parseNodes([]byte(in))
	require.NoError(t, err)
	require.Equal(t, []Node{
		{Name: "mokka-control-plane", Container: "mokka-control-plane", Role: RoleControlPlane},
		{Name: "worker-0", Container: "mokka-worker", Role: RoleWorker},
		{Name: "worker-1", Container: "mokka-worker2", Role: RoleWorker},
	}, got)
}

// Roles come from the control-plane label, not from a substring of the name:
// a pinned name need not contain "worker", and "worker" could appear in a
// control-plane name.
func TestParseNodesReadsRoleFromLabelNotName(t *testing.T) {
	in := nodesJSON(
		nodeJSON("cp-worker-host", "kind://docker/mokka/mokka-control-plane", true),
		nodeJSON("gpu-a", "kind://docker/mokka/mokka-worker", false),
	)

	got, err := parseNodes([]byte(in))
	require.NoError(t, err)
	require.Equal(t, RoleControlPlane, got[0].Role)
	require.Equal(t, RoleWorker, got[1].Role)
}

func TestParseNodesFallsBackToNodeNameWithoutKindProviderID(t *testing.T) {
	in := nodesJSON(
		nodeJSON("node-a", "", false),
		nodeJSON("node-b", "aws:///us-east-1a/i-0abc", false),
	)

	got, err := parseNodes([]byte(in))
	require.NoError(t, err)
	require.Equal(t, "node-a", got[0].Container)
	require.Equal(t, "node-b", got[1].Container)
}

// Scenarios index into Workers() (workers[0], workers[1]) to pair a node with
// a GPU profile, so ordering has to be stable regardless of API return order.
func TestParseNodesSortsByName(t *testing.T) {
	in := nodesJSON(
		nodeJSON("worker-1", "kind://docker/mokka/mokka-worker2", false),
		nodeJSON("worker-0", "kind://docker/mokka/mokka-worker", false),
	)

	got, err := parseNodes([]byte(in))
	require.NoError(t, err)
	require.Equal(t, []string{"worker-0", "worker-1"}, []string{got[0].Name, got[1].Name})
}

func TestParseNodesRejectsMalformedOutput(t *testing.T) {
	_, err := parseNodes([]byte("not json"))
	require.Error(t, err)
}

func TestValidateName(t *testing.T) {
	require.NoError(t, ValidateName("mokka"))
	require.Error(t, ValidateName(""))
	require.Error(t, ValidateName("Mokka"))
}
