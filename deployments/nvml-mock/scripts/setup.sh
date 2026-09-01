#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Entrypoint for the nvml-mock DaemonSet container.
#
# Holds only the phases the node agent has not claimed yet, each keeping its
# original number so the mapping to MEP-0003 stays readable. A ported phase is
# deleted rather than left as a duplicate: two owners of one surface diverge,
# and a phase that outlives the CLI it called fails the pod under `set -e`.
#
# Required env vars: GPU_COUNT, DRIVER_VERSION, NODE_NAME
set -e

HOST=/host/var/lib/nvml-mock
DRIVER_ROOT=$HOST/driver
# Co-locate device nodes under $DRIVER_ROOT so the upstream DRA driver's
# getDevRoot() (cmd/gpu-kubelet-plugin/root.go in NVIDIA/k8s-dra-driver-gpu)
# resolves devRoot to the mock driver root rather than falling back to "/".
DEV_ROOT=$DRIVER_ROOT/dev
CONFIG_DIR=$HOST/config

# Validate GPU_COUNT does not exceed profile device count
PROFILE_COUNT=$(grep -c "^[[:space:]]*- index:" /etc/nvml-mock/config.yaml || echo 0)
if [ "$PROFILE_COUNT" -gt 0 ] && [ "$GPU_COUNT" -gt "$PROFILE_COUNT" ]; then
  echo "WARNING: gpu.count ($GPU_COUNT) exceeds profile devices ($PROFILE_COUNT). Capping to $PROFILE_COUNT."
  GPU_COUNT=$PROFILE_COUNT
fi

echo "Setting up mock GPU environment: $GPU_COUNT GPUs, driver $DRIVER_VERSION"

# 1. Create directory structure
mkdir -p "$DRIVER_ROOT/usr/lib64" "$DRIVER_ROOT/usr/bin" "$DRIVER_ROOT/usr/local/lib" "$DRIVER_ROOT/config"
mkdir -p "$DEV_ROOT" "$CONFIG_DIR"
mkdir -p "$HOST/run"

# Runtime overrides (written by nvml-mock-ctl) are ephemeral: wipe them on
# every pod start so a restart of this DaemonSet resets simulated GPU state
# back to the pristine profile config.
rm -f "$CONFIG_DIR/overrides.yaml" "$DRIVER_ROOT/config/overrides.yaml"

# 6b. Stage the cluster-level ComputeDomain topology document into the overlay
#     tree so node-wide NRI injection can surface per-node fabric identity.
#     The daemon mounts the topology ConfigMap at /etc/nvml-mock/topology when
#     topology.enabled=true; the NRI plugin bind-mounts $HOST at the container
#     overlay path and injects MOCK_TOPOLOGY_CONFIG pointing here (plus the
#     node's NODE_NAME) so the mock NVML engine's applyTopologyOverlay() rewrites
#     each GPU's clique_id / cluster_uuid. No-op when topology is disabled.
if [ -f /etc/nvml-mock/topology/topology.yaml ]; then
  mkdir -p "$HOST/topology"
  cp /etc/nvml-mock/topology/topology.yaml "$HOST/topology/topology.yaml"
  echo "Staged ComputeDomain topology overlay at $HOST/topology/topology.yaml"
fi

# 7. Label node with nvidia.com/gpu.present (requires RBAC: get+patch on nodes).
if command -v kubectl >/dev/null 2>&1; then
  kubectl label node "$NODE_NAME" nvidia.com/gpu.present=true --overwrite || true
fi

# 8b. Deliberately NOT written here: /run/nvidia/validations/toolkit-ready.
#     Six GPU Operator operand DaemonSets ship an unconditional
#     `toolkit-validation` init container that loops on:
#       until [ -f /run/nvidia/validations/toolkit-ready ]; do sleep 5; done
#     (gpu-operator v26.3.0: assets/state-device-plugin/0500_daemonset.yaml:29,31;
#     gpu-feature-discovery/0500_daemonset.yaml:29,32;
#     state-dcgm-exporter/0800_daemonset.yaml:28,31; state-dcgm/0400_dcgm.yml:28,31;
#     state-mps-control-daemon/0400_daemonset.yaml:31,33;
#     state-mig-manager/0600_daemonset.yaml:28,31.)
#
#     That gate is real, but nvml-mock is not its satisfier and must not
#     pre-empt it. The marker's writer is GPU Operator's own nvidia-validator,
#     running as the operator-validator DaemonSet's `toolkit-validation` init
#     container with COMPONENT=toolkit
#     (assets/state-operator-validation/0500_daemonset.yaml:59-69); that
#     DaemonSet is deployed unconditionally. Toolkit.validate() DELETES the
#     marker (cmd/nvidia-validator/main.go:1134), runs nvidia-smi against our
#     mock driver, and re-creates it only on success (main.go:1153).
#
#     Writing it here was never durable — the validator deletes it on every run
#     and its preStop hook removes every *-ready file on shutdown
#     (state-operator-validation/0500_daemonset.yaml:133-136) — and it let
#     operands clear the gate before any toolkit check had run, turning an
#     ordering barrier into a no-op and making a green operand look like a
#     validated one. See #504.
#
#     The directory needs no mkdir either. The DaemonSets that mount the
#     validations dir directly — device-plugin
#     (state-device-plugin/0500_daemonset.yaml:146-149), mig-manager
#     (state-mig-manager/0600_daemonset.yaml:96-99) and the validator
#     (state-operator-validation/0500_daemonset.yaml:142-145) — all declare
#     hostPath type DirectoryOrCreate. The other four gated operands mount the
#     parent /run/nvidia instead: gpu-feature-discovery/0500_daemonset.yaml:129-132,
#     state-dcgm/0400_dcgm.yml:55-58 and
#     state-mps-control-daemon/0400_daemonset.yaml:125-128 as type Directory
#     (which requires the parent to pre-exist), and
#     state-dcgm-exporter/0800_daemonset.yaml:76-78 with no type at all — the
#     node-agent sidecar creates that parent. The validator also does os.Mkdir on the
#     status dir itself (main.go:524).
#
#     Residual hazard, as a debugging pointer: nvidia-validator has a cleanup-all
#     flag (main.go:302-309, env CLEANUP_ALL, default false) that os.RemoveAll's
#     the output dir before recreating it (main.go:513-527), wiping every marker;
#     because the validator's own /run/nvidia/validations is a bind mount
#     (state-operator-validation/0500_daemonset.yaml:54-55, 73-74, 96-97,
#     123-124, 138-139, over the hostPath volume cited above), the RemoveAll then
#     fails EBUSY on the mount point and the validator exits before recreating
#     anything (main.go:516-520 returns ahead of the os.Mkdir at :524), so
#     already-gated pods block on a marker that never returns while the validator
#     itself sits in CrashLoopBackOff. Nothing in assets/ or controllers/ sets
#     CLEANUP_ALL, so this is unreachable by default — but if an operand ever
#     hangs here, check this first.

echo "Mock GPU environment ready: $GPU_COUNT GPUs at $HOST"
