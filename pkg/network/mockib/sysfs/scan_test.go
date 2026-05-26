// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
)

func TestScan_RenderedTree(t *testing.T) {
	dir := t.TempDir()
	ib := config.Infiniband{Enabled: true}
	if err := render.Render(render.Options{IB: ib, GPUCount: 2, NodeName: "node-a", Output: dir}); err != nil {
		t.Fatal(err)
	}
	ports, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 {
		t.Fatalf("want 2 ports, got %d: %+v", len(ports), ports)
	}
	var mlx0 bool
	for _, p := range ports {
		if p.CAName == "mlx5_0" {
			mlx0 = true
			if p.Port != 1 || p.PortGUID == "" || p.LID == 0 {
				t.Fatalf("mlx5_0 advert: %+v", p)
			}
		}
	}
	if !mlx0 {
		t.Fatalf("mlx5_0 not found in %+v", ports)
	}
}
