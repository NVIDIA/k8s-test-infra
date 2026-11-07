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
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (c *collector) captureDriver(node string) {
	c.enter(node)
	c.driverPod = c.podOn(node, "nvidia-driver-daemonset")
	if c.driverPod == "" {
		c.logf("no driver pod on %s; skipping", node)
		return
	}
	c.logf("== %s: driver container (%s)", node, c.driverPod)

	for _, q := range []struct{ file, args string }{
		{"smi-q.xml", "-q -x"},
		{"smi-q.txt", "-q"},
		{"smi-L.txt", "-L"},
		{"smi-version.txt", "--version"},
		{"smi-help.txt", "-h"},
		{"smi-topo-m.txt", "topo -m"},
		{"smi-topo-p2p-n.txt", "topo -p2p n"},
		{"smi-topo-p2p-r.txt", "topo -p2p r"},
		{"smi-nvlink-s.txt", "nvlink -s"},
		{"smi-nvlink-c.txt", "nvlink -c"},
		{"smi-nvlink-e.txt", "nvlink -e"},
		{"smi-mig-lgip.txt", "mig -lgip"},
		{"smi-mig-lgipp.txt", "mig -lgipp"},
		{"smi-mig-lcip.txt", "mig -lcip"},
	} {
		c.capture(q.file, nil, c.drv(append([]string{"nvidia-smi"}, strings.Fields(q.args)...)...)...)
	}
	for _, section := range []string{"CLOCK", "POWER", "SUPPORTED_CLOCKS", "PERFORMANCE", "ECC", "ROW_REMAPPER", "FABRIC"} {
		c.capture("smi-q-d-"+section+".txt", nil, c.drv("nvidia-smi", "-q", "-d", section)...)
	}

	// One line per --query-gpu field, so an unsupported field costs one line and
	// not the whole query.
	c.capture("smi-help-query-gpu.txt", nil, c.drv("nvidia-smi", "--help-query-gpu")...)
	c.capture("smi-query-fields.txt", nil, c.drv(sh(`for f in $(nvidia-smi --help-query-gpu | sed -n "s/^\"\([^\"]*\)\".*/\1/p"); do printf "%s: " "$f"; nvidia-smi --query-gpu=$f --format=csv,noheader 2>&1 | paste -sd, -; done`)...)...)

	for _, f := range []struct{ file, script string }{
		{"proc-nvidia-files.txt", `find /proc/driver/nvidia /proc/driver/nvidia-caps -type f 2>/dev/null | sort`},
		{"proc-gpu-information.txt", `for f in /proc/driver/nvidia/gpus/*/information; do echo "== $f"; cat "$f"; done`},
		{"proc-gpu-power.txt", `for f in /proc/driver/nvidia/gpus/*/power; do echo "== $f"; cat "$f"; done`},
		{"proc-gpu-registry.txt", `for f in /proc/driver/nvidia/gpus/*/registry; do echo "== $f"; cat "$f"; done`},
		{"proc-capabilities.txt", `for f in $(find /proc/driver/nvidia/capabilities -type f | sort); do echo "== $f"; cat "$f"; done`},
		{"lib-listing.txt", `ls -l /usr/lib/*/libnvidia-*.so* /usr/lib/*/libcuda*.so* /usr/lib64/libnvidia-*.so* 2>/dev/null; true`},
		{"firmware-listing.txt", `ls -R /lib/firmware/nvidia 2>/dev/null | head -100`},
		{"bin-listing.txt", `ls /usr/bin | grep -i nvidia`},
		{"modinfo-nvidia.txt", `modinfo nvidia | head -30`},
		{"nvml-symbols.txt", `nm -D --defined-only $(ls /usr/lib/*/libnvidia-ml.so.1 /usr/lib64/libnvidia-ml.so.1 2>/dev/null | head -1) | awk "{print \$3}" | sort`},
	} {
		c.capture(f.file, nil, c.drv(sh(f.script)...)...)
	}

	for _, f := range []struct{ file, path string }{
		{"proc-nvidia-version.txt", "/proc/driver/nvidia/version"},
		{"proc-nvidia-params.txt", "/proc/driver/nvidia/params"},
		{"proc-registry.txt", "/proc/driver/nvidia/registry"},
		{"proc-suspend-depth.txt", "/proc/driver/nvidia/suspend_depth"},
		{"proc-mig-minors.txt", "/proc/driver/nvidia-caps/mig-minors"},
		{"proc-nvlink-minors.txt", "/proc/driver/nvidia-caps/nvlink-minors"},
		{"proc-sys-minors.txt", "/proc/driver/nvidia-caps/sys-minors"},
		{"sys-module-version.txt", "/sys/module/nvidia/version"},
	} {
		c.capture(f.file, nil, c.drv("cat", f.path)...)
	}

	c.captureNVMLDump()
}

