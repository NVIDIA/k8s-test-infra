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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/cmdline"
)

// writeNode writes a capture node directory holding files.
func writeNode(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600))
	}
	return dir
}

func read(t *testing.T, files map[string]string) *Node {
	t.Helper()
	n, err := Read(writeNode(t, files))
	require.NoError(t, err)
	return n
}

// An InfiniBand ConnectX function with an SMI sub-device, as on a GB300 tray.
const ibRDMASysfs = `== mlx5_7
node_guid=0000:0000:0000:0001
node_desc=node-a mlx5_7
node_type=1: CA
sys_image_guid=0000:0000:0000:0001
fw_ver=40.47.1026
hca_type=MT4131
board_id=NVD0000000054
hw_rev=0x0
devpath=/sys/devices/pci0000:00/0000:00:00.0/0000:01:00.0/0000:02:00.0/0000:03:00.0
files=board_id,device,fw_ver,hca_type,node_guid,ports
-- port 1
state=4: ACTIVE
phys_state=5: LinkUp
rate=800 Gb/sec (2X XDR)
lid=0x12
sm_lid=0x1
sm_sl=0
lid_mask_count=0
link_layer=InfiniBand
cap_mask=0xa751e848
pkeys=0x7fff,0xffff
counters=port_rcv_data,port_xmit_data
hw_counters=out_of_buffer
files=counters,gids,lid,state
gid[0]=fe80:0000:0000:0000:0000:0000:0000:0001 type=IB/RoCE v1 ndev=cat: read error: Invalid argument
== smi0
node_guid=0000:0000:0000:0001
hw_rev=cat: can't open '/sys/class/infiniband/smi0/hw_rev': No such file or directory
devpath=/sys/devices/pci0000:00/0000:00:00.0/0000:01:00.0/0000:02:00.0/0000:03:00.0
-- port 1
lid=0x12
link_layer=InfiniBand
`

const ibPCISysfs = `== 0000:02:00.0
vendor=0x15b3
device=0x197b
class=0x060400
devpath=/sys/devices/pci0000:00/0000:00:00.0/0000:01:00.0/0000:02:00.0
== 0000:03:00.0
vendor=0x15b3
device=0x1023
class=0x020700
numa_node=0
current_link_speed=32.0 GT/s PCIe
driver=mlx5_core
devpath=/sys/devices/pci0000:00/0000:00:00.0/0000:01:00.0/0000:02:00.0/0000:03:00.0
upstream=0x15b3:0x197b,0x15b3:0x197b
virtfns=0
iommu_group=17
hwmon=1
ptp=0
aux=eth,ib
irqs=128
files=class,device,vendor
`

func TestDevicesAreKeyedByTheirFunctionNotTheirName(t *testing.T) {
	t.Parallel()
	n := read(t, map[string]string{
		pciNICSysfs: ibPCISysfs,
		rdmaSysfs:   ibRDMASysfs,
		ibstat:      "CA 'mlx5_7'\n\tCA type: MT4131\n\tNode GUID: 0x0000000000000001\n\tPort 1:\n\t\tState: Active\n\t\tBase lid: 18\n\t\tRate: 800\n",
		ibvDevinfo:  "hca_id:\tmlx5_7\n\ttransport:\t\t\tInfiniBand (0)\n\tnode_guid:\t\t\t0000:0000:0000:0001\n\t\tport:\t1\n\t\t\tstate:\t\t\tPORT_ACTIVE (4)\n\t\t\tGID[  0]:\t\tfe80:0000:0000:0000:0000:0000:0000:0001\n",
		rdmaLink:    "link mlx5_7/1 subnet_prefix fe80:0000:0000:0000 lid 18 sm_lid 1 lmc 0 state ACTIVE physical_state LINK_UP\nlink smi0/1 state ACTIVE physical_state LINK_UP\n",
	})

	require.Equal(t, "mlx5_7", n.Records["rdma/0000:03:00.0/dev"]["name"])
	require.Equal(t, "yes", n.Records["rdma/0000:03:00.0/dev"]["pci_function"])
	require.Equal(t, "smi0", n.Records["rdma/0000:03:00.0/smi"]["name"])
	require.Equal(t, "1", n.Records["rdma/0000:03:00.0/dev"]["ports"])
	require.Equal(t, "Active", n.Records["ibstat/0000:03:00.0/dev"]["port.1.State"])
	require.NotContains(t, n.Records["ibstat/0000:03:00.0/dev"], "Node_GUID")
	require.NotContains(t, n.Records["ibstat/0000:03:00.0/dev"], "port.1.Base_lid")
	require.Equal(t, "PORT_ACTIVE (4)", n.Records["ibv/0000:03:00.0/dev"]["port.1.state"])
	require.Equal(t, "InfiniBand (0)", n.Records["ibv/0000:03:00.0/dev"]["transport"])
	require.NotContains(t, n.Records["ibv/0000:03:00.0/dev"], "node_guid")
	require.NotContains(t, n.Records["ibv/0000:03:00.0/dev"], "port.1.GID[  0]")
	require.Equal(t, "ACTIVE", n.Records["rdmalink/0000:03:00.0/dev/1"]["state"])
	require.Equal(t, "no", n.Records["rdmalink/0000:03:00.0/dev/1"]["netdev"])
	require.Contains(t, n.Records, "rdmalink/0000:03:00.0/smi/1")
	for o := range n.Records {
		require.NotContains(t, o, "mlx5_7", "no object keeps the device name")
	}
	require.Equal(t, rdmaSysfs, n.Source("rdma/0000:03:00.0/dev", "node_guid"))
	require.Equal(t, ibstat, n.Source("ibstat/0000:03:00.0/dev", "port.1.State"))
	require.Equal(t, ibstat, n.Source("ibstat/0000:03:00.0/dev", ""))
}

