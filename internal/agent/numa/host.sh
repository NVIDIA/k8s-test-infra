#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd /
root=/var/lib/nvml-mock/numa
target=/sys/devices/system/node
unit=mokka-numa-filesystem.service
dropin=/etc/systemd/system/kubelet.service.d/30-mokka-numa.conf
action=${1:?action required}
managed=${2:-false}
own_mount() { [[ -f "$target/.mokka-numa" ]] && [[ "$(cat "$target/.mokka-numa")" == mokka-numa-v1 ]]; }
mount_tree() {
  if mountpoint -q "$target"; then
    own_mount || { echo "Refusing to replace an unmanaged NUMA mount" >&2; return 1; }
    return
  fi
  # Kind nodes share the VM's sysfs. New mounts must stay inside this node.
  mount --make-rprivate /sys
  mount --bind "$root/sysnode" "$target"
  mount -o remount,bind,ro "$target"
}
restore() {
  if [[ -f "$root/original/kubelet-config.yaml" ]]; then
    systemctl stop kubelet
    cp "$root/original/kubelet-config.yaml" /var/lib/kubelet/config.yaml
    for state in cpu_manager_state memory_manager_state; do
      if [[ -f "$root/original/$state" ]]; then
        cp "$root/original/$state" "/var/lib/kubelet/$state"
      elif [[ -f "$root/original/$state.absent" ]]; then
        rm -f "/var/lib/kubelet/$state"
      fi
    done
    managed=true
  fi
  rm -f "$dropin"
  if [[ -e "/etc/systemd/system/$unit" ]]; then
    systemctl disable --now "$unit"
  fi
  if own_mount; then umount "$target"; fi
  rm -f "/etc/systemd/system/$unit"
  systemctl daemon-reload
  if [[ "$managed" == true ]]; then systemctl restart kubelet; fi
  rm -f "$root/applied" /etc/systemd/system/mokka-numa-setup.service
  rm -rf "$root/original"
}
case "$action" in
 mount) mount_tree; exit 0 ;;
 restore) restore; exit 0 ;;
 apply) ;;
 *) echo "Unknown NUMA action: $action" >&2; exit 2 ;;
esac
[[ -f "$root/desired" && -f "$root/sysnode/online" ]]
if [[ -f "$root/applied" ]] && cmp -s "$root/applied" "$root/desired"; then exit 0; fi
if [[ "$managed" == true ]]; then
  [[ -f /var/lib/kubelet/config.yaml ]]
  mkdir -p "$root/original"
  if [[ ! -f "$root/original/kubelet-config.yaml" ]]; then
    cp /var/lib/kubelet/config.yaml "$root/original/kubelet-config.yaml"
  fi
  for state in cpu_manager_state memory_manager_state; do
    if [[ ! -f "$root/original/$state" && ! -f "$root/original/$state.absent" ]]; then
      if [[ -f "/var/lib/kubelet/$state" ]]; then
        cp "/var/lib/kubelet/$state" "$root/original/$state"
      else
        touch "$root/original/$state.absent"
      fi
    fi
  done
  [[ -f "$root/kubelet-config.yaml" ]]
fi
on_error() {
  result=$?
  if (( result != 0 )); then
    trap - EXIT
    echo "NUMA setup failed; restoring the original node configuration" >&2
    restore || echo "NUMA rollback failed; inspect $root/original" >&2
    exit "$result"
  fi
}
trap on_error EXIT
cat > "/etc/systemd/system/$unit" <<'UNIT'
[Unit]
Description=Mokka NUMA filesystem simulation
After=local-fs.target
Before=kubelet.service
[Service]
Type=oneshot
ExecStart=/bin/bash /var/lib/nvml-mock/numa/host.sh mount
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
UNIT
mkdir -p "$(dirname "$dropin")"
cat > "$dropin" <<'UNIT'
[Unit]
Requires=mokka-numa-filesystem.service
After=mokka-numa-filesystem.service
UNIT
systemctl daemon-reload
systemctl enable --now "$unit"
mount_tree
if [[ "$managed" == true ]]; then
  systemctl stop kubelet
  cp "$root/kubelet-config.yaml" /var/lib/kubelet/config.yaml
  # Policy checkpoints cannot be reused across a policy change. Restore keeps backups.
  rm -f /var/lib/kubelet/cpu_manager_state /var/lib/kubelet/memory_manager_state
  systemctl start kubelet
  healthy=false
  for attempt in $(seq 1 30); do
    if systemctl is-active --quiet kubelet && curl -fsS --max-time 2 http://127.0.0.1:10248/healthz >/dev/null; then healthy=true; break; fi
    sleep 1
  done
  [[ "$healthy" == true ]]
fi
cp "$root/desired" "$root/applied"
