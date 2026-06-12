// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/stretchr/testify/require"
)

func TestScan_RenderedTree(t *testing.T) {
	dir := t.TempDir()
	ib := config.Infiniband{Enabled: true}
	err := render.Render(render.Options{IB: ib, GPUCount: 2, NodeName: "node-a", Output: dir})
	require.NoError(t, err)
	ports, err := Scan(dir)
	require.NoError(t, err)
	require.Len(t, ports, 2, "want 2 ports, got %d: %+v", len(ports), ports)
	var mlx0 bool
	for _, p := range ports {
		if p.CAName == "mlx5_0" {
			mlx0 = true
			require.False(t, p.Port != 1 || p.PortGUID == "" || p.LID == 0, "mlx5_0 advert: %+v", p)
		}
	}
	require.True(t, mlx0, "mlx5_0 not found in %+v", ports)
}
