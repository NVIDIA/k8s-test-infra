// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
)

func TestSynthesizeVerbsWrite_QueryDevice(t *testing.T) {
	root := t.TempDir()
	if err := render.Render(render.Options{
		IB:       config.Infiniband{Enabled: true},
		GPUCount: 1,
		NodeName: "n1",
		Output:   root,
	}); err != nil {
		t.Fatalf("render: %v", err)
	}
	srv, err := NewServer(Config{IBRoot: root}, nil)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	h := &verbsHandle{caName: "mlx5_0"}
	cmd := make([]byte, 16)
	binary.LittleEndian.PutUint32(cmd[0:4], ibUVCmdQueryDevice)
	resp, err := srv.synthesizeVerbsWrite(h, cmd)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if len(resp) < 132 || resp[131] != 1 {
		t.Fatalf("phys_port_cnt: len=%d byte=%d", len(resp), resp[131])
	}
}
