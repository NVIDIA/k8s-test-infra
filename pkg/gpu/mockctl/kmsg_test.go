// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mockctl

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

func TestXidRecord_DropsThePCIFunction(t *testing.T) {
	// The driver names the device, not the function: a real Xid reads
	// "(PCI:0000:1A:00)" even though NVML reports the BDF as 0000:1A:00.0.
	require.Equal(t, "kernel: NVRM: Xid (PCI:0000:1A:00): 79", XidRecord("0000:1A:00.0", 79))
}

func TestXidRecord_IsMatchedByTheAgentPattern(t *testing.T) {
	m := agentXidPattern.FindStringSubmatch(XidRecord("0000:1A:00.0", 48))
	require.Len(t, m, 3, "emitted record must match the pattern health agents use")
	require.Equal(t, "0000:1A:00", m[1], "captured PCI address")
	require.Equal(t, "48", m[2], "captured Xid code")
}

func TestEmitXid_WritesOneRecordPerBusID(t *testing.T) {
	kmsg := fakeKernelLog(t)

	wrote, err := EmitXid(kmsg, []string{"0000:1A:00.0", "0000:1B:00.0"}, 79)
	require.NoError(t, err)
	require.True(t, wrote)

	require.Equal(t,
		"kernel: NVRM: Xid (PCI:0000:1A:00): 79\nkernel: NVRM: Xid (PCI:0000:1B:00): 79\n",
		readFile(t, kmsg))
}

func TestEmitXid_AppendsRatherThanReplacing(t *testing.T) {
	kmsg := fakeKernelLog(t)

	for _, code := range []uint64{79, 48} {
		wrote, err := EmitXid(kmsg, []string{"0000:1A:00.0"}, code)
		require.NoError(t, err)
		require.True(t, wrote)
	}

	require.Equal(t,
		"kernel: NVRM: Xid (PCI:0000:1A:00): 79\nkernel: NVRM: Xid (PCI:0000:1A:00): 48\n",
		readFile(t, kmsg), "a second Xid must not overwrite the first")
}

func TestEmitXid_AbsentKernelLogIsNotAnError(t *testing.T) {
	// The common case off a node: no /dev/kmsg to write to. The NVML side of
	// the injection stands on its own, so this reports "not written", not an
	// error.
	kmsg := filepath.Join(t.TempDir(), "kmsg")

	wrote, err := EmitXid(kmsg, []string{"0000:1A:00.0"}, 79)
	require.NoError(t, err)
	require.False(t, wrote)
	require.NoFileExists(t, kmsg, "a missing kernel log must not be created")
}

func TestEmitXid_NothingToEmit(t *testing.T) {
	kmsg := fakeKernelLog(t)

	for name, busIDs := range map[string][]string{
		"no bus ids":   nil,
		"empty bus id": {""},
	} {
		wrote, err := EmitXid(kmsg, busIDs, 79)
		require.NoErrorf(t, err, "%s", name)
		require.Falsef(t, wrote, "%s", name)
		require.Emptyf(t, readFile(t, kmsg), "%s", name)
	}

	wrote, err := EmitXid("", []string{"0000:1A:00.0"}, 79)
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