// captureNVMLDump copies a Linux mokka-capture into the driver container, which
// holds the real NVML library, runs its dump there, and removes it again.
func (c *collector) captureNVMLDump() {
	if c.cfg.dumpBinary == "" {
		c.logf("-dump-binary not set; skipping the NVML value dump (see build-dump)")
		return
	}
	bin, err := os.Open(c.cfg.dumpBinary)
	if err != nil {
		c.logf("-dump-binary: %v; skipping the NVML value dump", err)
		return
	}
	defer func() { _ = bin.Close() }()

	// One path per run, so two captures of the same node never replace or
	// remove each other's binary.
	path := "/tmp/mokka-capture-" + c.suffix
	errs := c.errorsLog()
	defer func() { _ = errs.Close() }()
	if err := c.exec(bin, io.Discard, errs, c.drvStdin("sh", "-c", `cat > "$1" && chmod +x "$1"`, "sh", path)...); err != nil {
		_, _ = fmt.Fprintln(errs, "FAILED: nvml-dump.json (copy into the driver container)")
		c.logf("FAILED: nvml-dump.json (copy into the driver container)")
		return
	}
	c.capture("nvml-dump.json", nil, c.drv(path, "dump")...)
	// Leave nothing behind in the driver container.
	_ = c.exec(nil, io.Discard, io.Discard, c.drv("rm", "-f", path)...)
}

// drvStdin is drv with standard input attached.
func (c *collector) drvStdin(argv ...string) []string {
	return append([]string{"kubectl", "-n", c.cfg.operatorNS, "exec", "-i", c.driverPod, "-c", c.cfg.driverCtr, "--"}, argv...)
}

const hostPodManifest = `apiVersion: v1
kind: Pod
metadata:
  name: %s
spec:
  nodeName: %s
  hostPID: true
  hostNetwork: true
  restartPolicy: Never
  tolerations: [{operator: Exists}]
  containers:
  - name: c
    image: %s
    command: ["sleep", "3600"]
    securityContext: {privileged: true}
    volumeMounts: [{name: host, mountPath: /host}]
  volumes: [{name: host, hostPath: {path: /}}]
`

// startHostPod starts a privileged pod on node that sees the node's /sys, /dev
// and /proc. Its tools come from apt; if the cluster has no egress the sysfs
// captures still work and the rest fail into errors.log.
func (c *collector) startHostPod(node string) bool {
	c.hostPod = hostPodName(node, c.suffix)
	manifest := fmt.Sprintf(hostPodManifest, c.hostPod, node, c.cfg.hostPodImage)
	errs := c.errorsLog()
	defer func() { _ = errs.Close() }()
	if err := c.exec(strings.NewReader(manifest), io.Discard, errs, "kubectl", "-n", c.cfg.hostPodNS, "apply", "-f", "-"); err != nil {
		c.logf("could not create the host pod on %s", node)
		return false
	}
	if err := c.exec(nil, io.Discard, errs, "kubectl", "-n", c.cfg.hostPodNS, "wait", "--for=condition=Ready", "pod/"+c.hostPod, "--timeout=180s"); err != nil {
		c.logf("host pod did not become ready on %s", node)
		return false
	}
	install := `apt-get update -qq && apt-get install -y -qq pciutils numactl rdma-core ibverbs-utils infiniband-diags iproute2 ethtool >/dev/null`
	if err := c.exec(nil, io.Discard, errs, c.host(sh(install)...)...); err != nil {
		c.logf("tool install failed in the host pod; some captures will fail")
	}
	return true
}

