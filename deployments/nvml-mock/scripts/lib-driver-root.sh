#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Shared driver-root construction helpers, sourced by:
#   - deployments/nvml-mock/scripts/setup.sh          (host driver root)
#   - deployments/mock-driver/scripts/nvidia-driver   (driver-container rootfs)
# Both build the same layout from the same artifacts; keep every layout
# decision (library names, symlink chains, proc-file formats, device-node
# majors, config injection) here so the two driver roots cannot diverge.

# drl_require_version VERSION
# Driver versions are digits and dots only. This also guards the sed
# interpolation in drl_write_config against metacharacters.
drl_require_version() {
  case "$1" in
    ''|*[!0-9.]*)
      echo "ERROR: invalid driver version '$1' (digits and dots only)" >&2
      return 1
      ;;
  esac
}

# drl_cap_gpu_count CONFIG_FILE COUNT
# Echo COUNT capped at the profile's device count (warning goes to stderr).
drl_cap_gpu_count() {
  _drl_config=$1
  _drl_count=$2
  # grep -c prints 0 itself on no match; only its exit code needs ignoring
  _drl_profile_count=$(grep -c "^[[:space:]]*- index:" "$_drl_config" || true)
  if [ "$_drl_profile_count" -gt 0 ] && [ "$_drl_count" -gt "$_drl_profile_count" ]; then
    echo "WARNING: GPU count ($_drl_count) exceeds profile devices ($_drl_profile_count). Capping to $_drl_profile_count." >&2
    _drl_count=$_drl_profile_count
  fi
  echo "$_drl_count"
}

# drl_lib_filenames DRIVER_VERSION
# Echo the library filenames drl_install_libs creates (one per line) -- the
# single source of truth for manifest tracking and ownership guards.
drl_lib_filenames() {
  printf '%s\n' \
    "libnvidia-ml.so.$1" libnvidia-ml.so.1 libnvidia-ml.so \
    "libcuda.so.$1" libcuda.so.1 libcuda.so \
    libcudart.so.12 libcudart.so
}

# drl_install_libs SRC_DIR DEST_DIR DRIVER_VERSION
# Copy the built mock NVML/CUDA libraries from SRC_DIR into DEST_DIR under
# the runtime DRIVER_VERSION name (the .so is built with a fixed Makefile
# LIB_VERSION) and create the consumer-facing symlink chains.
drl_install_libs() {
  _drl_src=$1
  _drl_dest=$2
  _drl_ver=$3
  drl_require_version "$_drl_ver" || return 1
  mkdir -p "$_drl_dest"
  _drl_so=$(ls "$_drl_src"/libnvidia-ml.so.*.*.* 2>/dev/null | head -1)
  if [ -z "$_drl_so" ]; then
    echo "ERROR: No mock NVML library found in $_drl_src" >&2
    return 1
  fi
  # rm before cp: unlink keeps existing mmaps of an old copy valid, whereas
  # cp alone truncates the live inode (SIGBUS for long-running consumers)
  rm -f "$_drl_dest/libnvidia-ml.so.$_drl_ver"
  cp "$_drl_so" "$_drl_dest/libnvidia-ml.so.$_drl_ver"
  ln -sf "libnvidia-ml.so.$_drl_ver" "$_drl_dest/libnvidia-ml.so.1"
  ln -sf "libnvidia-ml.so.1" "$_drl_dest/libnvidia-ml.so"

  _drl_cuda=$(ls "$_drl_src"/libcuda.so.*.*.* 2>/dev/null | head -1)
  if [ -z "$_drl_cuda" ]; then
    echo "WARNING: No mock CUDA library found in $_drl_src, skipping libcuda.so setup"
  else
    rm -f "$_drl_dest/libcuda.so.$_drl_ver"
    cp "$_drl_cuda" "$_drl_dest/libcuda.so.$_drl_ver"
    ln -sf "libcuda.so.$_drl_ver" "$_drl_dest/libcuda.so.1"
    ln -sf "libcuda.so.1" "$_drl_dest/libcuda.so"
    # TODO: properly split driver API (libcuda.so) and runtime API (libcudart.so)
    # For now, our mock exports CUDA Runtime API symbols but is built as libcuda.so.
    # CUDA samples (e.g. vectorAdd) link against libcudart.so, so create a symlink.
    ln -sf "libcuda.so.1" "$_drl_dest/libcudart.so.12"
    ln -sf "libcudart.so.12" "$_drl_dest/libcudart.so"
  fi
}

