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

package nicdiff

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicsurfaces"
)

const gb300IB = "../../../tests/hardware-captures/gb300-ib/node-a"

// copyCapture copies a committed capture to a temporary directory, applying
// edit to each file's text.
func copyCapture(t *testing.T, src string, edit func(file, text string) string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	require.NoError(t, err)
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dst, e.Name()), []byte(edit(e.Name(), string(raw))), 0o600))
	}
	return dst
}

// replaceInBlock replaces old with repl in the "== header" section of text.
func replaceInBlock(t *testing.T, text, header, old, repl string) string {
	t.Helper()
	start := strings.Index(text, "== "+header+"\n")
	require.GreaterOrEqual(t, start, 0, header)
	end := strings.Index(text[start+1:], "\n== ")
	if end < 0 {
		end = len(text) - start - 1
	}
	block := text[start : start+1+end]
	require.Contains(t, block, old, header)
	return text[:start] + strings.Replace(block, old, repl, 1) + text[start+1+end:]
}

func failures(t *testing.T, realDir, mockDir string) []string {
	t.Helper()
	hw, err := nicsurfaces.Read(realDir)
	require.NoError(t, err)
	sim, err := nicsurfaces.Read(mockDir)
	require.NoError(t, err)
	var out []string
	for _, c := range compare(hw, sim) {
		if c.Status == fail {
			out = append(out, c.Rule+" "+c.Object)
		}
	}
	sort.Strings(out)
	return out
}

// Deliberate mistakes on the committed GB300 InfiniBand tray are each caught:
// a system image GUID that names the wrong device, or no device, and a
// BlueField-3 VU that no longer starts with its serial number, though its
// marker still matches. The unchanged capture matches itself.
func TestMistakesOnTheGB300InfiniBandCaptureAreCaught(t *testing.T) {
	t.Parallel()
	require.Empty(t, failures(t, gb300IB, gb300IB))

	mutated := copyCapture(t, gb300IB, func(file, text string) string {
		switch file {
		case "rdma-sysfs.txt":
			// mlx5_1's system image GUID becomes mlx5_0's node GUID, and
			// mlx5_5's, which is mlx5_4's node GUID, one of its own.
			text = replaceInBlock(t, text, "mlx5_1", "sys_image_guid=0000:0000:0000:0002", "sys_image_guid=0000:0000:0000:0001")
			text = replaceInBlock(t, text, "mlx5_5", "sys_image_guid=0000:0000:0000:0007", "sys_image_guid=0000:0000:0000:00aa")
		case "pci-vpd-fields.txt":
			text = replaceInBlock(t, text, "0016:01:00.0", "[VU] Vendor specific: 1650000000009MLNXS0D0F0", "[VU] Vendor specific: 1650000000099MLNXS0D0F0")
		}
		return text
	})
	require.Equal(t, []string{
		"rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/0002:03:00.0",
		"rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/0016:01:00.1",
		"rel.vu_serial.starts_with_serial rel/vu_serial/0016:01:00.0",
	}, failures(t, gb300IB, mutated))
}