// stopHostPod deletes the host pod. The deletion must still run after an
// interrupt has cancelled ctx, so it keeps ctx's values but not its
// cancellation, and has a timeout of its own.
func (c *collector) stopHostPod(ctx context.Context) {
	if c.hostPod == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.kubectl, "-n", c.cfg.hostPodNS, "delete", "pod", c.hostPod, "--wait=false")
	_ = cmd.Run()
	c.hostPod = ""
}

func (c *collector) captureHost(node string) {
	c.enter(node)
	c.logf("== %s: host pod", node)
	defer c.stopHostPod(c.ctx)
	if !c.startHostPod(node) {
		return
	}

	c.capture("host-uname.txt", nil, c.host("uname", "-a")...)
	c.capture("host-os-release.txt", nil, c.host("cat", "/host/etc/os-release")...)
	c.capture("host-pagesize.txt", nil, c.host("getconf", "PAGESIZE")...)
	c.capture("host-lscpu.txt", nil, c.host("lscpu")...)
	c.capture("host-numactl.txt", nil, c.host("numactl", "-H")...)
	c.capture("host-lspci-nvidia.txt", nil, c.host("lspci", "-nnvvv", "-d", "10de:")...)
	c.capture("host-lspci-mellanox.txt", nil, c.host("lspci", "-nn", "-d", "15b3:")...)
	c.capture("host-ibstat.txt", nil, c.host("ibstat")...)
	c.capture("host-ibv-devinfo.txt", nil, c.host("ibv_devinfo", "-v")...)
	c.capture("host-rdma-link.txt", nil, c.host("rdma", "link", "show")...)
	for _, f := range []struct{ file, script string }{
		{"host-numa-cpulist.txt", `for n in /sys/devices/system/node/node*; do echo "$n: $(cat $n/cpulist)"; done`},
		{"host-dmi.txt", `for f in product_name sys_vendor board_name bios_version; do echo "$f=$(cat /sys/class/dmi/id/$f)"; done`},
		{"host-pci-nvidia.txt", `for d in /sys/bus/pci/devices/*; do [ "$(cat $d/vendor)" = 0x10de ] || continue; echo "$d device=$(cat $d/device) class=$(cat $d/class) subsys=$(cat $d/subsystem_vendor):$(cat $d/subsystem_device) numa=$(cat $d/numa_node) link=$(cat $d/current_link_speed)x$(cat $d/current_link_width) max=$(cat $d/max_link_speed)x$(cat $d/max_link_width)"; done`},
		{"host-pci-hierarchy.txt", `readlink -f /sys/bus/pci/devices/*`},
		{"host-dev-nvidia.txt", `ls -l /dev/nvidia* /dev/nvidia-caps* /dev/nvidia-caps-imex-channels 2>&1; stat -c "%n %t:%T %a %U:%G" /dev/nvidia* 2>&1; true`},
		{"host-proc-devices.txt", `grep -i nvidia /proc/devices`},
		{"host-proc-modules.txt", `grep -E "^(nvidia|nvidia_uvm|nvidia_modeset|nvidia_peermem|mlx5_core|ib_core)" /proc/modules`},
		{"host-ib-sysfs.txt", `for h in /sys/class/infiniband/*; do echo "== $h"; for f in node_guid node_type fw_ver hca_type board_id; do echo "$f=$(cat $h/$f 2>&1)"; done; for p in $h/ports/*; do echo "port $(basename $p) state=$(cat $p/state) phys=$(cat $p/phys_state) rate=$(cat $p/rate) layer=$(cat $p/link_layer)"; done; done`},
		{"host-ib-dev.txt", `ls -l /dev/infiniband 2>&1`},
	} {
		c.capture(f.file, nil, c.host(sh(f.script)...)...)
	}

	c.captureNetworkSurfaces()

	// CDI specs the operator wrote. /var/run is a symlink to /run, which would
	// resolve inside the pod, so read /run through the host mount.
	c.capture("cdi-specs-host.txt", nil, c.host(sh(`ls -l /host/run/cdi /host/etc/cdi 2>&1; for f in /host/run/cdi/* /host/etc/cdi/*; do echo "== $f"; cat "$f"; done 2>/dev/null; true`)...)...)
	if toolkit := c.podOn(node, "container-toolkit"); toolkit != "" {
		c.capture("cdi-toolkit-list.txt", nil, "kubectl", "-n", c.cfg.operatorNS, "exec", toolkit, "-c", c.cfg.toolkitCtr, "--", c.cfg.toolkitCTK, "cdi", "list")
	}
}

