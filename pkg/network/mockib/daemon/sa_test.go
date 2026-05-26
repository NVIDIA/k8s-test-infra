// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/sysfs"
)

func TestSAPathQuery_LocalPort(t *testing.T) {
	dir := t.TempDir()
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dir,
	}); err != nil {
		t.Fatal(err)
	}
	ports, err := sysfs.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		localPorts: ports,
		loopback:   NewLoopback(ports),
		registry:   registry.New(),
	}
	dgid := gidBytesForPort(t, ports[0].DefaultGID)
	send := makeSAPathQueryMAD(dgid)
	h := &portHandle{}
	if !srv.trySAPathQuery(h, send) {
		t.Fatal("expected SA path query handled")
	}
	if len(h.recvQ) != 1 {
		t.Fatalf("recvQ len: got %d want 1", len(h.recvQ))
	}
	dlidOff, _ := pathRecordDLIDOffset(h.recvQ[0][umadMADOffset:])
	gotLID := binary.BigEndian.Uint16(h.recvQ[0][umadMADOffset+dlidOff:])
	if gotLID != ports[0].LID {
		t.Fatalf("dlid: got 0x%04x want 0x%04x", gotLID, ports[0].LID)
	}
	resp := h.recvQ[0][umadMADOffset:]
	if resp[20]&0x80 == 0 {
		t.Fatalf("SA GET response bit not set on method byte: 0x%02x", resp[20])
	}
	// libibmad _do_madrpc matches TRID at MAD bytes 8-15; must not be corrupted by method scan.
	if got, want := resp[8:16], send[umadMADOffset+8:umadMADOffset+16]; string(got) != string(want) {
		t.Fatalf("TRID bytes 8-15: got %x want %x", got, want)
	}
}

func TestSAPathQuery_RemoteViaRegistry(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", Output: dirA,
	}); err != nil {
		t.Fatal(err)
	}
	if err := render.Render(render.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-b", Output: dirB,
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

	dgid := gidBytesForPort(t, portsA[0].DefaultGID)
	send := makeSAPathQueryMAD(dgid)
	h := &portHandle{}
	if !client.trySAPathQuery(h, send) {
		t.Fatal("expected remote SA path query handled")
	}
	dlidOff, _ := pathRecordDLIDOffset(h.recvQ[0][umadMADOffset:])
	gotLID := binary.BigEndian.Uint16(h.recvQ[0][umadMADOffset+dlidOff:])
	if gotLID != portsA[0].LID {
		t.Fatalf("dlid: got 0x%04x want 0x%04x", gotLID, portsA[0].LID)
	}
}

func makeSAPathQueryMAD(dgid []byte) []byte {
	mad := make([]byte, umadMADOffset+256)
	binary.BigEndian.PutUint16(mad[umadLIDOffset:], 0x0001) // SM lid
	m := mad[umadMADOffset:]
	// Match libibumad SA GET PathRecord layout seen from ibping -G (attr id offset varies).
	m[20] = ibSAMethodGet
	binary.BigEndian.PutUint16(m[24:26], ibSAAttrPathRecord)
	copy(m[ibPathRecDGIDOff:ibPathRecDGIDOff+16], dgid)
	return mad
}

func gidBytesForPort(t *testing.T, gid string) []byte {
	t.Helper()
	var b [16]byte
	parseGIDInto(b[:], gid)
	return b[:]
}
