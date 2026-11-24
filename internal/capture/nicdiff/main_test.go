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
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicsurfaces"
)

// realFiles is a cut-down GB300 InfiniBand node, as collect network writes
// it: one ASIC with its SMI plane device and Data Direct function, and the GPU
// that pairs with it.
var realFiles = map[string]string{
	"pci-nic-sysfs.txt": "== 0000:03:00.0\nclass=0x020700\nlocal_cpulist=0-71\nfiles=class,device,vpd\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n" +
		"== 0009:03:00.0\ndevice=0x2100\nclass=0x020000\ndevpath=/sys/devices/pci0009:00/0009:03:00.0\nphysfn=0000:03:00.0\n",
	"pci-vpd-fields.txt": "== 0000:03:00.0\n[SN] Serial number: MT1\n[V3] Vendor specific: aaaa\n[VU] Vendor specific: 1111MLNXS0D0F0\n" +
		"== 0009:03:00.0\n[V3] Vendor specific: aaaa\n[VU] Vendor specific: 2222MLNXDDIS0D0F0\n",
	"pci-all-functions.txt": "0009:06:00.0 vendor=0x10de device=0x2941 class=0x030200 numa=1 root=pci0009:00\n",
	"rdma-sysfs.txt": "== mlx5_0\nnode_guid=0000:0000:0000:0001\nsys_image_guid=0000:0000:0000:0001\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n" +
		"-- port 1\nlid=0x10\nrate=800 Gb/sec (2X XDR)\nlink_layer=InfiniBand\ngid[0]=fe80:0000:0000:0000:0000:0000:0000:0001 type=IB/RoCE v1 ndev=\n" +
		"== smi0\nnode_guid=0000:0000:0000:0001\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n-- port 1\nlid=0x10\n",
	"rdma-char-devices.txt": "== /dev/infiniband\nissm6 type=char dev=e7:46 mode=660\numad0 type=char dev=e7:0 mode=660\nuverbs0 type=char dev=e7:c0 mode=666\n" +
		"== /sys/class/infiniband_verbs\nuverbs0 ibdev=mlx5_0 dev=231:192 abi_version=1 backlink=231:192\n",
	"host-ibstat.txt": "CA 'mlx5_0'\n\tCA type: MT4131\n",
}

// mockFiles simulates it with gaps: no SMI device, a different rate, a renamed
// RDMA device, a verbs node at another minor, and no ibstat.
var mockFiles = map[string]string{
	"pci-nic-sysfs.txt": "== 0000:03:00.0\nclass=0x020700\nlocal_cpulist=0-7\nfiles=class,device\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n" +
		"== 0009:03:00.0\ndevice=0x2100\nclass=0x020000\ndevpath=/sys/devices/pci0009:00/0009:03:00.0\nphysfn=0000:03:00.0\n",
	"pci-vpd-fields.txt": "== 0000:03:00.0\n[SN] Serial number: MT9\n[V3] Vendor specific: bbbb\n[VU] Vendor specific: 3333MLNXS0D0F0\n" +
		"== 0009:03:00.0\n[V3] Vendor specific: bbbb\n[VU] Vendor specific: 4444MLNXDDIS0D0F0\n",
	"pci-all-functions.txt": "0009:06:00.0 vendor=0x10de device=0x2941 class=0x030200 numa=1 root=pci0009:00\n",
	"rdma-sysfs.txt": "== rdma_rail0\nnode_guid=0000:0000:0000:0009\nsys_image_guid=0000:0000:0000:0009\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n" +
		"-- port 1\nlid=0x20\nrate=400 Gb/sec (4X NDR)\nlink_layer=InfiniBand\ngid[0]=fe80:0000:0000:0000:0000:0000:0000:0009 type=IB/RoCE v1 ndev=\n",
	"rdma-char-devices.txt": "== /dev/infiniband\nuverbs3 type=char dev=e7:c3 mode=666\n" +
		"== /sys/class/infiniband_verbs\nuverbs3 ibdev=rdma_rail0 dev=231:195 abi_version=1 backlink=231:195\n",
	"host-ibstat.txt": "",
	"errors.log":      "sh: ibstat: not found\nFAILED: host-ibstat.txt\n",
}

