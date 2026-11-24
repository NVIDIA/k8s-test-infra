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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const capturesDir = "../../../tests/hardware-captures"

// cx8PFs returns the ConnectX-8 network PFs of class class.
func cx8PFs(n *Node, class string) []string {
	var out []string
	for _, o := range n.Objects("pci") {
		if d, _ := n.Get(o, "device"); d == "0x1023" {
			if c, _ := n.Get(o, "class"); c == class {
				out = append(out, o)
			}
		}
	}
	return out
}

// The committed GB300 InfiniBand tray reads as the hardware is: four
// ConnectX-8 ASICs, one PF each, split into four planes, each paired with the
// Data Direct function that shares its VPD V3, and the relations inside values
// survive the scrub.
func TestCommittedGB300InfiniBandCaptureReadsAsTheTrayIs(t *testing.T) {
	t.Parallel()
	n, err := Read(filepath.Join(capturesDir, "gb300-ib", "node-a"))
	require.NoError(t, err)

	pfs := cx8PFs(n, "0x020700")
	require.Len(t, pfs, 4)
	for _, pf := range pfs {
		bdf := pf[len("pci/"):]
		require.Equal(t, "4", n.Records["fw/"+bdf]["NUM_OF_PLANES_P1"], pf)
		require.Equal(t, "InfiniBand", n.Records["port/"+bdf+"/dev/1"]["link_layer"], pf)
		require.Equal(t, "yes", n.Records["port/"+bdf+"/dev/1"]["gid0_is_node_guid"], pf)
		require.Equal(t, "yes", n.Records["uverbs/"+bdf+"/dev"]["function_backlink"], pf)

		v3, ok := n.Get(pf, "vpd.V3")
		require.True(t, ok, pf)
		var dataDirect []string
		for _, o := range n.Objects("pci") {
			if d, _ := n.Get(o, "device"); d == "0x2100" && n.Records[o]["vpd.V3"] == v3 {
				dataDirect = append(dataDirect, o)
			}
		}
		require.Len(t, dataDirect, 1, "%s pairs with one Data Direct function by V3", pf)
	}
	ipoib := 0
	for _, o := range n.Objects("net") {
		if v, _ := n.Get(o, "address_has_node_guid"); v == "yes" {
			ipoib++
		}
	}
	require.Equal(t, 4, ipoib, "an IPoIB interface per ASIC ends in its port GUID")
	require.Equal(t, "no such file", n.Missing["devlink-dev-info.txt"], "the pod had no devlink")
}

// The committed GB300 RoCE tray: four PFs per ASIC, an RDMA device spanning
// them with 72 ports, and link-local GIDs that carry their interface's MAC.
func TestCommittedGB300RoCECaptureReadsAsTheTrayIs(t *testing.T) {
	t.Parallel()
	n, err := Read(filepath.Join(capturesDir, "gb300-roce", "node-a"))
	require.NoError(t, err)

	require.Len(t, cx8PFs(n, "0x020000"), 16)
	rails := 0
	for _, o := range n.Objects("rdma") {
		if p, _ := n.Get(o, "ports"); p == "72" {
			rails++
		}
	}
	require.Equal(t, 4, rails)
	fromMAC := 0
	for _, o := range n.Objects("port") {
		if v, _ := n.Get(o, "link_local_gid_from_mac"); v == "yes" {
			fromMAC++
		}
	}
	require.Positive(t, fromMAC)
}