func TestADeviceOnNoPCIFunctionIsUnbound(t *testing.T) {
	t.Parallel()
	n := read(t, map[string]string{rdmaSysfs: "== mlx5_0\nnode_guid=0000:0000:0000:0002\ndevpath=/sys/devices/virtual/infiniband/mlx5_0\n-- port 1\nlink_layer=InfiniBand\n"})
	require.Equal(t, "no", n.Records["rdma/unbound/mlx5_0/dev"]["pci_function"])
	require.Contains(t, n.Records, "port/unbound/mlx5_0/dev/1")
}

func TestSysfsReadErrorsAndMissingFiles(t *testing.T) {
	t.Parallel()
	n := read(t, map[string]string{pciNICSysfs: ibPCISysfs, rdmaSysfs: ibRDMASysfs})
	smi := n.Records["rdma/0000:03:00.0/smi"]
	require.NotContains(t, smi, "hw_rev", "a file that does not exist has no record")
	port := n.Records["port/0000:03:00.0/dev/1"]
	require.Equal(t, "0", port["gid_ndevs"], "a GID whose ndev cannot be read has none")
	require.Equal(t, "800 Gb/sec (2X XDR)", port["rate"])
	require.Equal(t, "0x7fff,0xffff", port["pkeys"])
	require.Equal(t, "fe80:0000:0000:0000:0000:0000:0000:0001", port["gids"])
	require.Equal(t, "IB/RoCE v1", port["gid_types"])
}