const rdmaSysfsScript = `for h in /sys/class/infiniband/*; do
  echo "== $(basename $h)"
  for f in node_guid node_desc node_type sys_image_guid fw_ver hca_type board_id; do echo "$f=$(cat $h/$f 2>&1)"; done
  for f in vendor device class numa_node subsystem_vendor subsystem_device; do echo "device/$f=$(cat $h/device/$f 2>&1)"; done
  echo "devpath=$(readlink -f $h/device)"
  echo "driver=$(basename "$(readlink -f $h/device/driver 2>/dev/null)")"
  for p in $h/ports/*; do
    echo "-- port $(basename $p)"
    for f in state phys_state rate lid sm_lid sm_sl lmc link_layer cap_mask; do echo "$f=$(cat $p/$f 2>&1)"; done
    for g in $p/gids/*; do
      n=$(basename $g)
      t=$(cat $p/gid_attrs/types/$n 2>/dev/null) || continue
      echo "gid[$n]=$(cat $g 2>&1) type=$t ndev=$(cat $p/gid_attrs/ndevs/$n 2>&1)"
    done
  done
done
`

const rdmaCountersScript = `for h in /sys/class/infiniband/*; do
  p=$h/ports/1
  echo "== $(basename $h) port 1"
  for d in counters hw_counters; do
    for c in $p/$d/*; do [ -f "$c" ] && echo "$d/$(basename $c)=$(cat $c 2>&1)"; done
  done
done
`

