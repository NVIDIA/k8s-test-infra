// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
)

// Built from ibping -G LIBIBMAD_DEBUG_LEVEL=3 xdump (MAD payload rows at umad offset 64).
const ibpingSAPathSendHex = "" +
	"00000000000000000000000000000000" +
	"00000000000000006262c4eb00350000" +
	"00000000000000000000000000000000" +
	"0000000000000000000000000000000c" +
	"0000000000000000fe80000000000000" +
	"a088c20300ab2001fe80000000000000" +
	"a088c20300ab56040000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000" +
	"00000000000000000000000000000000"

func TestSAPathQuery_IbpingCapture(t *testing.T) {
	madPayload, err := hex.DecodeString(ibpingSAPathSendHex)
	if err != nil {
		t.Fatal(err)
	}
	if len(madPayload) != 256 {
		t.Fatalf("payload len: got %d want 256", len(madPayload))
	}
	umad := make([]byte, umadMADOffset+len(madPayload))
	copy(umad[umadMADOffset:], madPayload)
	binary.BigEndian.PutUint16(umad[umadLIDOffset:], 0x0001)

	if !isSAPathRecordGet(umad) {
		t.Fatal("isSAPathRecordGet: want true for captured ibping SA GET")
	}

	dirA := t.TempDir()
	dirB := t.TempDir()
	// Capture is from a Kind pod on nvml-mock-demo-worker pinging ...:2001.
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "nvml-mock-demo-worker", Output: dirA,
	}); err != nil {
		t.Fatal(err)
	}
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "nvml-mock-demo-worker2", Output: dirB,
	}); err != nil {
		t.Fatal(err)
	}
	portsA, _ := sysfs.Scan(dirA)
	portsB, _ := sysfs.Scan(dirB)

	client := &Server{
		localPorts: portsB,
		loopback:   NewLoopback(portsB),
		registry:   registry.New(),
	}
	client.registry.Register(portsA[0].PortGUID, registry.Peer{
		PodIP: "10.0.0.1", NodeName: "node-a", LID: portsA[0].LID,
	})

	h := &portHandle{}
	if !client.trySAPathQuery(h, umad) {
		t.Fatal("trySAPathQuery: want true")
	}
	if len(h.recvQ) != 1 {
		t.Fatalf("recvQ len: got %d want 1", len(h.recvQ))
	}
	dlidOff, _ := pathRecordDLIDOffset(h.recvQ[0][umadMADOffset:])
	gotLID := binary.BigEndian.Uint16(h.recvQ[0][umadMADOffset+dlidOff:])
	if gotLID != portsA[0].LID {
		t.Fatalf("dlid: got 0x%04x want 0x%04x", gotLID, portsA[0].LID)
	}
	mad := h.recvQ[0][umadMADOffset:]
	if got, want := mad[8:16], umad[umadMADOffset+8:umadMADOffset+16]; string(got) != string(want) {
		t.Fatalf("TRID bytes 8-15: got %x want %x", got, want)
	}
	if !saMethodResponseSet(mad) {
		t.Fatal("expected GET response bit on SA method byte")
	}
}