func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600))
	}
	return dir
}

func readNode(t *testing.T, files map[string]string) *nicsurfaces.Node {
	t.Helper()
	n, err := nicsurfaces.Read(writeDir(t, files))
	require.NoError(t, err)
	return n
}

// byObject indexes checks by "rule object" for assertions.
func byObject(checks []check) map[string]check {
	out := map[string]check{}
	for _, c := range checks {
		out[c.Rule+" "+c.Object] = c
	}
	return out
}

func compareFiles(t *testing.T, hw, sim map[string]string) map[string]check {
	t.Helper()
	return byObject(compare(readNode(t, hw), readNode(t, sim)))
}

func TestIdentifiersAreComparedThroughRelations(t *testing.T) {
	t.Parallel()
	checks := compareFiles(t, realFiles, mockFiles)

	// Different GUIDs and VPD IDs are not differences ...
	require.Equal(t, notCompared, checks["rdma.node_guid rdma/0000:03:00.0/dev"].Status)
	require.Equal(t, causeByDesign, checks["pci.vpd.V3 pci/0000:03:00.0"].Cause)
	// ... but what they relate is compared: both pair the PF with the Data
	// Direct function under the GPU's root port, by V3.
	require.Equal(t, pass, checks["rel.data_direct.by_v3 rel/data_direct/0000:03:00.0"].Status)
	require.Equal(t, pass, checks["rel.data_direct.gpu rel/data_direct/0000:03:00.0"].Status)
	require.Equal(t, "0009:06:00.0", checks["rel.data_direct.gpu rel/data_direct/0000:03:00.0"].Real)
	require.Equal(t, pass, checks["rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/0000:03:00.0"].Status)
	require.Equal(t, "self", checks["rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/0000:03:00.0"].Real)
	require.Equal(t, pass, checks["port.gid0_is_node_guid port/0000:03:00.0/dev/1"].Status)
	// The VU marker says which kind of function holds the field.
	require.Equal(t, pass, checks["pci.vpd.VU pci/0000:03:00.0"].Status)

	// The SMI device is missing, each of its attributes a failure, and with it
	// the relations it has.
	require.Equal(t, fail, checks["rdma.name rdma/0000:03:00.0/smi"].Status)
	require.Equal(t, "only on the real node", checks["rdma.name rdma/0000:03:00.0/smi"].Detail)
	require.Equal(t, fail, checks["rdma.node_guid rdma/0000:03:00.0/smi"].Status)
	require.Equal(t, fail, checks["rel.smi_node_guid.is_parent_node_guid rel/smi_node_guid/0000:03:00.0"].Status)
}

func TestValuesAreComparedByTheirRule(t *testing.T) {
	t.Parallel()
	checks := compareFiles(t, realFiles, mockFiles)

	require.Equal(t, pass, checks["pci.class pci/0000:03:00.0"].Status)
	rate := checks["port.rate port/0000:03:00.0/dev/1"]
	require.Equal(t, fail, rate.Status)
	require.Equal(t, "800 Gb/sec (2X XDR)", rate.Real)
	require.Equal(t, "400 Gb/sec (4X NDR)", rate.Sim)

	files := checks["pci.files pci/0000:03:00.0"]
	require.Equal(t, fail, files.Status)
	require.Equal(t, "not simulated: vpd", files.Detail)

	// One GID each: the values differ, the count does not.
	require.Equal(t, pass, checks["port.gids port/0000:03:00.0/dev/1"].Status)
	// A verbs node at another minor still matches; device files by kind.
	require.Equal(t, pass, checks["uverbs.devnode uverbs/0000:03:00.0/dev"].Status)
	require.Equal(t, pass, checks["uverbs.function_backlink uverbs/0000:03:00.0/dev"].Status)
	nodes := checks["node.dev_infiniband node"]
	require.Equal(t, "not simulated: issm,umad", nodes.Detail)
	// A renamed device and the host's CPUs are not differences.
	require.Equal(t, notCompared, checks["rdma.name rdma/0000:03:00.0/dev"].Status)
	require.Equal(t, notCompared, checks["pci.local_cpulist pci/0000:03:00.0"].Status)
}

