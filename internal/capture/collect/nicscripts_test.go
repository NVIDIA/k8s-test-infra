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

package collect

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The scripts read a node's /sys and /dev, so only a capture shows what they
// print; here each must at least parse.
func TestNICScriptsParse(t *testing.T) {
	t.Parallel()
	for name, script := range map[string]string{
		"rdma-sysfs": rdmaSysfsScript, "rdma-counters": rdmaCountersScript, "rdma-char-devices": rdmaCharDevicesScript,
		"pci-nic-sysfs": pciNICSysfsScript, "pci-vpd": pciVPDScript, "sys-module-rdma": sysModuleRDMAScript,
		"net-ecn-qos": netECNQoSScript, "net-ethtool": netEthtoolScript, "net-ethtool-m": netEthtoolModuleScript,
		"devlink-eswitch": devlinkEswitchScript, "fw-mlxconfig": fwMLXConfigScript, "fw-flint": fwFlintScript,
		"cable-mlxlink": cableMLXLinkScript,
	} {
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", name, out)
	}
}

// vpdBytes builds vital product data: the product name, one read-only keyword
// section ending in a checksum, and the end tag.
func vpdBytes(name string, fields [][2]string) []byte {
	b := []byte{0x82, byte(len(name)), 0}
	b = append(b, name...)
	var ro []byte
	for _, f := range append(fields, [2]string{"RV", "\x00"}) {
		ro = append(ro, f[0][0], f[0][1], byte(len(f[1])))
		ro = append(ro, f[1]...)
	}
	b = append(b, 0x90, byte(len(ro)), byte(len(ro)>>8))
	return append(append(b, ro...), 0x78)
}

func TestVPDIsPrintedAsLSPCIPrintsIt(t *testing.T) {
	t.Parallel()
	vpd := vpdBytes("ConnectX-8 C8280Z  ", [][2]string{
		{"PN", "900-9X86E-00CX-ST0"}, {"EC", "A6"}, {"SN", "MT0000X00001"},
		{"V3", "0123456789abcdef0123456789abcdef"}, {"VA", "MLX:MN=MLNX"}, {"ZZ", "x"},
	})
	var od strings.Builder
	for i, v := range vpd {
		if i%16 == 0 && i > 0 {
			od.WriteString("\n")
		}
		fmt.Fprintf(&od, " %3d", v)
	}
	cmd := exec.Command("awk", vpdAwk)
	cmd.Stdin = strings.NewReader(od.String() + "\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Equal(t, `Product Name: ConnectX-8 C8280Z
[PN] Part number: 900-9X86E-00CX-ST0
[EC] Engineering changes: A6
[SN] Serial number: MT0000X00001
[V3] Vendor specific: 0123456789abcdef0123456789abcdef
[VA] Vendor specific: MLX:MN=MLNX
[ZZ] Unknown: x
`, string(out))
}
