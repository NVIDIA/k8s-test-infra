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

package nicsurfaces

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A listing Write prints reads back as the same node: every record with its
// source, with each value on one line, the sources not captured, and each
// object's first source.
func TestListingReadsBackAsTheNodeItCameFrom(t *testing.T) {
	t.Parallel()
	for _, capture := range []string{"gb300-ib", "gb300-roce"} {
		t.Run(capture, func(t *testing.T) {
			t.Parallel()
			n, err := Read(filepath.Join(capturesDir, capture, "node-a"))
			require.NoError(t, err)
			var listing bytes.Buffer
			require.NoError(t, n.Write(&listing))

			path := filepath.Join(t.TempDir(), "nic-surfaces.tsv")
			require.NoError(t, os.WriteFile(path, listing.Bytes(), 0o600))
			back, err := Read(path)
			require.NoError(t, err)
			// A listing prints each value on one line.
			for obj := range n.Records {
				for attr, v := range n.Records[obj] {
					n.Records[obj][attr] = OneLine(v)
				}
			}
			require.Equal(t, n.Records, back.Records)
			require.Equal(t, n.Missing, back.Missing)
			for obj := range n.Records {
				for attr := range n.Records[obj] {
					require.Equal(t, n.Sources(obj, attr), back.Sources(obj, attr), "%s %s", obj, attr)
				}
				require.Equal(t, n.Source(obj, ""), back.Source(obj, ""), obj)
			}
			var again bytes.Buffer
			require.NoError(t, back.Write(&again))
			require.Equal(t, listing.String(), again.String())
		})
	}
}

func TestListingLinesNeedFourFields(t *testing.T) {
	t.Parallel()
	_, err := ReadListing(strings.NewReader("pci/0000:03:00.0\tclass\t0x020700\n"))
	require.ErrorContains(t, err, "line 1: want object, attribute, value and source")
}

// New starts a node that records can be set on, as a declared node's are.
func TestNewNodeTakesRecords(t *testing.T) {
	t.Parallel()
	n := New()
	n.Set("pci/0000:03:00.0", "class", "0x020700", pciNICSysfs)
	n.Set("port/0000:03:00.0/dev/1", "link_local_gid_from_mac", "yes", rdmaSysfs, netSysfs)
	n.Missing[fwFlint] = "no such file"
	var out bytes.Buffer
	require.NoError(t, n.Write(&out))
	require.Equal(t, "# not captured: fw-flint.txt (no such file)\npci/0000:03:00.0\tclass\t0x020700\tpci-nic-sysfs.txt\n"+
		"port/0000:03:00.0/dev/1\tlink_local_gid_from_mac\tyes\trdma-sysfs.txt+net-sysfs.txt\n", out.String())
}

// SourcesOf names the files every record of the committed captures comes from.
func TestSourceOfNamesEachRecordsFile(t *testing.T) {
	t.Parallel()
	for _, capture := range []string{"gb300-ib", "gb300-roce"} {
		n, err := Read(filepath.Join(capturesDir, capture, "node-a"))
		require.NoError(t, err)
		for obj := range n.Records {
			for attr := range n.Records[obj] {
				require.Equal(t, n.Sources(obj, attr), SourcesOf(obj, attr), "%s: %s %s", capture, obj, attr)
			}
		}
	}
}