func TestARecordFromAFileOneSideLacksIsABlindSpot(t *testing.T) {
	t.Parallel()
	checks := compareFiles(t, realFiles, mockFiles)

	// ibstat failed on the simulated node; its record is keyed by the device.
	c := checks["ibstat.CA_type ibstat/0000:03:00.0/dev"]
	require.Equal(t, notCompared, c.Status)
	require.Equal(t, causeBlindSpot, c.Cause)
	require.Equal(t, "host-ibstat.txt was not captured on the simulated node: the capturing pod lacks the tool", c.Note)
}

func TestCausesClassifyFailuresAndUnclassifiedOnesFail(t *testing.T) {
	t.Parallel()
	hw, sim := writeDir(t, realFiles), writeDir(t, mockFiles)

	var out, errs bytes.Buffer
	require.Equal(t, 1, run(options{failed: true}, hw, sim, &out, &errs))
	require.Contains(t, out.String(), "FAIL  port.rate  port/0000:03:00.0/dev/1  real=\"800 Gb/sec (2X XDR)\" simulated=\"400 Gb/sec (4X NDR)\"  [unclassified]")
	require.NotContains(t, out.String(), "PASS")

	causes := filepath.Join(t.TempDir(), "causes.yaml")
	require.NoError(t, os.WriteFile(causes, []byte(`
- match: '^(rdma|rel\.smi_\w+|port)(\.\S+)? .*/smi'
  cause: by design
  note: cluster software creates plane devices
- match: '^(port\.rate|pci\.files|node\.dev_infiniband) '
  cause: schema gap
  note: not modelled yet
`), 0o600))
	out.Reset()
	require.Equal(t, 0, run(options{causes: causes}, hw, sim, &out, &errs), errs.String())
	// A difference by design is out of scope: not compared, and still shown.
	require.Contains(t, out.String(), "SKIP  rdma.name  rdma/0000:03:00.0/smi  only on the real node (smi0)  [by design: cluster software creates plane devices]")
	require.Contains(t, out.String(), "[schema gap: not modelled yet]")
	require.Contains(t, out.String(), "unclassified 0")
}

