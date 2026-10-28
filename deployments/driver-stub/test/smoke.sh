#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Runs one stub image the way the driver DaemonSet does and checks it against the operator's own
# startup probe: failing during the install, passing after it, writing the ready file, and stopping
# on SIGTERM without waiting out the grace period.
set -euo pipefail

image="$1"
here="$(cd "$(dirname "$0")" && pwd)"
install_seconds=5
name="driver-stub-smoke-$$"
# Stands in for Mokka's bind of the simulated modules over /sys/module.
modules=$(mktemp -d)

cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
	rm -rf "$modules"
}
trap cleanup EXIT

probe() { docker exec "$name" sh /usr/local/bin/startup-probe.sh >/dev/null 2>&1; }

# Without Mokka's tree over /sys/module the stand-in refuses to run, before it binds anything.
if out=$(docker run --rm --privileged -e STUB_DRIVER_ROOT= -e STUB_PROC_MODULES= "$image" nvidia-driver init 2>&1); then
	echo "FAIL: ran without Mokka's module tree" >&2
	exit 1
fi
case "$out" in
*"installing driver"*)
	printf 'FAIL: started the install, and so the binds after it, without the module tree:\n%s\n' "$out" >&2
	exit 1
	;;
*"/sys/module is the kernel's"*) echo "ok: refuses to run without Mokka's module tree, before installing" ;;
*)
	printf 'FAIL: unexpected failure without the module tree:\n%s\n' "$out" >&2
	exit 1
	;;
esac

docker run -d --name "$name" --privileged \
	-e STUB_INSTALL_SECONDS="$install_seconds" \
	-e NODE_NAME=smoke -e STUB_DRIVER_ROOT= -e STUB_PROC_MODULES= \
	-v "$modules:/sys/module" \
	-v "$here/startup-probe.sh:/usr/local/bin/startup-probe.sh:ro" \
	"$image" nvidia-driver init >/dev/null

sleep 1
if probe; then
	echo "FAIL: probe passed during the install" >&2
	exit 1
fi
echo "ok: probe fails during the install"

sleep "$install_seconds"
if ! probe; then
	echo "FAIL: probe still failing after the install" >&2
	docker logs "$name" >&2
	exit 1
fi
docker exec "$name" test -f /run/nvidia/validations/.driver-ctr-ready
echo "ok: probe passes after the install and wrote .driver-ctr-ready"

# The loaded modules as the kernel lays them out: nvidia held by the two that depend on it.
loaded=$(docker exec "$name" sh -c 'cd /sys/module && for m in nvidia nvidia_uvm nvidia_modeset; do
	echo "$m:$(cat $m/refcnt):$(ls $m/holders | paste -sd, -)"; done')
expected="nvidia:2:nvidia_modeset,nvidia_uvm
nvidia_uvm:0:
nvidia_modeset:0:"
if [ "$loaded" != "$expected" ]; then
	printf 'FAIL: loaded modules:\n%s\nwant:\n%s\n' "$loaded" "$expected" >&2
	exit 1
fi
echo "ok: loaded nvidia, held by nvidia_uvm and nvidia_modeset"

start=$(date +%s)
docker stop -t 30 "$name" >/dev/null
elapsed=$(($(date +%s) - start))
if [ "$elapsed" -ge 10 ]; then
	echo "FAIL: stop took ${elapsed}s; SIGTERM not handled" >&2
	exit 1
fi
echo "ok: stopped in ${elapsed}s"

docker logs "$name"
