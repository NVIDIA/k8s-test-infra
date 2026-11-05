#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module="github.com/NVIDIA/k8s-test-infra"
api_package="${module}/api/v1alpha1"
generated_package="${module}/pkg/generated"
header="${repo_root}/hack/boilerplate.go.txt"
tool_dir="$(mktemp -d)"

trap 'rm -rf "${tool_dir}"' EXIT

# The generator versions are pinned in hack/codegen/go.mod.
for tool in client-gen lister-gen informer-gen; do
	GOBIN="${tool_dir}" go -C "${repo_root}/hack/codegen" install "k8s.io/code-generator/cmd/${tool}"
done

export GOFLAGS=-mod=mod

rm -rf "${repo_root}/pkg/generated"

"${tool_dir}/client-gen" \
	--go-header-file "${header}" \
	--clientset-name versioned \
	--input-base "${module}" \
	--input api/v1alpha1 \
	--output-dir "${repo_root}/pkg/generated/clientset" \
	--output-pkg "${generated_package}/clientset"

"${tool_dir}/lister-gen" \
	--go-header-file "${header}" \
	--output-dir "${repo_root}/pkg/generated/listers" \
	--output-pkg "${generated_package}/listers" \
	"${api_package}"

"${tool_dir}/informer-gen" \
	--go-header-file "${header}" \
	--versioned-clientset-package "${generated_package}/clientset/versioned" \
	--listers-package "${generated_package}/listers" \
	--output-dir "${repo_root}/pkg/generated/informers" \
	--output-pkg "${generated_package}/informers" \
	"${api_package}"

gofmt -w "${repo_root}/pkg/generated"