// captureNetworkSurfaces runs inside the host pod: a privileged pod on the host
// network sees the node's own interfaces, /sys/class/net and RDMA devices.
func (c *collector) captureNetworkSurfaces() {
	hostScript := []string{"kubectl", "-n", c.cfg.hostPodNS, "exec", "-i", c.hostPod, "--", "sh", "-s"}
	c.capture("host-lspci-mellanox-vv.txt", nil, c.host("lspci", "-nnvvv", "-d", "15b3:")...)
	c.capture("net-ip-link.txt", nil, c.host("ip", "-d", "link", "show")...)
	c.capture("rdma-dev.txt", nil, c.host("rdma", "dev", "show")...)
	c.capture("rdma-link.txt", nil, c.host("rdma", "link", "show")...)
	c.capture("rdma-sysfs.txt", strings.NewReader(rdmaSysfsScript), hostScript...)
	c.capture("rdma-counters.txt", strings.NewReader(rdmaCountersScript), hostScript...)
	for _, f := range []struct{ file, script string }{
		{"pci-all-functions.txt", `for d in /sys/bus/pci/devices/*; do echo "$(basename $d) vendor=$(cat $d/vendor) device=$(cat $d/device) class=$(cat $d/class) numa=$(cat $d/numa_node) driver=$(basename $(readlink $d/driver 2>/dev/null) 2>/dev/null) root=$(readlink -f $d | cut -d/ -f4)"; done`},
		{"pci-nic-functions.txt", `for d in /sys/bus/pci/devices/*; do [ "$(cat $d/vendor)" = 0x15b3 ] || continue; echo "$(basename $d) subsys=$(cat $d/subsystem_vendor):$(cat $d/subsystem_device) sriov_totalvfs=$(cat $d/sriov_totalvfs 2>&1) sriov_numvfs=$(cat $d/sriov_numvfs 2>&1) physfn=$(readlink $d/physfn 2>&1) virtfn=$(ls -d $d/virtfn* 2>/dev/null | wc -l)"; done`},
		{"pci-vpd.txt", `for d in /sys/bus/pci/devices/*; do [ "$(cat $d/vendor)" = 0x15b3 ] || continue; echo "$(basename $d): $(tr -c "[:print:]" " " < $d/vpd 2>/dev/null | grep -oE "PN[^ ]* [^ ]*" | tr "\n" " ")"; done`},
		{"net-sysfs.txt", `for i in /sys/class/net/*; do echo "== $(basename $i)"; for f in address operstate carrier speed duplex mtu type dev_id dev_port; do echo "$f=$(cat $i/$f 2>&1)"; done; echo "device=$(readlink -f $i/device 2>&1)"; done`},
		{"net-ethtool-i.txt", `for i in /sys/class/net/*; do n=$(basename $i); echo "== $n"; ethtool -i $n 2>&1; done`},
		{"rdma-classes.txt", `for c in infiniband infiniband_verbs infiniband_mad infiniband_cm; do echo "== /sys/class/$c"; ls /sys/class/$c 2>&1; done; true`},
		{"sys-module-mlx5.txt", `for m in mlx5_core mlx5_ib ib_core ib_uverbs nvidia; do echo "$m version=$(cat /sys/module/$m/version 2>&1) parameters=$(ls /sys/module/$m/parameters 2>&1 | tr "\n" " ")"; done`},
		{"kernel-log-nic-gpu.txt", `dmesg 2>&1 | grep -i -E "mlx5|NVRM|nvidia" | tail -300`},
	} {
		c.capture(f.file, nil, c.host(sh(f.script)...)...)
	}
}

// captureNetwork takes only the network surfaces, for adding to an existing
// capture.
func (c *collector) captureNetwork(node string) {
	c.enter(node)
	c.logf("== %s: network surfaces (host pod)", node)
	defer c.stopHostPod(c.ctx)
	if !c.startHostPod(node) {
		return
	}
	c.captureNetworkSurfaces()
}

func (c *collector) captureClusterNetwork() {
	c.enter("cluster")
	crds, err := c.kubectlOut("get", "crd", "-o", "name")
	var matched []string
	for _, l := range strings.Split(strings.TrimSpace(crds), "\n") {
		if l != "" && crdNetworkRE.MatchString(l) {
			matched = append(matched, l)
		}
	}
	text := strings.Join(matched, "\n")
	if text != "" {
		text += "\n"
	}
	c.captureText("crds-network.txt", text, err)
	c.capture("nodefeatures.yaml", nil, "kubectl", "get", "nodefeatures", "-A", "-o", "yaml")
	c.capture("nicclusterpolicy.yaml", nil, "kubectl", "get", "nicclusterpolicies.mellanox.com", "-o", "yaml")
	c.capture("network-attachments.yaml", nil, "kubectl", "get", "network-attachment-definitions", "-A", "-o", "yaml")
	c.capture("sriov-node-state.yaml", nil, "kubectl", "get", "sriovnetworknodestates", "-A", "-o", "yaml")
}

