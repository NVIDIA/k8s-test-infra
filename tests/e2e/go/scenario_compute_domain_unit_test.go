//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// The rack topology mirrors the nodes' clique labels, one domain per cluster
// UUID, so the mock reports the clique each node is labeled with.
func TestRackTopologyValues(t *testing.T) {
	values, err := rackTopologyValues([]rack{
		{clique: "00000000-0000-0000-0000-0000000000a1.0", nodes: []string{"worker-0", "worker-1"}},
		{clique: "00000000-0000-0000-0000-0000000000b2.0", nodes: []string{"worker-2", "worker-3"}},
	})
	require.NoError(t, err)

	var got struct {
		Topology struct {
			Enabled bool `json:"enabled"`
			Domains []struct {
				UUID    string `json:"uuid"`
				Cliques []struct {
					ID    int      `json:"id"`
					Nodes []string `json:"nodes"`
				} `json:"cliques"`
			} `json:"domains"`
		} `json:"topology"`
	}
	require.NoError(t, yaml.Unmarshal(values, &got))
	require.True(t, got.Topology.Enabled)
	require.Len(t, got.Topology.Domains, 2)
	require.Equal(t, "00000000-0000-0000-0000-0000000000a1", got.Topology.Domains[0].UUID)
	require.Equal(t, 0, got.Topology.Domains[0].Cliques[0].ID)
	require.Equal(t, []string{"worker-0", "worker-1"}, got.Topology.Domains[0].Cliques[0].Nodes)
	require.Equal(t, []string{"worker-2", "worker-3"}, got.Topology.Domains[1].Cliques[0].Nodes)
}

func TestRackTopologyValuesRejectsAMalformedClique(t *testing.T) {
	_, err := rackTopologyValues([]rack{{clique: "no-clique-id", nodes: []string{"worker-0"}}})
	require.ErrorContains(t, err, "is not <cluster UUID>.<clique ID>")
}

func TestComputeDomainManifests(t *testing.T) {
	var cd struct {
		Kind string `json:"kind"`
		Spec struct {
			NumNodes int `json:"numNodes"`
			Channel  struct {
				AllocationMode        string `json:"allocationMode"`
				ResourceClaimTemplate struct {
					Name string `json:"name"`
				} `json:"resourceClaimTemplate"`
			} `json:"channel"`
		} `json:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(computeDomainManifest("rack-1", 2, "All"), &cd))
	require.Equal(t, "ComputeDomain", cd.Kind)
	require.Equal(t, 2, cd.Spec.NumNodes)
	require.Equal(t, "All", cd.Spec.Channel.AllocationMode)
	require.Equal(t, "rack-1-channel", cd.Spec.Channel.ResourceClaimTemplate.Name)

	var pod struct {
		Spec struct {
			NodeSelector   map[string]string `json:"nodeSelector"`
			ResourceClaims []struct {
				ResourceClaimTemplateName string `json:"resourceClaimTemplateName"`
			} `json:"resourceClaims"`
		} `json:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(cdWorkloadManifest("rack-1-a", "rack-1", "worker-0"), &pod))
	require.Equal(t, map[string]string{"kubernetes.io/hostname": "worker-0"}, pod.Spec.NodeSelector)
	require.Equal(t, "rack-1-channel", pod.Spec.ResourceClaims[0].ResourceClaimTemplateName)
}
