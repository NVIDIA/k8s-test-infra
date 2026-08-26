#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Stages the InfiniBand CLI tools and their shared-library closure into a
# self-contained tree, and gives every binary an RPATH relative to its own
# location. Runs ONCE at image build time, from the Dockerfile.
#
# Build time is deliberate. The node agent's infiniband simulator copies this
# tree into the node overlay at runtime, where patchelf is not installed and the
# ordering of the injected LD_LIBRARY_PATH would otherwise decide which library
# wins. Baking the RPATH here is what makes each tool self-locating in every
# mount context, exactly as the Dockerfile already does for nvidia-smi.
# See NVIDIA/k8s-test-infra#438.
#
# Layout produced (mirrors the overlay's driver/usr/{bin,lib64}):
#   /usr/local/nvml-mock-ib/bin/<tool>     RPATH=$ORIGIN/../lib64
#   /usr/local/nvml-mock-ib/lib64/<lib>    RPATH=$ORIGIN
set -eu

STAGE_ROOT=${STAGE_ROOT:-/usr/local/nvml-mock-ib}
BIN_DIR="$STAGE_ROOT/bin"
LIB_DIR="$STAGE_ROOT/lib64"

# ELF tools only. ibstatus is a /bin/sh script: it has no RPATH to set and it
# only runs in images that ship a shell, so the infiniband simulator picks it up
# off PATH instead (see fallbackTools in internal/agent/infiniband/stage.go).
IB_TOOLS="ibnetdiscover ibstat iblinkinfo sminfo ibping ibv_devinfo"

# Libraries every glibc image already provides. Staging these would put a
# second copy of the C runtime ahead of the container's own on the search
# path, which is how you get a binary running against a libc that does not
# match its loader. The tools' own dependencies are what we stage.
is_glibc_core() {
	case "$1" in
	libc.so.* | libm.so.* | libdl.so.* | librt.so.* | libpthread.so.* | \
		ld-linux*.so.* | ld.so.* | linux-vdso.so.*)
		return 0
		;;
	esac
	return 1
}

mkdir -p "$BIN_DIR" "$LIB_DIR"

# 1. Collect the tools.
staged_tools=""
for tool in $IB_TOOLS; do
	path=$(command -v "$tool" 2>/dev/null) || {
		echo "ERROR: $tool not found in PATH; the image must install infiniband-diags/ibverbs-utils" >&2
		exit 1
	}
	cp "$path" "$BIN_DIR/$tool"
	staged_tools="$staged_tools $tool"
done

# 2. Collect their dependency closure. ldd resolves transitively, so this
#    picks up libnl-3/libnl-route-3, which no tool names directly — they
#    arrive through libibverbs.
for tool in $IB_TOOLS; do
	ldd "$BIN_DIR/$tool"
done | awk '/=>/ && $3 ~ /^\// {print $3}' | sort -u | while read -r lib; do
	base=$(basename "$lib")
	if is_glibc_core "$base"; then
		continue
	fi
	# -L dereferences the SONAME symlink so the overlay gets a real file.
	cp -aL "$lib" "$LIB_DIR/$base"
done

# 3. Set the RPATHs.
#
#    The hard case is a transitive dependency: ibv_devinfo names libnl
#    nowhere, it reaches it through libibverbs. Two independent mechanisms
#    resolve that, and this stages both:
#
#      a. --force-rpath writes DT_RPATH rather than patchelf's default
#         DT_RUNPATH. DT_RUNPATH applies only to the direct dependencies of
#         the object carrying it; DT_RPATH is inherited down the chain.
#      b. $ORIGIN on each staged library, so a library resolves its own
#         dependencies from the directory it was loaded out of.
#
#    Measured on distroless with libnl staged: strip (b) and keep (a) and
#    ibv_devinfo loads; strip (a) and keep (b) and it loads; strip both and it
#    dies with "libnl-route-3.so.200: cannot open shared object file". Either
#    is sufficient alone, so keeping both means no single edit here silently
#    reintroduces the bug.
#    The single quotes below are deliberate and must stay. $ORIGIN is resolved
#    by the dynamic loader at load time, not by this shell — expanding it here
#    would bake in a build-time absolute path and defeat the entire point.
# shellcheck disable=SC2016
for tool in "$BIN_DIR"/*; do
	patchelf --force-rpath --set-rpath '$ORIGIN/../lib64' "$tool"
done
staged_libs=""
# shellcheck disable=SC2016
for lib in "$LIB_DIR"/*; do
	patchelf --force-rpath --set-rpath '$ORIGIN' "$lib"
	staged_libs="$staged_libs $(basename "$lib")"
done

# 4. Self-verify. A partial run must not exit 0: without this, a tool whose
#    dependency failed to copy still produces a plausible-looking tree and the
#    breakage only surfaces at e2e as a missing-.so error inside a pod.
#
#    The allowlist below is spelled out again on purpose. An earlier revision
#    called is_glibc_core() here too, and that made the check unable to fail:
#    widening the staging filter widened the verifier with it, so a run that
#    dropped libnl still exited 0. A verifier that shares its predicate with
#    the step it verifies is not a verifier. Keep these two lists independent.
needs_staging() {
	case "$1" in
	libc.so.* | libm.so.* | libdl.so.* | librt.so.* | libpthread.so.* | \
		ld-linux*.so.* | ld.so.* | linux-vdso.so.*)
		return 1
		;;
	esac
	return 0
}

rc=0
# shellcheck disable=SC2016 # literal $ORIGIN, see above
for tool in "$BIN_DIR"/*; do
	got=$(patchelf --print-rpath "$tool")
	if [ "$got" != '$ORIGIN/../lib64' ]; then
		echo "ERROR: $tool has RPATH '$got', want '\$ORIGIN/../lib64'" >&2
		rc=1
	fi
done
for obj in "$BIN_DIR"/* "$LIB_DIR"/*; do
	for need in $(patchelf --print-needed "$obj"); do
		if ! needs_staging "$need"; then
			continue
		fi
		if [ ! -e "$LIB_DIR/$need" ]; then
			echo "ERROR: $obj needs $need, which is not staged in $LIB_DIR" >&2
			rc=1
		fi
	done
done
if [ "$rc" -ne 0 ]; then
	exit "$rc"
fi

echo "Staged IB tools:$staged_tools"
echo "Staged IB libraries:$staged_libs"
