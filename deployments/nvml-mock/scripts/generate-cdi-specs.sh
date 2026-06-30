#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# CDI YAML fragments for tiered mock injection. Sourced by setup.sh.

# Usage: generate_mock_nvml_cdi_edits >> file
generate_mock_nvml_cdi_edits() {
  cat <<'EOF'
    - hostPath: /var/lib/nvml-mock/driver/usr/lib64/libnvidia-ml.so.1
      containerPath: /usr/lib64/libnvidia-ml.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/usr/bin/nvidia-smi
      containerPath: /usr/bin/nvidia-smi
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/config/config.yaml
      containerPath: /etc/nvml-mock/config.yaml
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/proc/driver/nvidia/version
      containerPath: /proc/driver/nvidia/version
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/proc/driver/nvidia/params
      containerPath: /proc/driver/nvidia/params
      options: [ro, nosuid, nodev, bind]
EOF
}

generate_mock_ib_cdi_edits() {
  cat <<'EOF'
    - hostPath: /var/lib/nvml-mock/driver/usr/local/lib/libibmockumad.so.1
      containerPath: /usr/local/lib/libibmockumad.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/usr/local/lib/libibmockverbs.so.1
      containerPath: /usr/local/lib/libibmockverbs.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/usr/local/lib/libibmocksys.so.1
      containerPath: /usr/local/lib/libibmocksys.so.1
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/ib
      containerPath: /var/lib/nvml-mock/ib
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/run/mock-ib.sock
      containerPath: /var/lib/nvml-mock/run/mock-ib.sock
      options: [rw, nosuid, nodev, bind]
EOF
}

generate_mock_pci_cdi_edits() {
  cat <<'EOF'
    - hostPath: /var/lib/nvml-mock/driver/usr/local/lib/libpcimocksys.so.1
      containerPath: /usr/local/lib/libpcimocksys.so.1
      options: [ro, nosuid, nodev, bind]
EOF
}

generate_full_tier_env_edits() {
  cat <<'EOF'
    - LD_PRELOAD=/usr/local/lib/libibmockumad.so.1:/usr/local/lib/libibmockverbs.so.1:/usr/local/lib/libibmocksys.so.1:/usr/local/lib/libpcimocksys.so.1
    - MOCK_IB=full
    - MOCK_IB_ROOT=/var/lib/nvml-mock/ib
    - MOCK_IB_PING_SOCKET=/var/lib/nvml-mock/run/mock-ib.sock
    - MOCK_PCI_ROOT=/var/lib/nvml-mock
EOF
}

# Append proc/IB/PCI mounts to nvidia.yaml before the hooks block.
append_full_tier_to_nvidia_cdi() {
  local outfile="$1"
  {
    cat <<'EOF'
    - hostPath: /var/lib/nvml-mock/driver/proc/driver/nvidia/version
      containerPath: /proc/driver/nvidia/version
      options: [ro, nosuid, nodev, bind]
    - hostPath: /var/lib/nvml-mock/driver/proc/driver/nvidia/params
      containerPath: /proc/driver/nvidia/params
      options: [ro, nosuid, nodev, bind]
EOF
    generate_mock_ib_cdi_edits
    generate_mock_pci_cdi_edits
  } >> "$outfile"
}

write_nvml_mock_cdi_spec() {
  local cdi_dir="$1" tier="$2" include_ib="$3" include_pci="$4"
  local preload ib_env pci_env
  preload=""
  ib_env=""
  pci_env=""
  if [ "$include_ib" = "1" ]; then
    preload="/usr/local/lib/libibmockumad.so.1:/usr/local/lib/libibmockverbs.so.1:/usr/local/lib/libibmocksys.so.1"
    ib_env='
    - MOCK_IB=full
    - MOCK_IB_ROOT=/var/lib/nvml-mock/ib
    - MOCK_IB_PING_SOCKET=/var/lib/nvml-mock/run/mock-ib.sock'
  fi
  if [ "$include_pci" = "1" ]; then
    preload="${preload:+$preload:}/usr/local/lib/libpcimocksys.so.1"
    pci_env='
    - MOCK_PCI_ROOT=/var/lib/nvml-mock'
  fi
  local ld_env=""
  [ -n "$preload" ] && ld_env="
    - LD_PRELOAD=$preload"

  cat > "$cdi_dir/nvml-mock-${tier}.yaml" <<EOF
cdiVersion: "0.6.0"
kind: "nvml-mock.nvidia.com/mock"
devices:
  - name: "${tier}"
    containerEdits:
      mounts:
$(generate_mock_nvml_cdi_edits)
$([ "$include_ib" = "1" ] && generate_mock_ib_cdi_edits)
$([ "$include_pci" = "1" ] && generate_mock_pci_cdi_edits)
      hooks:
        - hookName: createContainer
          path: /usr/bin/nvidia-cdi-hook
          args: [nvidia-cdi-hook, update-ldcache, --folder, /usr/lib64]
      env:
        - MOCK_NVML_CONFIG=/etc/nvml-mock/config.yaml${ld_env}${ib_env}${pci_env}
EOF
}
