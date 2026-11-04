#!/usr/bin/env bash

# Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Capture everything about a real GPU node that Mokka has to reproduce: NVML and
# nvidia-smi, the driver's /proc files, PCI and device files, NUMA, InfiniBand
# and RoCE NICs, CDI specs, and the Kubernetes view of the node.
#
# Every command runs in the cluster; every redirect happens here, so each output
# is a file under $OUT and nothing has to be copied out of a pod. A command that
# fails does not stop the run: its error is appended to errors.log beside the
# output, and a failure is data (an unsupported query is an answer too).
#
# See docs/contributing/capturing-a-real-node.md for the full procedure.

set -u -o pipefail

usage() {
  cat <<'EOF'
Usage: hack/capture-node.sh <command> [node...]

Commands:
  driver <node>...   nvidia-smi, NVML dump and /proc/driver/nvidia, taken inside
                     the GPU Operator driver container on each node
  host <node>...     PCI, NUMA, device files, NICs and CDI specs, taken from a
                     temporary privileged pod on each node
  network <node>...  NICs, network interfaces, RDMA devices and ports, SR-IOV,
                     kernel modules and kernel log, plus the cluster's network
                     objects; a subset of host, for adding network surfaces to
                     an existing capture
  cluster            node labels, ResourceSlices, ClusterPolicy, DCGM metrics
  all <node>...      driver, host, then cluster
  build-dump         build the nvml-dump binary for the host's architecture
                     (DUMP_PLATFORM=linux/amd64 builds for another one)
  mock-dump <node>...  nvml-dump, nvidia-smi and the NVML symbols of the mock, taken
                     in the nvml-mock pod on each node of a Mokka cluster
  compare <real-dir> <mock-dir>
                     per-surface match between a real capture and one taken from
                     a mock node (host, driver or mock-dump output); needs no cluster

Environment:
  OUT                      output directory (default ./capture)
  KUBECONFIG               cluster to capture from (default kubectl's own)
  OPERATOR_NAMESPACE       GPU Operator namespace (default gpu-operator)
  DRIVER_CONTAINER         driver container name (default nvidia-driver-ctr)
  TOOLKIT_CONTAINER        toolkit container name (default nvidia-container-toolkit-ctr)
  TOOLKIT_CTK              path of nvidia-ctk in that container
                           (default /usr/local/nvidia/toolkit/nvidia-ctk)
  HOST_POD_NAMESPACE       namespace for the temporary host pod (default default)
  HOST_POD_IMAGE           image for the host pod; privileged, so pinned by digest
                           (default ubuntu:24.04@sha256:fe8156...; the multi-arch index)
  NVML_DUMP                linux binary of nvml-dump to run in the driver
                           container (or, for mock-dump, the nvml-mock pod);
                           without it the NVML value dump is skipped
  DUMP_PLATFORM            platform build-dump targets, for example linux/amd64
                           (default: the Docker host's)
  MOCK_NAMESPACE           namespace of the nvml-mock release (default mokka)
  MOCK_LIB_DIR             where the mock library sits in its pod
  HOST_POD_MOCK=1          set when `host` runs on a mock node: the pod then asks the
                           nvml-mock NRI plugin to inject the mock (devices and
                           infiniband annotations), or it would read the Kind host
                           (default /var/lib/nvml-mock/driver/usr/lib64)
EOF
}

OUT=${OUT:-./capture}
OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE:-gpu-operator}
DRIVER_CONTAINER=${DRIVER_CONTAINER:-nvidia-driver-ctr}
TOOLKIT_CONTAINER=${TOOLKIT_CONTAINER:-nvidia-container-toolkit-ctr}
TOOLKIT_CTK=${TOOLKIT_CTK:-/usr/local/nvidia/toolkit/nvidia-ctk}
HOST_POD_NAMESPACE=${HOST_POD_NAMESPACE:-default}
# The host pod is privileged and mounts the node's root, so its image is pinned by
# digest (the multi-arch index of ubuntu:24.04) rather than by a tag that can move.
HOST_POD_IMAGE=${HOST_POD_IMAGE:-ubuntu:24.04@sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852}
NVML_DUMP=${NVML_DUMP:-}
DUMP_PLATFORM=${DUMP_PLATFORM:-}
MOCK_NAMESPACE=${MOCK_NAMESPACE:-mokka}
MOCK_LIB_DIR=${MOCK_LIB_DIR:-/var/lib/nvml-mock/driver/usr/lib64}

CAP_DIR=""
DRIVER_POD=""
HOST_POD=""

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

# cap <file> <command...>: run the command, write its stdout to $CAP_DIR/<file>.
cap() {
  local file=$1
  shift
  if ! "$@" >"${CAP_DIR}/${file}" 2>>"${CAP_DIR}/errors.log"; then
    printf 'FAILED: %s\n' "${file}" | tee -a "${CAP_DIR}/errors.log" >&2
  fi
}

# drv <command...>: run inside the driver container of the current node.
drv() {
  kubectl -n "${OPERATOR_NAMESPACE}" exec "${DRIVER_POD}" -c "${DRIVER_CONTAINER}" -- "$@"
}

# host <command...>: run inside the temporary host pod of the current node.
host() {
  kubectl -n "${HOST_POD_NAMESPACE}" exec "${HOST_POD}" -- "$@"
}

# host_script: run a shell script read from stdin inside the host pod.
host_script() {
  kubectl -n "${HOST_POD_NAMESPACE}" exec -i "${HOST_POD}" -- sh -s
}

# A dropped API connection yields empty files that look like answers, so check
# before each node.
require_cluster() {
  kubectl get nodes >/dev/null 2>&1 || die "cannot reach the cluster; check KUBECONFIG and any tunnel"
}

node_dir() {
  CAP_DIR="${OUT}/$1"
  mkdir -p "${CAP_DIR}"
}

capture_driver() {
  local node=$1
  require_cluster
  node_dir "${node}"

  DRIVER_POD=$(kubectl -n "${OPERATOR_NAMESPACE}" get pods -o name \
    --field-selector "spec.nodeName=${node}" | grep nvidia-driver-daemonset | cut -d/ -f2 | head -1)
  [ -n "${DRIVER_POD}" ] || { log "no driver pod on ${node}; skipping"; return; }
  log "== ${node}: driver container (${DRIVER_POD})"

  cap smi-q.xml               drv nvidia-smi -q -x
  cap smi-q.txt               drv nvidia-smi -q
  cap smi-L.txt               drv nvidia-smi -L
  cap smi-version.txt         drv nvidia-smi --version
  cap smi-help.txt            drv nvidia-smi -h
  cap smi-topo-m.txt          drv nvidia-smi topo -m
  cap smi-topo-p2p-n.txt      drv nvidia-smi topo -p2p n
  cap smi-topo-p2p-r.txt      drv nvidia-smi topo -p2p r
  cap smi-nvlink-s.txt        drv nvidia-smi nvlink -s
  cap smi-nvlink-c.txt        drv nvidia-smi nvlink -c
  cap smi-nvlink-e.txt        drv nvidia-smi nvlink -e
  cap smi-mig-lgip.txt        drv nvidia-smi mig -lgip
  cap smi-mig-lgipp.txt       drv nvidia-smi mig -lgipp
  cap smi-mig-lcip.txt        drv nvidia-smi mig -lcip
  local section
  for section in CLOCK POWER SUPPORTED_CLOCKS PERFORMANCE ECC ROW_REMAPPER FABRIC; do
    cap "smi-q-d-${section}.txt" drv nvidia-smi -q -d "${section}"
  done

  # One line per --query-gpu field, so an unsupported field costs one line and
  # not the whole query.
  cap smi-help-query-gpu.txt  drv nvidia-smi --help-query-gpu
  cap smi-query-fields.txt    drv sh -c 'for f in $(nvidia-smi --help-query-gpu | sed -n "s/^\"\([^\"]*\)\".*/\1/p"); do printf "%s: " "$f"; nvidia-smi --query-gpu=$f --format=csv,noheader 2>&1 | paste -sd, -; done'

  cap proc-nvidia-files.txt   drv sh -c 'find /proc/driver/nvidia /proc/driver/nvidia-caps -type f 2>/dev/null | sort'
  cap proc-nvidia-version.txt drv cat /proc/driver/nvidia/version
  cap proc-nvidia-params.txt  drv cat /proc/driver/nvidia/params
  cap proc-registry.txt       drv cat /proc/driver/nvidia/registry
  cap proc-suspend-depth.txt  drv cat /proc/driver/nvidia/suspend_depth
  cap proc-gpu-information.txt drv sh -c 'for f in /proc/driver/nvidia/gpus/*/information; do echo "== $f"; cat "$f"; done'
  cap proc-gpu-power.txt      drv sh -c 'for f in /proc/driver/nvidia/gpus/*/power; do echo "== $f"; cat "$f"; done'
  cap proc-gpu-registry.txt   drv sh -c 'for f in /proc/driver/nvidia/gpus/*/registry; do echo "== $f"; cat "$f"; done'
  cap proc-capabilities.txt   drv sh -c 'for f in $(find /proc/driver/nvidia/capabilities -type f | sort); do echo "== $f"; cat "$f"; done'
  cap proc-mig-minors.txt     drv cat /proc/driver/nvidia-caps/mig-minors
  cap proc-nvlink-minors.txt  drv cat /proc/driver/nvidia-caps/nvlink-minors
  cap proc-sys-minors.txt     drv cat /proc/driver/nvidia-caps/sys-minors
  cap sys-module-version.txt  drv cat /sys/module/nvidia/version

  cap lib-listing.txt         drv sh -c 'ls -l /usr/lib/*/libnvidia-*.so* /usr/lib/*/libcuda*.so* /usr/lib64/libnvidia-*.so* 2>/dev/null; true'
  cap firmware-listing.txt    drv sh -c 'ls -R /lib/firmware/nvidia 2>/dev/null | head -100'
  cap bin-listing.txt         drv sh -c 'ls /usr/bin | grep -i nvidia'
  cap modinfo-nvidia.txt      drv sh -c 'modinfo nvidia | head -30'
  cap nvml-symbols.txt        drv sh -c 'nm -D --defined-only $(ls /usr/lib/*/libnvidia-ml.so.1 /usr/lib64/libnvidia-ml.so.1 2>/dev/null | head -1) | awk "{print \$3}" | sort'

  capture_nvml_dump
}

# The value-level NVML dump needs nvml-dump inside the driver container, which
# holds the real library. Copy the binary in and run it there.
capture_nvml_dump() {
  if [ -z "${NVML_DUMP}" ]; then
    log "NVML_DUMP not set; skipping the NVML value dump (see build-dump)"
    return
  fi
  [ -x "${NVML_DUMP}" ] || { log "NVML_DUMP=${NVML_DUMP} is not an executable file; skipping"; return; }

  # One path per run, so two captures of the same node never replace or remove
  # each other's binary.
  local bin="/tmp/nvml-dump-$$"
  if kubectl -n "${OPERATOR_NAMESPACE}" exec -i "${DRIVER_POD}" -c "${DRIVER_CONTAINER}" -- \
    sh -c 'cat > "$1" && chmod +x "$1"' sh "${bin}" <"${NVML_DUMP}" 2>>"${CAP_DIR}/errors.log"; then
    cap nvml-dump.json drv "${bin}"
    # Leave nothing behind in the driver container.
    drv rm -f "${bin}" >/dev/null 2>&1 || true
  else
    printf 'FAILED: nvml-dump.json (copy into the driver container)\n' | tee -a "${CAP_DIR}/errors.log" >&2
  fi
}

# The host pod is privileged and sees the node's /sys, /dev and /proc. Its tools
# come from apt; if the cluster has no egress the sysfs captures still work and
# the rest fail into errors.log.
start_host_pod() {
  local node=$1
  # One pod per run, so two captures of the same node never delete each
  # other's pod. The node name is cut to keep the pod name within 63 characters.
  local suffix="-$$"
  HOST_POD="capture-${node:0:$((63 - 8 - ${#suffix}))}${suffix}"
  local annotations=""
  if [ "${HOST_POD_MOCK:-}" = 1 ]; then
    annotations=$'\n  annotations:\n    nvml-mock.nvidia.com/devices: "true"\n    nvml-mock.nvidia.com/infiniband: "true"'
  fi
  kubectl -n "${HOST_POD_NAMESPACE}" apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${HOST_POD}${annotations}
spec:
  nodeName: ${node}
  hostPID: true
  hostNetwork: true
  restartPolicy: Never
  tolerations: [{operator: Exists}]
  containers:
  - name: c
    image: ${HOST_POD_IMAGE}
    command: ["sleep", "3600"]
    securityContext: {privileged: true}
    volumeMounts: [{name: host, mountPath: /host}]
  volumes: [{name: host, hostPath: {path: /}}]
EOF
  kubectl -n "${HOST_POD_NAMESPACE}" wait --for=condition=Ready "pod/${HOST_POD}" --timeout=180s >/dev/null \
    || { log "host pod did not become ready on ${node}"; return 1; }
  host sh -c 'apt-get update -qq && apt-get install -y -qq pciutils numactl rdma-core ibverbs-utils infiniband-diags iproute2 ethtool >/dev/null' \
    >/dev/null 2>>"${CAP_DIR}/errors.log" || log "tool install failed in the host pod; some captures will fail"
}

stop_host_pod() {
  [ -n "${HOST_POD}" ] || return 0
  kubectl -n "${HOST_POD_NAMESPACE}" delete pod "${HOST_POD}" --wait=false >/dev/null 2>&1 || true
  HOST_POD=""
}

capture_host() {
  local node=$1
  require_cluster
  node_dir "${node}"
  log "== ${node}: host pod"
  start_host_pod "${node}" || { stop_host_pod; return; }

  cap host-uname.txt          host uname -a
  cap host-os-release.txt     host cat /host/etc/os-release
  cap host-pagesize.txt       host getconf PAGESIZE
  cap host-lscpu.txt          host lscpu
  cap host-numactl.txt        host numactl -H
  cap host-numa-cpulist.txt   host sh -c 'for n in /sys/devices/system/node/node*; do echo "$n: $(cat $n/cpulist)"; done'
  cap host-dmi.txt            host sh -c 'for f in product_name sys_vendor board_name bios_version; do echo "$f=$(cat /sys/class/dmi/id/$f)"; done'

  cap host-lspci-nvidia.txt   host lspci -nnvvv -d 10de:
  cap host-lspci-mellanox.txt host lspci -nn -d 15b3:
  cap host-pci-nvidia.txt     host sh -c 'for d in /sys/bus/pci/devices/*; do [ "$(cat $d/vendor)" = 0x10de ] || continue; echo "$d device=$(cat $d/device) class=$(cat $d/class) subsys=$(cat $d/subsystem_vendor):$(cat $d/subsystem_device) numa=$(cat $d/numa_node) link=$(cat $d/current_link_speed)x$(cat $d/current_link_width) max=$(cat $d/max_link_speed)x$(cat $d/max_link_width)"; done'
  cap host-pci-hierarchy.txt  host sh -c 'readlink -f /sys/bus/pci/devices/*'

  cap host-dev-nvidia.txt     host sh -c 'ls -l /dev/nvidia* /dev/nvidia-caps* /dev/nvidia-caps-imex-channels 2>&1; stat -c "%n %t:%T %a %U:%G" /dev/nvidia* 2>&1; true'
  cap host-proc-devices.txt   host sh -c 'grep -i nvidia /proc/devices'
  cap host-proc-modules.txt   host sh -c 'grep -E "^(nvidia|nvidia_uvm|nvidia_modeset|nvidia_peermem|mlx5_core|ib_core)" /proc/modules'

  cap host-ibstat.txt         host ibstat
  cap host-ibv-devinfo.txt    host ibv_devinfo -v
  cap host-rdma-link.txt      host rdma link show
  cap host-ib-sysfs.txt       host sh -c 'for h in /sys/class/infiniband/*; do echo "== $h"; for f in node_guid node_type fw_ver hca_type board_id; do echo "$f=$(cat $h/$f 2>&1)"; done; for p in $h/ports/*; do echo "port $(basename $p) state=$(cat $p/state) phys=$(cat $p/phys_state) rate=$(cat $p/rate) layer=$(cat $p/link_layer)"; done; done'
  cap host-ib-dev.txt         host sh -c 'ls -l /dev/infiniband 2>&1'

  capture_network_surfaces

  # CDI specs the operator wrote. /var/run is a symlink to /run, which would
  # resolve inside the pod, so read /run through the host mount.
  cap cdi-specs-host.txt      host sh -c 'ls -l /host/run/cdi /host/etc/cdi 2>&1; for f in /host/run/cdi/* /host/etc/cdi/*; do echo "== $f"; cat "$f"; done 2>/dev/null; true'
  local toolkit_pod
  toolkit_pod=$(kubectl -n "${OPERATOR_NAMESPACE}" get pods -o name \
    --field-selector "spec.nodeName=${node}" | grep container-toolkit | cut -d/ -f2 | head -1)
  if [ -n "${toolkit_pod}" ]; then
    cap cdi-toolkit-list.txt  kubectl -n "${OPERATOR_NAMESPACE}" exec "${toolkit_pod}" -c "${TOOLKIT_CONTAINER}" -- "${TOOLKIT_CTK}" cdi list
  fi

  stop_host_pod
}

# Network surfaces, taken inside the host pod. A privileged pod on the host
# network sees the node's own interfaces, /sys/class/net and RDMA devices.
capture_network_surfaces() {
  cap pci-all-functions.txt   host sh -c 'for d in /sys/bus/pci/devices/*; do echo "$(basename $d) vendor=$(cat $d/vendor) device=$(cat $d/device) class=$(cat $d/class) numa=$(cat $d/numa_node) driver=$(basename $(readlink $d/driver 2>/dev/null) 2>/dev/null) root=$(readlink -f $d | cut -d/ -f4)"; done'
  cap pci-nic-functions.txt   host sh -c 'for d in /sys/bus/pci/devices/*; do [ "$(cat $d/vendor)" = 0x15b3 ] || continue; echo "$(basename $d) subsys=$(cat $d/subsystem_vendor):$(cat $d/subsystem_device) sriov_totalvfs=$(cat $d/sriov_totalvfs 2>&1) sriov_numvfs=$(cat $d/sriov_numvfs 2>&1) physfn=$(readlink $d/physfn 2>&1) virtfn=$(ls -d $d/virtfn* 2>/dev/null | wc -l)"; done'
  cap host-lspci-mellanox-vv.txt host lspci -nnvvv -d 15b3:
  cap pci-vpd.txt             host sh -c 'for d in /sys/bus/pci/devices/*; do [ "$(cat $d/vendor)" = 0x15b3 ] || continue; echo "$(basename $d): $(tr -c "[:print:]" " " < $d/vpd 2>/dev/null | grep -oE "PN[^ ]* [^ ]*" | tr "\n" " ")"; done'

  cap net-ip-link.txt         host ip -d link show
  cap net-sysfs.txt           host sh -c 'for i in /sys/class/net/*; do echo "== $(basename $i)"; for f in address operstate carrier speed duplex mtu type dev_id dev_port; do echo "$f=$(cat $i/$f 2>&1)"; done; echo "device=$(readlink -f $i/device 2>&1)"; done'
  cap net-ethtool-i.txt       host sh -c 'for i in /sys/class/net/*; do n=$(basename $i); echo "== $n"; ethtool -i $n 2>&1; done'

  cap rdma-dev.txt            host rdma dev show
  cap rdma-link.txt           host rdma link show
  cap rdma-sysfs.txt          host_script <<'EOF_RDMA'
for h in /sys/class/infiniband/*; do
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
EOF_RDMA
  cap rdma-counters.txt       host_script <<'EOF_CNT'
for h in /sys/class/infiniband/*; do
  p=$h/ports/1
  echo "== $(basename $h) port 1"
  for d in counters hw_counters; do
    for c in $p/$d/*; do [ -f "$c" ] && echo "$d/$(basename $c)=$(cat $c 2>&1)"; done
  done
done
EOF_CNT
  cap rdma-classes.txt        host sh -c 'for c in infiniband infiniband_verbs infiniband_mad infiniband_cm; do echo "== /sys/class/$c"; ls /sys/class/$c 2>&1; done; true'

  cap sys-module-mlx5.txt     host sh -c 'for m in mlx5_core mlx5_ib ib_core ib_uverbs nvidia; do echo "$m version=$(cat /sys/module/$m/version 2>&1) parameters=$(ls /sys/module/$m/parameters 2>&1 | tr "\n" " ")"; done'
  cap kernel-log-nic-gpu.txt  host sh -c 'dmesg 2>&1 | grep -i -E "mlx5|NVRM|nvidia" | tail -300'
}

# network <node>: only the network surfaces, for adding to an existing capture.
capture_network() {
  local node=$1
  require_cluster
  node_dir "${node}"
  log "== ${node}: network surfaces (host pod)"
  start_host_pod "${node}" || { stop_host_pod; return; }
  capture_network_surfaces
  stop_host_pod
}

capture_cluster_network() {
  CAP_DIR="${OUT}/cluster"
  mkdir -p "${CAP_DIR}"
  cap crds-network.txt        sh -c 'kubectl get crd -o name | grep -i -E "mellanox|nvidia|sriov|nfd|k8s.cni|dranet|topograph"'
  cap nodefeatures.yaml       kubectl get nodefeatures -A -o yaml
  cap nicclusterpolicy.yaml   kubectl get nicclusterpolicies.mellanox.com -o yaml
  cap network-attachments.yaml kubectl get network-attachment-definitions -A -o yaml
  cap sriov-node-state.yaml   kubectl get sriovnetworknodestates -A -o yaml
}

capture_cluster() {
  require_cluster
  CAP_DIR="${OUT}/cluster"
  mkdir -p "${CAP_DIR}"
  log "== cluster"

  cap versions.txt            sh -c 'kubectl version -o yaml; date -u'
  cap nodes-wide.txt          kubectl get nodes -o wide
  cap nodes.yaml              kubectl get nodes -o yaml
  cap gpu-labels.txt          kubectl get nodes -L nvidia.com/gpu.product,nvidia.com/gpu.machine,nvidia.com/gpu.family,nvidia.com/gpu.count
  cap resourceslices.yaml     kubectl get resourceslices -o yaml
  cap deviceclasses.yaml      kubectl get deviceclasses -o yaml
  cap computedomains.yaml     kubectl get computedomains -A -o yaml
  cap clusterpolicy.yaml      kubectl get clusterpolicy -o yaml
  cap operator-pods.txt       kubectl -n "${OPERATOR_NAMESPACE}" get pods -o wide
  cap operator-daemonsets.yaml kubectl -n "${OPERATOR_NAMESPACE}" get daemonsets -o yaml
  cap operator-configmaps.yaml kubectl -n "${OPERATOR_NAMESPACE}" get configmaps -o yaml
  cap validator-logs.txt      kubectl -n "${OPERATOR_NAMESPACE}" logs -l app=nvidia-operator-validator --all-containers --prefix --tail=-1

  # DCGM metrics through the API server, one file per exporter pod.
  local pod
  for pod in $(kubectl -n "${OPERATOR_NAMESPACE}" get pods -l app=nvidia-dcgm-exporter -o name | cut -d/ -f2); do
    cap "dcgm-${pod}.txt" kubectl get --raw "/api/v1/namespaces/${OPERATOR_NAMESPACE}/pods/${pod}:9400/proxy/metrics"
  done
}

build_dump() {
  command -v docker >/dev/null || die "docker is required to build nvml-dump"
  local root version
  root=$(git rev-parse --show-toplevel) || die "run from inside the repository"
  version=$("${root}/hack/golang-version.sh")
  mkdir -p "${OUT}/bin"
  # go-nvml loads the driver's library at run time, so the binary is dynamically
  # linked and must be built for the node's architecture, on a host of that
  # architecture.
  local platform=()
  [ -z "${DUMP_PLATFORM}" ] || platform=(--platform "${DUMP_PLATFORM}")
  docker run --rm "${platform[@]}" -v "${root}:/src" -v "$(cd "${OUT}/bin" && pwd):/out" -w /src \
    "golang:${version}" go build -o /out/nvml-dump-linux ./hack/tools/nvml-dump \
    || die "build failed"
  log "built ${OUT}/bin/nvml-dump-linux; run with NVML_DUMP=${OUT}/bin/nvml-dump-linux"
}

# The mock side of the comparison: what the nvml-mock pod's library answers on
# the same calls, written under $OUT/<node>, beside the output of `host`, in the same layout a
# real node uses, so `compare` and `nvml-dump diff` can read both. The pod runs the same
# binary as the real driver container, so build it for the mock cluster's
# architecture (DUMP_PLATFORM).
# The nvml-mock image has no nm, so copy the library out and read it here. llvm-nm
# is preferred because the system nm on macOS cannot read ELF files.
mock_symbols() {
  local dir=$1 tool lib
  tool=$(command -v llvm-nm || command -v nm) || { printf 'FAILED: nvml-symbols.txt (no nm here)\n' | tee -a "${dir}/errors.log" >&2; return; }
  lib=$(mktemp)
  if kubectl -n "${MOCK_NAMESPACE}" exec "${pod}" -- sh -c \
    "cat \$(ls ${MOCK_LIB_DIR}/libnvidia-ml.so.1 /usr/local/lib/libnvidia-ml.so.1 2>/dev/null | head -1)" >"${lib}" 2>>"${dir}/errors.log" \
    && "${tool}" -D --defined-only "${lib}" 2>>"${dir}/errors.log" | awk '{print $3}' | sort >"${dir}/nvml-symbols.txt"; then
    :
  else
    printf 'FAILED: nvml-symbols.txt\n' | tee -a "${dir}/errors.log" >&2
  fi
  rm -f "${lib}"
}

capture_mock() {
  local node=$1
  require_cluster
  node_dir "${node}"
  local pod
  pod=$(kubectl -n "${MOCK_NAMESPACE}" get pod -l app.kubernetes.io/name=nvml-mock \
    --field-selector "spec.nodeName=${node}" -o jsonpath='{.items[0].metadata.name}' 2>>"${CAP_DIR}/errors.log")
  [ -n "${pod}" ] || { log "no nvml-mock pod on ${node} in ${MOCK_NAMESPACE}"; return 1; }
  log "== mock ${node} (${pod})"

  mock() { kubectl -n "${MOCK_NAMESPACE}" exec "${pod}" -- "$@"; }
  local libs="LD_LIBRARY_PATH=${MOCK_LIB_DIR}:\${LD_LIBRARY_PATH:-}"

  cap smi-q.xml mock sh -c "${libs} nvidia-smi -q -x"
  cap smi-q.txt mock sh -c "${libs} nvidia-smi -q"
  cap smi-L.txt mock sh -c "${libs} nvidia-smi -L"
  cap smi-topo-m.txt mock sh -c "${libs} nvidia-smi topo -m"
  mock_symbols "${CAP_DIR}"

  if [ -z "${NVML_DUMP}" ] || [ ! -x "${NVML_DUMP}" ]; then
    log "NVML_DUMP not set to an executable; skipping the NVML value dump (see build-dump)"
    return
  fi
  if kubectl -n "${MOCK_NAMESPACE}" exec -i "${pod}" -- \
    sh -c 'cat > /tmp/nvml-dump && chmod +x /tmp/nvml-dump' <"${NVML_DUMP}" 2>>"${CAP_DIR}/errors.log"; then
    cap nvml-dump.json mock sh -c "${libs} /tmp/nvml-dump"
  else
    printf 'FAILED: nvml-dump.json (copy into the mock pod)\n' | tee -a "${CAP_DIR}/errors.log" >&2
  fi
}

# compare <real-dir> <mock-dir>: for every file both directories hold, the share
# of the real file's lines that the mock's file also has, grouped by surface (the
# file name's prefix: smi, proc, host, pci, rdma, net). A line-level match is a
# blunt measure: placeholders and numbering differ between a scrubbed capture and
# a live mock, so treat it as a pointer to the files worth reading, not a score.
# The structured comparisons are nvml-dump diff and smi-xml-diff.
compare_dirs() {
  local real=$1 mock=$2
  [ -d "${real}" ] && [ -d "${mock}" ] || die "compare needs two directories"
  local f name surface total common
  printf '%-8s %-44s %8s %8s %6s\n' surface file real-lines shared pct
  for f in "${real}"/*; do
    name=$(basename "${f}")
    case "${name}" in errors.log | proc-* | *.json | *.json.gz | *.xml | *.so*) continue ;; esac
    surface=${name%%-*}
    total=$(wc -l <"${f}" | tr -d ' ')
    if [ ! -f "${mock}/${name}" ]; then
      printf '%-8s %-44s %8s %8s %6s\n' "${surface}" "${name}" "${total}" "-" "absent"
      continue
    fi
    common=$(comm -12 <(sort "${f}") <(sort "${mock}/${name}") | wc -l | tr -d ' ')
    printf '%-8s %-44s %8s %8s %5s%%\n' "${surface}" "${name}" "${total}" "${common}" \
      "$([ "${total}" -gt 0 ] && echo $((100 * common / total)) || echo 100)"
  done | sort -k1,1 -k2,2
  log ""
  log "JSON dumps:  nvml-dump diff ${real}/nvml-dump.json ${mock}/nvml-dump.json"
  log "smi XML:     smi-xml-diff ${real}/smi-q.xml ${mock}/smi-q.xml"
}

# Empty files usually mean a dropped connection, not an empty answer.
report() {
  log ""
  log "Captured under ${OUT}"
  local empty
  empty=$(find "${OUT}" -type f -size 0 ! -name errors.log 2>/dev/null)
  if [ -n "${empty}" ]; then
    log "Empty files (rerun the command that produced each):"
    printf '%s\n' "${empty}" >&2
  fi
  log "Commands that failed are listed in errors.log beside each output."
  log "The raw capture holds serial numbers, UUIDs and addresses; scrub it before sharing."
}

main() {
  [ $# -ge 1 ] || { usage; exit 2; }
  local command=$1
  shift
  local node

  # An interrupted run must not leave a privileged pod on the node: delete it on
  # every way out. INT and TERM exit, which runs the EXIT trap.
  trap stop_host_pod EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  case "${command}" in
    driver) [ $# -ge 1 ] || die "driver needs at least one node"
      for node in "$@"; do capture_driver "${node}"; done; report ;;
    host) [ $# -ge 1 ] || die "host needs at least one node"
      for node in "$@"; do capture_host "${node}"; done; report ;;
    network) [ $# -ge 1 ] || die "network needs at least one node"
      for node in "$@"; do capture_network "${node}"; done
      require_cluster; capture_cluster_network; report ;;
    cluster) capture_cluster; capture_cluster_network; report ;;
    all) [ $# -ge 1 ] || die "all needs at least one node"
      for node in "$@"; do capture_driver "${node}"; capture_host "${node}"; done
      capture_cluster; capture_cluster_network; report ;;
    build-dump) build_dump ;;
    mock-dump) [ $# -ge 1 ] || die "mock-dump needs at least one node"
      for node in "$@"; do capture_mock "${node}"; done; report ;;
    compare) [ $# -eq 2 ] || die "compare needs <real-dir> <mock-dir>"
      compare_dirs "$1" "$2" ;;
    -h | --help | help) usage ;;
    *) usage; exit 2 ;;
  esac
}

main "$@"
