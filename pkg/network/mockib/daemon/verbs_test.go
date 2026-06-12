// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/stretchr/testify/require"
)

func TestSynthesizeVerbsWrite_QueryDevice(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, render.Render(render.Options{
		IB:       config.Infiniband{Enabled: true},
		GPUCount: 1,
		NodeName: "n1",
		Output:   root,
	}), "render")
	srv, err := NewServer(Config{IBRoot: root}, nil)
	require.NoError(t, err, "server")
	h := &verbsHandle{caName: "mlx5_0"}
	cmd := make([]byte, 16)
	binary.LittleEndian.PutUint32(cmd[0:4], ibUVCmdQueryDevice)
	resp, err := srv.synthesizeVerbsWrite(h, cmd)
	require.NoError(t, err, "synthesize")
	require.GreaterOrEqual(t, len(resp), 132, "phys_port_cnt: len=%d", len(resp))
	require.Equal(t, byte(1), resp[131], "phys_port_cnt")
}
