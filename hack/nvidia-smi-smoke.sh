#!/usr/bin/env bash

# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

# Runs every nvidia-smi the nvml-mock image bundles through the nvidia-smi
# commands Mokka's e2e suite and guides rely on, against the image's own mock
# NVML on a profile whose driver is that binary's: the pairing the node agent
# stages. Needs only Docker, no cluster, so a binary that cannot drive the mock
# fails here on every architecture CI builds, before any leg spends a cluster
# on it.
#
# Usage: hack/nvidia-smi-smoke.sh [image]    (default nvml-mock:e2e)

set -euo pipefail

IMAGE=${1:-nvml-mock:e2e}
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# gb300 covers every surface the commands below touch: NVLink through switches,
# fabric, MIG (its 1g.35gb slice is named below) and persistence.
PROFILE="${ROOT}/pkg/gpu/mocknvml/configs/mock-nvml-config-gb300.yaml"
# Where the image bundles one nvidia-smi per driver version
# (deployments/nvml-mock/Dockerfile).
BUNDLED=/usr/local/libexec/nvml-mock/nvidia-smi

# Runs inside the image: each command must exit 0, and the binary must report
# the version its directory is named after.
CHECKS='
set -u
failed=0
run() {
  if "$SMI" "$@" >/tmp/out 2>&1; then
    printf "  ok    nvidia-smi %s\n" "$*"
  else
    printf "  FAIL  nvidia-smi %s (exit %s)\n" "$*" "$?"
    sed "s/^/        /" /tmp/out | head -n 20
    failed=1
  fi
}
"$SMI" | grep -q "NVIDIA-SMI $WANT " || { echo "  FAIL  the banner does not report $WANT"; failed=1; }
run -L
run -q
run -q -x
run topo -m
run nvlink -s -i 0
run nvlink --info -i 0
run --query-gpu=name,pci.bus_id,memory.total --format=csv
run -pm 0 -i 0
run -pm 1 -i 0
run -r -i 0
run -i 0 -mig 1
run mig -i 0 -cgi 1g.35gb -C
run mig -lgi
run mig -i 0 -dci -gi 0 -ci 0
run mig -i 0 -dgi -gi 0
run -i 0 -mig 0
run -B
run vgpu
exit $failed
'

versions=$(docker run --rm --entrypoint ls "${IMAGE}" "${BUNDLED}")
[ -n "${versions}" ] || { echo "${IMAGE} bundles no nvidia-smi under ${BUNDLED}" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT

status=0
for version in ${versions}; do
  echo "nvidia-smi ${version}"
  # The mock writes its overrides beside the config, so each binary gets a
  # fresh, writable copy of the profile on its own driver.
  dir="${work}/${version}"
  mkdir -p "${dir}"
  sed -E "s/^(  driver_version: ).*/\1\"${version}\"/" "${PROFILE}" >"${dir}/config.yaml"
  cp "${PROFILE%.yaml}.mig.yaml" "${dir}/config.mig.yaml"
  docker run --rm -v "${dir}:/cfg" -e MOCK_NVML_CONFIG=/cfg/config.yaml \
    -e SMI="${BUNDLED}/${version}/nvidia-smi" -e WANT="${version}" \
    --entrypoint sh "${IMAGE}" -c "${CHECKS}" || status=1
done
exit "${status}"
