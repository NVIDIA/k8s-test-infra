// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kernellog

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// The shape health agents key off — NVSentinel's syslog-health-monitor matches
// this exact pattern, and kmsg readers scan for the same "NVRM: Xid" text.
// Pinned here so a change to the emitted record cannot silently stop matching.
var agentXidPattern = regexp.MustCompile(`NVRM: Xid \(PCI:([0-9a-fA-F:.]+)\): (\d+)`)

func TestXidRecord_SpellsTheAddressAsTheDriverDoes(t *testing.T) {
	// The driver names the device, not the function, and prints hex in lower
	// case: a real Xid reads "(PCI:0000:1a:00)" where NVML reports the BDF as
	// 0000:1A:00.0. The case matters because an agent correlating the Xid with
	// the simulated /sys/bus/pci tree finds lower case there.
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1a:00): 79", xidRecord("0000:1A:00.0", 79))
}

func TestXidRecord_IsMatchedByTheAgentPattern(t *testing.T) {
	m := agentXidPattern.FindStringSubmatch(xidRecord("0000:1A:00.0", 48))
	require.Len(t, m, 3, "emitted record must match the pattern health agents use")
	require.Equal(t, "0000:1a:00", m[1], "captured PCI address")
	require.Equal(t, "48", m[2], "captured Xid code")
}

// Each Xid is its own kernel message, and a later one never displaces what the
// log already holds — a driver's printks accumulate.
func TestEmitXid_AppendsOneRecordPerCall(t *testing.T) {
	kmsg := fakeKernelLog(t)

	for _, code := range []uint64{79, 48} {
		wrote, err := emitXid(kmsg, "0000:1A:00.0", code)
		require.NoError(t, err)
		require.True(t, wrote)
	}

	require.Equal(t,
		"kernel: NVRM: Xid (PCI:0000:1a:00): 79\nkernel: NVRM: Xid (PCI:0000:1a:00): 48\n",
		readFile(t, kmsg))
}

func TestEmitXid_AbsentKernelLogIsNotAnError(t *testing.T) {
	// The common case off a node: no /dev/kmsg to write to. The NVML side of
	// the injection stands on its own, so this reports "not written", not an
	// error.
	kmsg := filepath.Join(t.TempDir(), "kmsg")

	wrote, err := emitXid(kmsg, "0000:1A:00.0", 79)
	require.NoError(t, err)
	require.False(t, wrote)
	require.NoFileExists(t, kmsg, "a missing kernel log must not be created")
}

func TestEmitXid_NothingToEmit(t *testing.T) {
	kmsg := fakeKernelLog(t)

	wrote, err := emitXid(kmsg, "", 79)
	require.NoError(t, err)
	require.False(t, wrote, "a device with no address is named nowhere")
	require.Empty(t, readFile(t, kmsg))

	wrote, err = emitXid("", "0000:1A:00.0", 79)
	require.NoError(t, err)
	require.False(t, wrote, "an empty path disables emission")
}

// fakeKernelLog stands in for /dev/kmsg: a writable file that already exists,
// which is what the availability check looks for.
func fakeKernelLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kmsg")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