func TestJSONHasOneObjectPerCheckAndASummary(t *testing.T) {
	t.Parallel()
	hw := writeDir(t, realFiles)

	var out, errs bytes.Buffer
	require.Equal(t, 0, run(options{json: true}, hw, hw, &out, &errs), errs.String())
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var last struct {
		Summary summary `json:"summary"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last))
	require.Zero(t, last.Summary.Failed[causeUnclassified])
	require.Equal(t, last.Summary.Checks, last.Summary.Passed)
	var first check
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	require.NotEmpty(t, first.Rule)
}

func TestBadInputIsAnError(t *testing.T) {
	t.Parallel()
	var out, errs bytes.Buffer
	require.Equal(t, 1, run(options{}, t.TempDir(), writeDir(t, mockFiles), &out, &errs))
	require.Contains(t, errs.String(), "no NIC surface files")

	causes := filepath.Join(t.TempDir(), "causes.yaml")
	require.NoError(t, os.WriteFile(causes, []byte("- {match: x, cause: bad luck}\n"), 0o600))
	_, err := readCauses(causes)
	require.ErrorContains(t, err, `cause "bad luck"`)
}

func TestADeviceWithoutAPCIFunctionIsComparedWithTheRealDeviceOfItsName(t *testing.T) {
	t.Parallel()
	checks := compareFiles(t, map[string]string{
		"rdma-sysfs.txt": "== mlx5_0\nhca_type=MT4131\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n-- port 1\nrate=800 Gb/sec (2X XDR)\n",
	}, map[string]string{
		"rdma-sysfs.txt": "== mlx5_0\nhca_type=MT4129\ndevpath=/sys/devices/virtual/infiniband/mlx5_0\n-- port 1\nrate=400 Gb/sec (4X NDR)\n",
	})

	require.Equal(t, fail, checks["rdma.pci_function rdma/0000:03:00.0/dev"].Status)
	require.Equal(t, "MT4129", checks["rdma.hca_type rdma/0000:03:00.0/dev"].Sim)
	require.Equal(t, "400 Gb/sec (4X NDR)", checks["port.rate port/0000:03:00.0/dev/1"].Sim)
	require.NotContains(t, checks, "rdma rdma/unbound/mlx5_0/dev")
}

func TestTopoRecordsFollowTheDeviceAndNeedNvidiaSmiOnBothSides(t *testing.T) {
	t.Parallel()
	topo := func(name, rel string) map[string]string {
		return map[string]string{
			"rdma-sysfs.txt": "== " + name + "\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n",
			"smi-topo-m.txt": "\tGPU0\tNIC0\nGPU0\t X \t" + rel + "\nNIC0\t" + rel + "\t X \n\nNIC Legend:\n\n  NIC0: " + name + "\n",
		}
	}
	checks := compareFiles(t, topo("mlx5_0", "NODE"), topo("rdma_rail0", "SYS"))
	require.Equal(t, fail, checks["topo.GPU0 topo/0000:03:00.0/dev"].Status)

	without := topo("mlx5_0", "NODE")
	delete(without, "smi-topo-m.txt")
	checks = compareFiles(t, topo("mlx5_0", "NODE"), without)
	require.Equal(t, causeBlindSpot, checks["topo.GPU0 topo/0000:03:00.0/dev"].Cause)
	require.Equal(t, "smi-topo-m.txt was not captured on the simulated node: no such file", checks["topo.GPU0 topo/0000:03:00.0/dev"].Note)
}

func TestACauseNoFailureMatchesIsReported(t *testing.T) {
	t.Parallel()
	known, err := readCausesFrom(t, `
- {match: '^port\.rate ', cause: schema gap}
- {match: '^port\.rate ', cause: rule bug}
- {match: '^nothing ', cause: schema gap}
`)
	require.NoError(t, err)
	_, unused := classify(compare(readNode(t, realFiles), readNode(t, mockFiles)), known)
	require.Len(t, unused, 2, "the shadowed entry and the one that matches nothing")
	require.Equal(t, causeRuleBug, unused[0].Cause)
}

func readCausesFrom(t *testing.T, yaml string) ([]cause, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "causes.yaml")
	require.NoError(t, os.WriteFile(p, []byte(yaml), 0o600))
	return readCauses(p)
}

func TestReportGroupsChecksBySurfaceAndCause(t *testing.T) {
	t.Parallel()
	var out, errs bytes.Buffer
	require.Equal(t, 1, run(options{report: true}, writeDir(t, realFiles), writeDir(t, mockFiles), &out, &errs))
	report := out.String()
	require.Contains(t, report, "## By surface")
	require.Contains(t, report, "`port.rate`")
	require.Contains(t, report, "| `rel.data_direct.by_v3` | 1 |")
	require.Contains(t, report, "host-ibstat.txt was not captured on the simulated node: the capturing pod lacks the tool")
}

// A side without rdma-sysfs.txt cannot key the tools' records by device, so
// they are a blind spot rather than two one-sided objects.
func TestToolRecordsAreABlindSpotWhereDevicesCannotBeKeyed(t *testing.T) {
	t.Parallel()
	ibstat := "CA 'mlx5_0'\n\tCA type: MT4131\n"
	checks := compare(
		readNode(t, map[string]string{"rdma-sysfs.txt": "== mlx5_0\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n", "host-ibstat.txt": ibstat}),
		readNode(t, map[string]string{"host-ibstat.txt": ibstat}))
	require.NotEmpty(t, checks)
	for _, c := range checks {
		require.NotEqual(t, fail, c.Status, "%s %s", c.Rule, c.Object)
	}
	require.Equal(t, "rdma-sysfs.txt, which keys the RDMA devices, was not captured on the simulated node: no such file",
		byObject(checks)["ibstat.CA_type ibstat/mlx5_0"].Note)
}

// Auxiliary devices are numbered in probe order, so the PCI function's file
// list is compared without their numbers.
func TestAuxiliaryDeviceNumbersAreNotCompared(t *testing.T) {
	t.Parallel()
	pci := func(files string) map[string]string {
		return map[string]string{"pci-nic-sysfs.txt": "== 0000:03:00.0\nclass=0x020700\nfiles=" + files + "\n"}
	}
	checks := compareFiles(t, pci("class,mlx5_core.eth.0,mlx5_core.rdma.0"), pci("class,mlx5_core.eth.2,mlx5_core.rdma.3"))
	require.Equal(t, pass, checks["pci.files pci/0000:03:00.0"].Status)
	checks = compareFiles(t, pci("class,mlx5_core.eth.0,mlx5_core.rdma.0"), pci("class,mlx5_core.eth.2"))
	require.Equal(t, "not simulated: mlx5_core.rdma", checks["pci.files pci/0000:03:00.0"].Detail)
}

// A system image GUID is compared by whose node GUID it is: the device's own,
// another device's, or none. Pointing it at the wrong device fails.
func TestASystemImageGUIDIsComparedByWhoseNodeGUIDItIs(t *testing.T) {
	t.Parallel()
	sysfs := func(second string) map[string]string {
		return map[string]string{"rdma-sysfs.txt": "" +
			"== mlx5_0\nnode_guid=0000:0000:0000:0011\nsys_image_guid=0000:0000:0000:0011\ndevpath=/sys/devices/pci0000:00/0000:03:00.0\n" +
			"== mlx5_1\nnode_guid=0000:0000:0000:0012\nsys_image_guid=" + second + "\ndevpath=/sys/devices/pci0000:00/0000:03:00.1\n"}
	}
	shared := sysfs("0000:0000:0000:0011")
	for _, mutated := range []string{"0000:0000:0000:0012", "0000:0000:0000:00aa"} {
		c := compareFiles(t, shared, sysfs(mutated))["rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/0000:03:00.1"]
		require.Equal(t, fail, c.Status, mutated)
		require.Equal(t, "0000:03:00.0", c.Real)
	}
	require.Equal(t, pass, compareFiles(t, shared, shared)["rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/0000:03:00.1"].Status)
}

// A BlueField's VPD VU starts with its serial number; a VU that no longer does
// fails, though its MLNX marker still matches.
func TestAVUThatStopsStartingWithItsSerialFails(t *testing.T) {
	t.Parallel()
	vpd := func(vu string) map[string]string {
		return map[string]string{
			"pci-nic-sysfs.txt":  "== 0016:01:00.0\nclass=0x020000\n",
			"pci-vpd-fields.txt": "== 0016:01:00.0\n[SN] Serial number: MT0000X00001\n[VU] Vendor specific: " + vu + "\n",
		}
	}
	checks := compareFiles(t, vpd("MT0000X00001MLNXS0D0F0"), vpd("MT0000X00099MLNXS0D0F0"))
	require.Equal(t, fail, checks["rel.vu_serial.starts_with_serial rel/vu_serial/0016:01:00.0"].Status)
	require.Equal(t, pass, checks["pci.vpd.VU pci/0016:01:00.0"].Status)
}

// An object one side lacks fails once per attribute, so rendering it can only
// lower the failure count: each attribute that then matches passes.
func TestRenderingAMissingObjectNeverRaisesTheFailures(t *testing.T) {
	t.Parallel()
	failures := func(sim map[string]string) int {
		hw := map[string]string{"pci-nic-sysfs.txt": "== 0000:03:00.0\nclass=0x020700\ndevice=0x1023\nnuma_node=0\n"}
		n := 0
		for _, c := range compareFiles(t, hw, sim) {
			if c.Status == fail {
				n++
			}
		}
		return n
	}
	missing := failures(map[string]string{"pci-nic-sysfs.txt": ""})
	rendered := failures(map[string]string{"pci-nic-sysfs.txt": "== 0000:03:00.0\nclass=0x020700\ndevice=0x1021\nnuma_node=0\n"})
	require.Greater(t, missing, rendered)
	require.Equal(t, 1, rendered, "only the device ID differs")
}
