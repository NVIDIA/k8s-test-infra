#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

set -eu

if [ "$#" -ne 3 ]; then
    echo "usage: $0 LOCK_FILE TARGETARCH OUTPUT_DIR" >&2
    exit 2
fi

lock_file=$1
target_arch=$2
output_dir=$3

# The lock file is maintained in this repository and contains only shell
# assignments. Sourcing it keeps the version, full paths, and checksums in one
# reviewable location without duplicating them as Docker build arguments.
# shellcheck disable=SC1090
. "${lock_file}"

case "${target_arch}" in
    amd64)
        relative_path=${IMEX_AMD64_PATH}
        expected_sha256=${IMEX_AMD64_SHA256}
        ;;
    arm64)
        relative_path=${IMEX_ARM64_PATH}
        expected_sha256=${IMEX_ARM64_SHA256}
        ;;
    *)
        echo "unsupported TARGETARCH: ${target_arch}" >&2
        exit 1
        ;;
esac

case "${expected_sha256}" in
    *[!0-9a-f]*)
        echo "invalid SHA256 for TARGETARCH ${target_arch}" >&2
        exit 1
        ;;
esac
if [ "${#expected_sha256}" -ne 64 ]; then
    echo "invalid SHA256 for TARGETARCH ${target_arch}" >&2
    exit 1
fi

redist_base_url=${IMEX_REDIST_BASE_URL:-https://developer.download.nvidia.com/compute/nvidia-driver/redist}
work_dir=$(mktemp -d)
archive=${work_dir}/imex.tar.xz
trap 'rm -rf -- "${work_dir}"' EXIT HUP INT TERM

curl --fail --location --silent --show-error \
    --output "${archive}" "${redist_base_url}/${relative_path}"
printf '%s  %s\n' "${expected_sha256}" "${archive}" | sha256sum --check -

mkdir -p "${output_dir}"
tar --extract --xz --file "${archive}" --directory "${output_dir}" \
    --strip-components=1

for required_file in \
    usr/bin/nvidia-imex \
    usr/bin/nvidia-imex-ctl \
    etc/nvidia-imex/config.cfg \
    LICENSE \
    usr/share/doc/third-party-notices.txt; do
    if [ ! -f "${output_dir}/${required_file}" ]; then
        echo "IMEX archive is missing ${required_file}" >&2
        exit 1
    fi
done
