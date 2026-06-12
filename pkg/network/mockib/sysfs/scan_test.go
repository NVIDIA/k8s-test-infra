// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/stretchr/testify/require"
)

// TestScan_NodeGUIDAndErrorPaths pins two contracts on hand-built trees:
//
//  1. A missing/empty node_guid must propagate as NodeGUID "" — normalizing
//     "" yields the non-empty "0000:0000:0000:0000", which defeats
//     fabric.coalesceGUID's nodeGUID=="" port-GUID fallback and makes every
//     local port advertise a zero NodeGUID, so ibnetdiscover-style consumers
//     merge all CAs into one node (#393).
//  2. Missing/garbled required port files (port_guid, lid) surface as errors.
func TestScan_NodeGUIDAndErrorPaths(t *testing.T) {
	valid := map[string]string{
		"ports/1/port_guid": "0xa088c20300ab0001",
		"ports/1/lid":       "0x0300",
		"node_guid":         "a088:c203:00ab:0000",
	}
	tests := []struct {
		name         string
		files        map[string]string
		wantNodeGUID string
		wantErr      string // substring; empty means Scan must succeed
	}{
		{"node_guid present is normalized", valid, "a088:c203:00ab:0000", ""},
		{"node_guid file missing keeps NodeGUID empty", omit(valid, "node_guid"), "", ""},
		{"node_guid file empty keeps NodeGUID empty", with(valid, "node_guid", ""), "", ""},
		{"node_guid whitespace-only keeps NodeGUID empty", with(valid, "node_guid", " \n"), "", ""},
		{"missing port_guid errors", omit(valid, "ports/1/port_guid"), "", "port_guid"},
		{"missing lid errors", omit(valid, "ports/1/lid"), "", "lid"},
		{"malformed lid errors", with(valid, "ports/1/lid", "not-a-lid"), "", "lid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSysfsCA(t, root, "mlx5_0", tc.files)
			ports, err := Scan(root)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, ports, 1)
			require.Equal(t, tc.wantNodeGUID, ports[0].NodeGUID)
			require.Equal(t, "a088:c203:00ab:0001", ports[0].PortGUID)
		})
	}
}

// writeSysfsCA materializes files (paths relative to the CA dir) under
// root/sys/class/infiniband/<ca>/.
func writeSysfsCA(t *testing.T, root, ca string, files map[string]string) {
	t.Helper()
	caDir := filepath.Join(root, "sys/class/infiniband", ca)
	require.NoError(t, os.MkdirAll(filepath.Join(caDir, "ports/1"), 0o755))
	for rel, content := range files {
		path := filepath.Join(caDir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

// omit returns a copy of m without key; with returns a copy with key=value.
func omit(m map[string]string, key string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}
	return out
}

func with(m map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out[key] = value
	return out
}

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
