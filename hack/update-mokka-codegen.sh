#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module="github.com/NVIDIA/k8s-test-infra"
api_package="${module}/pkg/apis/mokka/v1alpha1"
generated_package="${module}/pkg/generated"
header="${repo_root}/hack/boilerplate.go.txt"
yaml_header="${repo_root}/hack/boilerplate.yaml.txt"
crd_dir="${repo_root}/deployments/mokka-crds/helm/mokka-crds/crds"
tool_dir="$(mktemp -d)"

trap 'rm -rf "${tool_dir}"' EXIT

GOBIN="${tool_dir}" GOFLAGS=-mod=mod go install k8s.io/code-generator/cmd/deepcopy-gen@v0.36.3
GOBIN="${tool_dir}" GOFLAGS=-mod=mod go install k8s.io/code-generator/cmd/client-gen@v0.36.3
GOBIN="${tool_dir}" GOFLAGS=-mod=mod go install k8s.io/code-generator/cmd/lister-gen@v0.36.3
GOBIN="${tool_dir}" GOFLAGS=-mod=mod go install k8s.io/code-generator/cmd/informer-gen@v0.36.3
GOBIN="${tool_dir}" GOFLAGS=-mod=mod go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0

export GOFLAGS=-mod=mod

rm -rf "${repo_root}/pkg/generated"
mkdir -p "${crd_dir}"
find "${crd_dir}" -maxdepth 1 -type f -name '*.yaml' -delete

"${tool_dir}/deepcopy-gen" \
	--go-header-file "${header}" \
	--output-file zz_generated.deepcopy.go \
	"${api_package}"

"${tool_dir}/client-gen" \
	--go-header-file "${header}" \
	--clientset-name versioned \
	--input-base "${module}/pkg/apis" \
	--input mokka/v1alpha1 \
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

"${tool_dir}/controller-gen" \
	crd:headerFile="${yaml_header}",maxDescLen=0 \
	paths="${api_package}" \
	output:crd:dir="${crd_dir}"

gofmt -w "${repo_root}/pkg/apis/mokka/v1alpha1/zz_generated.deepcopy.go" "${repo_root}/pkg/generated"
