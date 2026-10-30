#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Runs one stub image the way the driver DaemonSet does, in a container whose mount namespace
# stands in for the node's (node.sh), and checks it against the operator's own startup probe.
set -euo pipefail

image="$1"
here="$(cd "$(dirname "$0")" && pwd)"
install_seconds=5
name="driver-stub-smoke-$$"

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

probe() { docker exec "$name" sh /usr/local/bin/startup-probe.sh >/dev/null 2>&1; }

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# Without Mokka's tree over /sys/module the stand-in refuses to run, before it binds anything.
if out=$(docker run --rm --privileged "$image" nvidia-driver init 2>&1); then
	fail "ran without Mokka's module tree"
fi
case "$out" in
*"installing driver"*) fail "started the install without the module tree: $out" ;;
*"/sys/module is the kernel's"*) echo "ok: refuses to run without Mokka's module tree" ;;
*) fail "unexpected failure without the module tree: $out" ;;
esac

# Runs the stand-in under node.sh; the tmpfs stands in for Mokka's bind of the simulated modules
# over /sys/module.
run() {
	docker run -d --name "$name" --privileged --tmpfs /sys/module \
		-v "$here/node.sh:/usr/local/bin/node.sh:ro" \
		-v "$here/startup-probe.sh:/usr/local/bin/startup-probe.sh:ro" \
		"$@" --entrypoint sh "$image" /usr/local/bin/node.sh >/dev/null
}

run -e STUB_INSTALL_SECONDS="$install_seconds"

sleep 1
probe && fail "probe passed during the install"
echo "ok: probe fails during the install"

sleep "$install_seconds"
probe || { docker logs "$name" >&2; fail "probe still failing after the install"; }
docker exec "$name" test -f /run/nvidia/validations/.driver-ctr-ready
echo "ok: probe passes after the install and wrote .driver-ctr-ready"

# The loaded modules as the kernel lays them out: nvidia held by the two that depend on it.
loaded=$(docker exec "$name" sh -c 'cd /sys/module && for m in *; do [ -f "$m/refcnt" ] || continue; echo "$m:$(cat $m/refcnt):$(ls $m/holders | paste -sd, -):$(cat $m/initstate)"; done')
[ "$loaded" = "nvidia:2:nvidia_modeset,nvidia_uvm:live
nvidia_modeset:0::live
nvidia_uvm:0::live" ] || fail "loaded modules: $loaded"
echo "ok: loaded nvidia, held by nvidia_uvm and nvidia_modeset"

firmware=$(docker exec "$name" sh -c 'ls /sys/module/firmware_class/parameters; cat /sys/module/firmware_class/parameters/path')
[ "$firmware" = "path
/lib/firmware" ] || fail "firmware search path: $firmware"
echo "ok: kept the firmware search path mounted in /sys/module"

binds=$(docker exec "$name" sh -c 'grep -c " /run/nvidia/driver " /proc/self/mountinfo; cat /run/nvidia/driver/stub-root /proc/modules')
[ "$binds" = "2
stub-root
nvidia 62312448 2 nvidia_uvm,nvidia_modeset, Live 0x0000000000000000" ] || fail "binds: $binds"
echo "ok: bound the driver root over the node agent's, and the module list"

start=$(date +%s)
docker stop -t 30 "$name" >/dev/null
elapsed=$(($(date +%s) - start))
[ "$elapsed" -lt 10 ] || fail "stop took ${elapsed}s; SIGTERM not handled"
echo "ok: stopped in ${elapsed}s"

logs=$(docker logs "$name" 2>&1)
echo "$logs"
[[ "$logs" == *"binds left: 1 0"* ]] || fail "binds left after stopping are not just the node agent's"
echo "ok: removed its own binds and left the node agent's"