# drl_write_proc_files PROC_NVIDIA_DIR DRIVER_VERSION
# Write /proc/driver/nvidia-format version and params files.
drl_write_proc_files() {
  _drl_dir=$1
  _drl_ver=$2
  mkdir -p "$_drl_dir"
  cat > "$_drl_dir/version" << PROC_VERSION_EOF
NVRM version: NVIDIA UNIX x86_64 Kernel Module  $_drl_ver  Thu Feb 20 23:41:34 UTC 2026
GCC version:  gcc version 12.2.0 (Debian 12.2.0-14)
PROC_VERSION_EOF
  cat > "$_drl_dir/params" << PROC_PARAMS_EOF
EnableMSI: 1
NVreg_RegistryDwords:
NVreg_DeviceFileGID: 0
NVreg_DeviceFileMode: 438
NVreg_DeviceFileUID: 0
NVreg_ModifyDeviceFiles: 1
NVreg_PreserveVideoMemoryAllocations: 0
NVreg_EnableResizableBar: 0
PROC_PARAMS_EOF
}

# drl_write_config SRC_CONFIG DEST_CONFIG COUNT DRIVER_VERSION
# Copy the profile config, inject num_devices (so the .so knows the GPU count
# without env vars), and pin system.driver_version plus the nvml_version
# driver suffix to DRIVER_VERSION -- keeping NVML answers in agreement with
# library filenames, nvidia-smi output, and the proc version file even when
# the driver version is overridden away from the profile default.
drl_write_config() {
  _drl_src=$1
  _drl_dest=$2
  _drl_count=$3
  _drl_ver=$4
  drl_require_version "$_drl_ver" || return 1
  cp "$_drl_src" "$_drl_dest"
  sed -i "/^system:/a\\  num_devices: $_drl_count" "$_drl_dest"
  sed -i "s/^\([[:space:]]*driver_version:\).*/\1 \"$_drl_ver\"/" "$_drl_dest"
  # Rewrite the nvml_version driver suffix, normalizing to a quoted value
  # whether or not the source was quoted (e.g. "12.550.163.01" or 12.550.163.01)
  sed -i "s/^\([[:space:]]*nvml_version:[[:space:]]*\)\"\{0,1\}\([0-9]\{1,\}\)\..*/\1\"\2.$_drl_ver\"/" "$_drl_dest"
}

# drl_mknod_devices DEV_DIR COUNT
# Create char device nodes: nvidia0..N-1 (major 195), nvidiactl (195,255),
# nvidia-uvm (510,0), nvidia-uvm-tools (510,1). Failures are ignored -- the
# nodes are cosmetic for consumers that receive devices via CDI.
drl_mknod_devices() {
  _drl_dev=$1
  _drl_count=$2
  mkdir -p "$_drl_dev"
  for _drl_i in $(seq 0 $((_drl_count - 1))); do
    mknod -m 666 "$_drl_dev/nvidia$_drl_i" c 195 "$_drl_i" 2>/dev/null || true
  done
  mknod -m 666 "$_drl_dev/nvidiactl" c 195 255 2>/dev/null || true
  mknod -m 666 "$_drl_dev/nvidia-uvm" c 510 0 2>/dev/null || true
  mknod -m 666 "$_drl_dev/nvidia-uvm-tools" c 510 1 2>/dev/null || true
}