func (c *collector) captureCluster() {
	c.enter("cluster")
	c.logf("== cluster")
	ns := c.cfg.operatorNS

	version, err := c.kubectlOut("version", "-o", "yaml")
	c.captureText("versions.txt", version+time.Now().UTC().Format(time.UnixDate)+"\n", err)
	c.capture("nodes-wide.txt", nil, "kubectl", "get", "nodes", "-o", "wide")
	c.capture("nodes.yaml", nil, "kubectl", "get", "nodes", "-o", "yaml")
	c.capture("gpu-labels.txt", nil, "kubectl", "get", "nodes", "-L", "nvidia.com/gpu.product,nvidia.com/gpu.machine,nvidia.com/gpu.family,nvidia.com/gpu.count")
	c.capture("resourceslices.yaml", nil, "kubectl", "get", "resourceslices", "-o", "yaml")
	c.capture("deviceclasses.yaml", nil, "kubectl", "get", "deviceclasses", "-o", "yaml")
	c.capture("computedomains.yaml", nil, "kubectl", "get", "computedomains", "-A", "-o", "yaml")
	c.capture("clusterpolicy.yaml", nil, "kubectl", "get", "clusterpolicy", "-o", "yaml")
	c.capture("operator-pods.txt", nil, "kubectl", "-n", ns, "get", "pods", "-o", "wide")
	c.capture("operator-daemonsets.yaml", nil, "kubectl", "-n", ns, "get", "daemonsets", "-o", "yaml")
	c.capture("operator-configmaps.yaml", nil, "kubectl", "-n", ns, "get", "configmaps", "-o", "yaml")
	c.capture("validator-logs.txt", nil, "kubectl", "-n", ns, "logs", "-l", "app=nvidia-operator-validator", "--all-containers", "--prefix", "--tail=-1")

	// DCGM metrics through the API server, one file per exporter pod.
	pods, err := c.kubectlOut("-n", ns, "get", "pods", "-l", "app=nvidia-dcgm-exporter", "-o", "name")
	if err != nil {
		c.logf("%v", err)
	}
	for _, p := range strings.Fields(pods) {
		pod := strings.TrimPrefix(p, "pod/")
		c.capture("dcgm-"+pod+".txt", nil, "kubectl", "get", "--raw", "/api/v1/namespaces/"+ns+"/pods/"+pod+":9400/proxy/metrics")
	}
}

// buildDump builds a Linux mokka-capture in a golang container. go-nvml loads
// the driver's library at run time, so the binary is dynamically linked and
// must be built for the node's architecture, on a host of that architecture.
func (c *collector) buildDump(stdout io.Writer) int {
	root, err := exec.CommandContext(c.ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		c.logf("error: run build-dump from inside the repository")
		return 1
	}
	repo := strings.TrimSpace(string(root))
	version, err := exec.CommandContext(c.ctx, filepath.Join(repo, "hack", "golang-version.sh")).Output()
	if err != nil {
		c.logf("error: hack/golang-version.sh: %v", err)
		return 1
	}
	bin, err := filepath.Abs(filepath.Join(c.cfg.out, "bin"))
	if err == nil {
		err = os.MkdirAll(bin, 0o755)
	}
	if err != nil {
		c.logf("error: %v", err)
		return 1
	}
	name := "mokka-capture-linux-" + runtime.GOARCH
	cmd := exec.CommandContext(c.ctx, "docker", "run", "--rm", "-v", repo+":/src", "-v", bin+":/out", "-w", "/src",
		"golang:"+strings.TrimSpace(string(version)), "go", "build", "-o", "/out/"+name, "./cmd/mokka-capture")
	cmd.Stdout, cmd.Stderr = c.stderr, c.stderr
	if err := cmd.Run(); err != nil {
		c.logf("error: build failed: %v", err)
		return 1
	}
	out := filepath.Join(bin, name)
	_, _ = fmt.Fprintf(stdout, "%s\n", out)
	c.logf("built %s; run collect with -dump-binary %s", out, out)
	return 0
}
