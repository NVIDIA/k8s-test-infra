// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/daemon/madtest"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
)

func TestLoopback_ShouldQueueRecv_LocalGUID(t *testing.T) {
	dir := t.TempDir()
	ib := config.Infiniband{Enabled: true}
	if err := render.Render(render.Options{IB: ib, GPUCount: 2, NodeName: "node-a", Output: dir}); err != nil {
		t.Fatal(err)
	}
	ports, err := sysfs.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	lb := NewLoopback(ports)
	mad := madtest.PingMAD(ports[0])
	if !lb.ShouldQueueRecv(mad) {
		t.Fatalf("want queue for local ping; port=%+v", ports[0])
	}
}

func TestLoopback_ShouldNotQueueRecv_RemoteLID(t *testing.T) {
	dir := t.TempDir()
	ib := config.Infiniband{Enabled: true}
	if err := render.Render(render.Options{IB: ib, GPUCount: 2, NodeName: "node-a", Output: dir}); err != nil {
		t.Fatal(err)
	}
	ports, err := sysfs.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	lb := NewLoopback(ports)
	remote := make([]byte, 72)
	binary.BigEndian.PutUint16(remote[umadLIDOffset:], ports[0].LID+0x100)
	if lb.ShouldQueueRecv(remote) {
		t.Fatal("should not loopback echo for non-local destination LID")
	}
}

func TestLoopback_umadMADOffsetMatchesShim(t *testing.T) {
	const want = 56 // libibumad umad_get_mad() offset; see c/umad_shim.c MOCK_UMAD_HDR_SZ
	if umadMADOffset != want {
		t.Fatalf("umadMADOffset = %d, want %d (libibumad header size)", umadMADOffset, want)
	}
}