func TestPCIFunctionsBridgesAndGPUs(t *testing.T) {
	t.Parallel()
	n := read(t, map[string]string{
		pciNICSysfs: ibPCISysfs,
		pciVPD:      "== 0000:03:00.0\nProduct Name: ConnectX-8 C8280Z\n[PN] Part number: 900-9X86E-00CX-ST0\n[V3] Vendor specific: 00000000000000000000000000000001\n",
		pciAll:      "0000:03:00.0 vendor=0x15b3 device=0x1023 class=0x020700 numa=0 driver=mlx5_core root=pci0000:00\n0008:01:00.0 vendor=0x10de device=0x2941 class=0x030200 numa=1 driver=nvidia root=pci0008:00\n",
		lspciNIC: "0000:03:00.0 Infiniband controller [0207]: Mellanox Technologies Device [15b3:1023]\n" +
			"\tCapabilities: [60] Express (v2) Endpoint, MSI 00\n" +
			"\t\tLnkCap:\tPort #0, Speed 32GT/s, Width x16, ASPM not supported\n" +
			"\t\tLnkSta:\tSpeed 32GT/s, Width x16\n" +
			"\t\tLnkCap2: Supported Link Speeds: 2.5-32GT/s, Crosslink- Retimer+ 2Retimers+ DRS-\n" +
			"\tCapabilities: [1c0 v1] Device Serial Number 00-00-00-00-00-00-00-01\n" +
			"\tCapabilities: [48] Vital Product Data\n" +
			"\t\tProduct Name: ConnectX-8 C8280Z\n\t\tRead-only fields:\n\t\t\t[PN] Part number: 900-9X86E-00CX-ST0\n" +
			"\tKernel driver in use: mlx5_core\n" +
			"0000:02:00.0 PCI bridge [0604]: Mellanox Technologies Device [15b3:197b]\n" +
			"\tCapabilities: [60] Express (v2) Downstream Port (Slot-), MSI 00\n",
	})

	require.Equal(t, map[string]string{"device": "0x197b", "root": "pci0000:00"}, n.Records["bridge/0000:02:00.0"])
	pci := n.Records["pci/0000:03:00.0"]
	require.Equal(t, "pci0000:00", pci["root"])
	require.Equal(t, "yes", pci["iommu_group"])
	require.Equal(t, "0", pci["virtfn_count"])
	require.NotContains(t, pci, "physfn")
	require.Equal(t, "ConnectX-8 C8280Z", pci["vpd.name"])
	require.Equal(t, "900-9X86E-00CX-ST0", pci["vpd.PN"])
	require.Equal(t, "00000000000000000000000000000001", pci["vpd.V3"])
	require.Equal(t, "32GT/s x16", pci["lspci.LnkCap"])
	require.Equal(t, "32GT/s x16", pci["lspci.LnkSta"])
	require.Equal(t, "mlx5_core", pci["lspci.driver"])
	require.Equal(t, "Device Serial Number,Express (v2) Endpoint,Vital Product Data", pci["lspci.capabilities"])
	require.Equal(t, map[string]string{"device": "0x2941", "numa_node": "1", "root": "pci0008:00"}, n.Records["gpu/0008:01:00.0"])
	require.NotContains(t, n.Records, "gpu/0000:03:00.0")
}

func TestRelationsInsideValues(t *testing.T) {
	t.Parallel()
	n := read(t, map[string]string{
		pciNICSysfs: ibPCISysfs + "== 0000:03:00.1\nvendor=0x15b3\nclass=0x020000\ndevpath=/sys/devices/pci0000:00/0000:03:00.1\n",
		rdmaSysfs: ibRDMASysfs + "== mlx5_8\nnode_guid=0000:0000:0000:0003\ndevpath=/sys/devices/pci0000:00/0000:03:00.1\n-- port 1\nlink_layer=Ethernet\n" +
			"gid[0]=fe80:0000:0000:0000:0000:00ff:fe00:0001 type=IB/RoCE v1 ndev=eth9\n" +
			"gid[1]=fe80:0000:0000:0000:0000:00ff:fe00:0001 type=RoCE v2 ndev=eth9\n",
		netSysfs: "== eth9\naddress=02:00:00:00:00:01\ntype=1\ndev_port=0\ndevice=/sys/devices/pci0000:00/0000:03:00.1\n" +
			"== ibp3s0\naddress=00:00:10:87:fe:80:00:00:00:00:00:00:00:00:00:00:00:00:00:01\ntype=32\ndev_port=0\ndevice=/sys/devices/pci0000:00/0000:03:00.0\n" +
			"== ibp3s0.8001\naddress=00:00:10:89:fe:80:00:00:00:00:00:00:00:00:00:00:00:00:00:09\ntype=32\ndev_port=0\npkey=0x8001\ndevice=/sys/devices/pci0000:00/0000:03:00.0\n" +
			"== veth1\naddress=aa:bb:cc:dd:ee:ff\ndevice=cat: can't open\n",
	})

	require.Equal(t, "yes", n.Records["port/0000:03:00.0/dev/1"]["gid0_is_node_guid"])
	require.NotContains(t, n.Records["port/0000:03:00.0/dev/1"], "link_local_gid_from_mac")
	require.Equal(t, "yes", n.Records["port/0000:03:00.1/dev/1"]["link_local_gid_from_mac"])
	require.Equal(t, "2", n.Records["port/0000:03:00.1/dev/1"]["gid_ndevs"])
	require.NotContains(t, n.Records["port/0000:03:00.1/dev/1"], "gid0_is_node_guid")

	require.Equal(t, "eth9", n.Records["net/0000:03:00.1/port0"]["name"])
	require.NotContains(t, n.Records["net/0000:03:00.1/port0"], "phys_port_name", "no port name: keyed by device port")
	require.Equal(t, "yes", n.Records["net/0000:03:00.0/port0"]["address_has_node_guid"])
	require.Equal(t, "no", n.Records["net/0000:03:00.0/port0/pkey0x8001"]["address_has_node_guid"])
	require.Equal(t, "ibp3s0.8001", n.Records["net/0000:03:00.0/port0/pkey0x8001"]["name"])
	for o := range n.Records {
		require.NotContains(t, o, "veth1", "only interfaces on a NIC function")
	}

	// A partition's child interface keeps its key under another name.
	renamed := read(t, map[string]string{
		pciNICSysfs: ibPCISysfs,
		netSysfs:    "== ib-storage\naddress=00:00:10:89:fe:80:00:00:00:00:00:00:00:00:00:00:00:00:00:09\ntype=32\ndev_port=0\npkey=0x8001\ndevice=/sys/devices/pci0000:00/0000:03:00.0\n",
	})
	require.Equal(t, "ib-storage", renamed.Records["net/0000:03:00.0/port0/pkey0x8001"]["name"])
}

func TestDeviceFilesAndClasses(t *testing.T) {
	t.Parallel()
	n := read(t, map[string]string{
		pciNICSysfs: ibPCISysfs,
		rdmaSysfs:   ibRDMASysfs,
		rdmaChar: "== /dev/infiniband\nby-ibdev type=dir\nby-ibdev/uverbs-mlx5_7 type=link target=../uverbs7\nby-path type=dir\n" +
			"by-path/pci-0000:03:00.0-uverbs type=link target=../uverbs7\n" +
			"rdma_cm type=char dev=a:7a mode=666\numad7 type=char dev=e7:7 mode=600\nuverbs7 type=char dev=e7:c7 mode=666\nuverbs9 type=file mode=644\n" +
			"== /sys/class/infiniband_verbs\nabi_version=6\n" +
			"uverbs7 ibdev=mlx5_7 dev=231:199 abi_version=1 backlink=231:199\nuverbs9 ibdev=mlx5_9 dev=231:201 abi_version=1 backlink=none\n" +
			"== /sys/class/infiniband_mad\nabi_version=5\numad7 ibdev=mlx5_7 dev=231:7 port=1 backlink=231:7\n",
	})

	require.Equal(t, "by-ibdev,by-path,rdma_cm,umad7,uverbs7,uverbs9", n.Records["node"]["dev_infiniband"])
	require.Equal(t, "6", n.Records["node"]["verbs_abi_version"])
	require.Equal(t, "5", n.Records["node"]["mad_abi_version"])
	require.Equal(t, "10:122 666", n.Records["devnode/rdma_cm"]["devnode"])
	verbs := n.Records["uverbs/0000:03:00.0/dev"]
	require.Equal(t, "231:199 666", verbs["devnode"])
	require.Equal(t, "yes", verbs["function_backlink"])
	require.Equal(t, "yes", verbs["by_ibdev"])
	require.Equal(t, "yes", verbs["by_path"])
	unbound := n.Records["uverbs/unbound/mlx5_9/dev"]
	require.Equal(t, "file", unbound["devnode"])
	require.Equal(t, "missing", unbound["function_backlink"])
	require.Equal(t, "no", unbound["by_path"])
	require.Equal(t, map[string]string{"name": "umad7", "dev": "231:7", "devnode": "231:7 600"}, n.Records["mad/0000:03:00.0/dev/1/umad"])
}

func TestToolOutputs(t *testing.T) {
	t.Parallel()
	esc := "\x1b[4m"
	n := read(t, map[string]string{
		pciNICSysfs: ibPCISysfs,
		rdmaSysfs:   ibRDMASysfs,
		netSysfs:    "== eth9\naddress=02:00:00:00:00:01\ntype=1\ndev_port=0\nphys_port_name=p0\ndevice=/sys/devices/pci0000:00/0000:03:00.0\n",
		sysModules:  "== mlx5_core\ninitstate=live\nversion=25.07\nparameters/prof_sel=2\n== ib_core\nparameters/netns_mode=Y\n",
		netECNQoS:   "== eth9\necn/roce_np/enable/0=1\necn/roce_np/enable/1=0\necn/roce_rp/enable/0=1\nqos/trust=dscp\nqos/pfc/x=1\n",
		netEthtoolI: "== eth9\ndriver: mlx5_core\nversion: 25.07\nfirmware-version: 40.47.1026 (NVD0000000054)\nbus-info: 0000:03:00.0\n== lo\ndriver: loopback\n",
		netEthtool: "== eth9\nSettings for eth9:\n\tSupported ports: [ Backplane ]\n\tSupported link modes:   400000baseCR4/Full\n\t                        800000baseCR8/Full\n" +
			"\tSupported pause frame use: Symmetric\n\tSpeed: 800000Mb/s\n\tLanes: 8\n\tLink detected: yes\n",
		netEthtoolM:   "== eth9\n\tIdentifier                                : 0x19 (OSFP)\n\tVendor SN                                 : 1650000000004\n\tVendor PN                                 : MCP4Y10-N001\n",
		devlinkInfo:   "pci/0000:03:00.0:\n  driver mlx5_core\n  serial_number 1650000000001\n  versions:\n      fixed:\n        fw.psid NVD0000000054\n      running:\n        fw.version 40.47.1026\n",
		devlinkPort:   "pci/0000:03:00.0/65535: type eth netdev eth9 flavour physical port 0 splittable false\n",
		devlinkESW:    "== pci/0000:03:00.0\npci/0000:03:00.0: mode legacy inline-mode none encap-mode basic\n",
		devlinkHealth: "pci/0000:03:00.0:\n  reporter fw\n    state healthy error 0\n  reporter fw_fatal\n    state healthy error 0\n",
		fwMLXConfig: "== 0000:03:00.0\nDevice type:        ConnectX8\nDescription:        NVIDIA ConnectX-8\n\nConfigurations:     Default     Current     Next Boot\n" +
			"*       LINK_TYPE_P1      IB(1)      ETH(2)      ETH(2)\n        NUM_OF_PLANES_P1  0          4           4\n        OTHER_PARAM  1  1  1\n" +
			"Configurations:     Next Boot\n        MODULE_SPLIT_M0[1]   2\n        MODULE_SPLIT_M0[0]   1\n",
		fwFlint:      "== 0000:03:00.0\nImage type:            FS5\nFW Version:            40.47.1026\nBase GUID:             0000000000000001        4\nPSID:                  NVD0000000054\n",
		cableMLXLink: "== 0000:03:00.0\nSpeed                              : IB-XDR\nWidth                              : 2x\nCable Type                         : Passive copper cable\nBias Current [mA]                  : 0,0,0,0\n",
		smiTopo: "\t" + esc + "GPU0\tGPU1\tNIC0\tCPU Affinity\tNUMA Affinity\tGPU NUMA ID\x1b[0m\n" +
			"GPU0\t X \tNV18\tNODE\t0-71\t0\t\tN/A\nNIC0\tNODE\tSYS\t X \t\t\t\t\n\nNIC Legend:\n\n  NIC0: mlx5_7\n",
	})

	require.Equal(t, map[string]string{"loaded": "yes", "version": "25.07", "param.prof_sel": "2"}, n.Records["module/mlx5_core"])
	require.Equal(t, map[string]string{"loaded": "builtin", "param.netns_mode": "Y"}, n.Records["module/ib_core"])
	net := n.Records["net/0000:03:00.0/p0"]
	require.Equal(t, "roce_np,roce_rp", net["ecn"])
	require.Equal(t, "1,0", net["ecn.roce_np.enable"])
	require.Equal(t, "pfc,trust", net["qos"])
	require.Equal(t, "dscp", net["qos.trust"])
	require.Equal(t, "40.47.1026 (NVD0000000054)", net["ethtool.firmware-version"])
	require.Equal(t, "400000baseCR4/Full,800000baseCR8/Full", net["ethtool.supported_link_modes"])
	require.Equal(t, "800000Mb/s", net["ethtool.speed"])
	require.Equal(t, "yes", net["ethtool.link_detected"])
	require.Equal(t, "0x19 (OSFP)", net["ethtool.module.identifier"])
	require.NotContains(t, net, "ethtool.module.vendor_sn")
	dl := n.Records["devlink/0000:03:00.0"]
	require.Equal(t, "yes", dl["present"])
	require.Equal(t, "mlx5_core", dl["info.driver"])
	require.Equal(t, "40.47.1026", dl["info.running.fw.version"])
	require.NotContains(t, dl, "info.serial_number")
	require.Equal(t, "1", dl["ports.physical"])
	require.Equal(t, "legacy", dl["eswitch.mode"])
	require.Equal(t, "fw,fw_fatal", dl["health.reporters"])
	fw := n.Records["fw/0000:03:00.0"]
	require.Equal(t, "ETH(2)", fw["LINK_TYPE_P1"], "the current value")
	require.Equal(t, "4", fw["NUM_OF_PLANES_P1"])
	require.NotContains(t, fw, "OTHER_PARAM")
	require.Equal(t, "1,2", fw["MODULE_SPLIT_M0"])
	require.Equal(t, "ConnectX8", fw["device_type"])
	require.Equal(t, "FS5", fw["flint.image_type"])
	require.NotContains(t, fw, "flint.base_guid")
	cable := n.Records["cable/0000:03:00.0"]
	require.Equal(t, "IB-XDR", cable["Speed"])
	require.Equal(t, "4", cable["lanes"])
	require.Equal(t, map[string]string{"GPU0": "NODE", "GPU1": "SYS"}, n.Records["topo/0000:03:00.0/dev"])
}

func TestMissingSourcesAreBlindSpots(t *testing.T) {
	t.Parallel()
	dir := writeNode(t, map[string]string{
		rdmaSysfs:   ibRDMASysfs,
		devlinkInfo: "",
		devlinkPort: "",
		ibvDevinfo:  "",
		fwMLXConfig: "",
		netEthtoolI: "",
		ibstat:      "",
		"errors.log": "time=\"…\" level=error msg=\"exec failed: unable to start container process: exec: \\\"devlink\\\": executable file not found in $PATH\"\n" +
			"command terminated with exit code 255\nFAILED: devlink-dev-info.txt\n" +
			"Unable to connect to the server: net/http: TLS handshake timeout\nFAILED: devlink-port.txt\n" +
			"Unable to connect to the server: net/http: TLS handshake timeout\n" +
			"exec failed: unable to start container process: exec: \"ibstat\": executable file not found in $PATH\nFAILED: host-ibstat.txt\n" +
			"--- rdma-sysfs.txt, try 1\nUnable to connect to the server: net/http: TLS handshake timeout\n" +
			"--- host-ibv-devinfo.txt, try 1\nFailed to open device\ncommand terminated with exit code 1\nFAILED: host-ibv-devinfo.txt\n" +
			"none of mlxconfig mstconfig found\ncommand terminated with exit code 127\nFAILED: fw-mlxconfig.txt\n" +
			"command terminated with exit code 127\nFAILED: net-ethtool-i.txt\n",
	})
	n, err := Read(dir)
	require.NoError(t, err)
	require.Equal(t, "the capturing pod lacks the tool", n.Missing[devlinkInfo])
	require.Equal(t, "the connection to the cluster dropped", n.Missing[devlinkPort])
	require.Equal(t, "the capturing pod lacks the tool", n.Missing[ibstat], "a command run again is judged by its last try")
	require.Equal(t, "the capturing pod lacks the tool", n.Missing[fwMLXConfig])
	require.Equal(t, "the capturing pod lacks the tool", n.Missing[netEthtoolI], "the shell's exit code for a command it cannot find")
	require.NotContains(t, n.Missing, ibvDevinfo, "a tool that ran and failed shows the surface is not there, "+
		"whatever an earlier command's recovered try printed")
	require.Equal(t, "no such file", n.Missing[fwFlint])
	require.NotContains(t, n.Missing, rdmaSysfs)

	_, err = Read(t.TempDir())
	require.ErrorContains(t, err, "no NIC surface files")
}

func TestCommandPrintsRecordsWithTheirSource(t *testing.T) {
	t.Parallel()
	dir := writeNode(t, map[string]string{rdmaSysfs: "== mlx5_0\nnode_guid=0000:0000:0000:0002\ndevpath=/sys/devices/virtual/infiniband/mlx5_0\n"})
	var out, errs bytes.Buffer
	root := &cli.Command{Name: "mokka-capture", Commands: []*cli.Command{Command()}}
	require.Equal(t, 0, cmdline.Run(context.Background(), root, []string{"nic-surfaces", dir}, &out, &errs), errs.String())
	lines := strings.Split(out.String(), "\n")
	require.True(t, strings.HasPrefix(lines[0], "# not captured: pci-nic-sysfs.txt (no such file), "))
	require.Contains(t, out.String(), "rdma/unbound/mlx5_0/dev\tnode_guid\t0000:0000:0000:0002\trdma-sysfs.txt\n")
	require.Equal(t, 1, cmdline.Run(context.Background(), root, []string{"nic-surfaces", t.TempDir()}, &out, &errs))
}
