# Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
SHELL := /usr/bin/env bash
# NOTE: GNU Make only honours .SHELLFLAGS from 3.82 onward. macOS ships 3.81,
# which treats the line below as an ordinary variable and runs recipes with a
# bare `-c`. Any recipe whose exit status depends on these flags must set them
# itself — see the `e2e` and `.mod-verify` targets.
.SHELLFLAGS := -o pipefail -ec

.PHONY: build fmt verify release lint vendor check-vendor helm-unittest e2e e2e-dra e2e-gpu-operator e2e-multi-node e2e-nri e2e-nfd

GO_CMD ?= go
GO_FMT ?= gofmt
GO_SRC := $(shell find . -type f -name '*.go' -not -path "./vendor/*")
# First-party Go source directories. gofumpt / golangci-lint walk these
# rather than "." so they don't dive into vendor/ or tmp/ (which may hold an
# untracked clone of a sibling repo — e.g. tmp/topograph/).
GO_PKG_DIRS := cmd pkg tests

VERSION := 0.0.1

IMAGE_REGISTRY ?= ghcr.io/nvidia
IMAGE_TAG_NAME ?= $(VERSION)
IMAGE_NAME := k8s-test-infra
IMAGE_REPO := $(IMAGE_REGISTRY)/$(IMAGE_NAME)
IMAGE_TAG := $(IMAGE_REPO):$(IMAGE_TAG_NAME)

PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

BIN_DIR=$(PWD)/tmp/bin

.PHONY: help
help:
	@echo "🛠️ Dev Commands\n"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: tools
tools: ## Install static checkers & other binaries
	@echo "🚚 Downloading tools.."
	@GOBIN=$(BIN_DIR) go install mvdan.cc/gofumpt@latest
	@GOBIN=$(BIN_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@GOBIN=$(BIN_DIR) go install github.com/denis-tingaikin/go-header/cmd/go-header@latest
	@GOBIN=$(BIN_DIR) go install github.com/goreleaser/goreleaser/v2@latest

.PHONY: lint
lint: tools ## Lint the source code (read-only checks)
	@echo "🎨 Checking format.."
	@out="$$($(BIN_DIR)/gofumpt -l $(GO_PKG_DIRS))"; \
	if [ -n "$$out" ]; then \
	    echo "gofumpt found unformatted files (run 'make lint-fix' to fix):"; \
	    echo "$$out"; \
	    exit 1; \
	fi
	@echo "🧹 Vetting.."
	@go vet ./...
	@echo "🧹 GoCI Lint.."
	@$(BIN_DIR)/golangci-lint run ./...
	@echo "🧹 GoReleaser check.."
	@$(BIN_DIR)/goreleaser check

.PHONY: lint-fix
lint-fix: tools ## Same checks as `lint`, but auto-fix what can be fixed; report the rest
	@echo "🧹 Tidying go.mod.."
	@go mod tidy
	@echo "🎨 Formatting with gofumpt.."
	@$(BIN_DIR)/gofumpt -l -w $(GO_PKG_DIRS)
	@echo "🔧 golangci-lint --fix.."
	@$(BIN_DIR)/golangci-lint run --fix ./...
	@echo "🧹 Vetting (report-only).."
	@go vet ./...
	@echo "🧹 GoReleaser check (report-only).."
	@$(BIN_DIR)/goreleaser check

.PHONY: gen
gen: ## Generate NVML Bridge
	@echo "Generate NVML Bridge.."
	@go generate ./pkg/gpu/mocknvml/bridge/...

BIN_OUT ?= bin

.PHONY: build
build: ## Build every CLI under cmd/ into $(BIN_OUT)/
	@rm -rf $(BIN_OUT)
	@mkdir -p $(BIN_OUT)
	@for pkg in $$(find ./cmd -type f -name main.go -exec dirname {} \; | sort -u); do \
	    name=$$(basename $$pkg); \
	    parent=$$(basename $$(dirname $$pkg)); \
	    if [ "$$parent" != "cmd" ]; then name=$$parent-$$name; fi; \
	    echo "🔨 $$name"; \
	    $(GO_CMD) build -mod=vendor -o $(BIN_OUT)/$$name $$pkg; \
	done

.PHONY: vendor
vendor: ## Refresh top-level go.mod / vendor / verify
	go mod tidy
	go mod vendor
	go mod verify

.PHONY: vendor-check
vendor-check: vendor ## Fail if go.mod / go.sum / vendor are out of sync with HEAD
	git diff --quiet HEAD -- go.mod go.sum vendor

.PHONY: modules
modules: | .mod-tidy .mod-vendor .mod-verify ## Tidy / vendor / verify every sub-module
.mod-tidy:
	@for mod in $$(find . -name go.mod -not -path "./testdata/*" -not -path "./third_party/*"); do \
	    echo "Tidying $$mod..."; ( \
	        cd $$(dirname $$mod) && go mod tidy \
            ) || exit 1; \
	done

.mod-vendor:
	@for mod in $$(find . -name go.mod -not -path "./testdata/*" -not -path "./third_party/*" -not -path "./deployments/*"); do \
		echo "Vendoring $$mod..."; ( \
			cd $$(dirname $$mod) && go mod vendor \
			) || exit 1; \
	done

.mod-verify:
	@for mod in $$(find . -name go.mod -not -path "./testdata/*" -not -path "./third_party/*"); do \
	    echo "Verifying $$mod..."; ( \
	        set -o pipefail; cd $$(dirname $$mod) && go mod verify | sed 's/^/  /g' \
	    ) || exit 1; \
	done

.PHONY: modules-check
modules-check: modules ## Fail if any sub-module go.mod / go.sum / vendor is out of sync
	@echo "- Checking if go.mod and go.sum are in sync..."
	@git diff --exit-code -- $$(find . \( -name go.mod -o -name go.sum \))
	@echo "- Checking if the go mod vendor dir is in sync..."
	@git diff --exit-code -- $$(find . -name vendor)

HELM_CHART_DIR := deployments/nvml-mock/helm/nvml-mock

.PHONY: helm-unittest
helm-unittest: ## Run the nvml-mock chart unit test suite
	helm unittest $(HELM_CHART_DIR)

# Unit tests for the e2e harness itself (framework/*). They are behind the `e2e`
# build tag, so the untagged CI unit-test run skips them, and `make e2e` targets
# only the Ginkgo suite package ./tests/e2e/go -- neither reaches these. They
# need no cluster and no kubectl: the one that shells out substitutes a stub on
# PATH.
.PHONY: test-e2e-framework
test-e2e-framework:
	$(GO_CMD) test -tags e2e -race ./tests/e2e/go/framework/...

.PHONY: generate
generate:
	go generate ./pkg/gpu/mocknvml/bridge/...

# Drives the built libnvidia-ml.so through go-nvml over the real C ABI.
# Docker-based, hence separate from the `go test` run.
.PHONY: test-mocknvml-bridge
test-mocknvml-bridge:
	$(MAKE) -C tests/mocknvml test

KIND_NODE_IMAGE   ?= kind-node-nv:latest
# Cluster profile (select via PROFILE=<name>):
#   - PROFILE=default (default)  local/kind/default.kind.yaml        (1 CP + 2 workers labelled a100 / t4)
#   - PROFILE=compute-domain     local/kind/compute-domain.kind.yaml (1 CP + 4 workers labelled clique 0 / 1)
# Consumer selection (--gpu-operator / --dra / --fgo / --multi-gpu-profile)
# happens in the Tiltfile — the default cluster shape supports every consumer
# scenario without a rebuild.
# compute-domain also changes the cluster name because topology.yaml
# hardcodes worker names as <cluster-name>-worker[N] — see
# local/compute-domain/topology.yaml and local/kind/compute-domain.kind.yaml.
# Note: distinct from Tilt's --gpu-profile (a100|gb200|...) — this PROFILE
# picks the Kind cluster topology; --gpu-profile picks the simulated GPU.
PROFILE ?= default
_VALID_PROFILES := default compute-domain
ifeq ($(filter $(PROFILE),$(_VALID_PROFILES)),)
$(error PROFILE=$(PROFILE) is not valid. Choose one of: $(_VALID_PROFILES))
endif
KIND_CLUSTER_NAME   ?= $(if $(filter compute-domain,$(PROFILE)),mokka-compute-domain,mokka)
KIND_CLUSTER_CONFIG ?= local/kind/$(PROFILE).kind.yaml

.PHONY: image-kind-node cluster-create cluster-delete
# KIND_NODE_IMAGE_PREBUILT (env, any non-empty value): skip the local docker
# build and use the pre-built $(KIND_NODE_IMAGE) already loaded in the local
# daemon. Verify with `docker image inspect` before skipping, so a botched
# staging step fails here (with a clear message) instead of surfacing later
# as an opaque `kind create cluster` pull error. CI sets this after
# `docker pull` + `docker tag` of the pre-built image from ttl.sh (see
# .github/workflows/nvml-mock-e2e-go.yaml build-kind-node job); local devs
# leave it unset and get the rebuild-when-Dockerfile-changes behavior.
image-kind-node:
	@if [ -n "$$KIND_NODE_IMAGE_PREBUILT" ]; then \
		docker image inspect $(KIND_NODE_IMAGE) >/dev/null 2>&1 || { \
			echo "ERROR: KIND_NODE_IMAGE_PREBUILT is set but $(KIND_NODE_IMAGE) is not in the local docker daemon."; \
			echo "       Ensure a preceding step ran: docker pull <ref> && docker tag <ref> $(KIND_NODE_IMAGE)"; \
			echo "       (Or unset KIND_NODE_IMAGE_PREBUILT to build it locally.)"; \
			exit 1; \
		}; \
		echo "Using pre-built $(KIND_NODE_IMAGE) already present locally"; \
	else \
		docker build -t $(KIND_NODE_IMAGE) ./deployments/kind-nvidia-cdi; \
	fi

cluster-create: image-kind-node
	@kind create cluster --name $(KIND_CLUSTER_NAME) --image $(KIND_NODE_IMAGE) --config $(KIND_CLUSTER_CONFIG)

cluster-delete:
	@kind delete cluster --name $(KIND_CLUSTER_NAME)

# ---------------------------------------------------------------------------
# Go end-to-end suite (tests/e2e) — the Go port of docs/demo/standalone/demo.sh.
# One entrypoint for local + CI: the harness owns the full lifecycle (Kind
# create/teardown, image build/load, Helm upgrade --install, validation,
# diagnostics). A SINGLE shared multi-node cluster is created once and every
# selected profile runs against it (profile switch = `helm upgrade`, not a
# cluster rebuild). Defaults to gb200; scope with E2E_PROFILES /
# E2E_GINKGO_FLAGS. Examples:
#   make e2e                       # gb200
#   make e2e E2E_PROFILES=a100     # fast inner loop, single profile
#   make e2e E2E_GINKGO_FLAGS='--label-filter="nvidia-smi || nvlink"'
#   make e2e-dra                   # DRA scenario
#   make e2e-gpu-operator          # GPU Operator scenario
#   make e2e-multi-node            # heterogeneous A100/T4 multi-node scenario
#   make e2e-nri                   # node-wide NRI ambient-injection scenario
#   make e2e-nfd                   # NFD label-provenance scenario
# CI builds the image once per job and sets E2E_SKIP_BUILD=true + E2E_IMAGE.
#
# NOTE: this targets ./tests/e2e/go (the Ginkgo suite package) only, NOT
# ./tests/e2e/go/... — the subpackages (profile, ibutil) hold plain `go test`
# unit tests (e.g. the profile drift-guard oracle, which always checks ALL
# profiles regardless of E2E_PROFILES). Those run in the normal unit-test/CI
# path; keeping them out of `make e2e` means the output reflects only the
# E2E_PROFILES-scoped cluster suite.
# ---------------------------------------------------------------------------
GINKGO ?= $(GO_CMD) run github.com/onsi/ginkgo/v2/ginkgo
E2E_TIMEOUT ?= 90m
E2E_DEFAULT_LABEL_FILTER ?= !validator && !dra && !gpu-operator && !multi-node && !nri && !nfd
E2E_GINKGO_FLAGS ?= --label-filter='$(E2E_DEFAULT_LABEL_FILTER)'

.PHONY: e2e e2e-dra e2e-gpu-operator e2e-multi-node e2e-nri e2e-nfd

# `set -o pipefail` is inline on purpose; do not drop it as redundant with
# .SHELLFLAGS. GNU Make ignores .SHELLFLAGS before 3.82 and macOS ships 3.81,
# so on a developer machine this recipe otherwise returns tee's status and a
# failed suite exits 0 — see issue #560 and tests/makefile/makefile_test.go.
e2e: ## Run the Ginkgo e2e suite (scope with E2E_PROFILES / E2E_GINKGO_FLAGS)
	set -o pipefail; $(GINKGO) --tags=e2e -v --timeout=$(E2E_TIMEOUT) $(E2E_GINKGO_FLAGS) ./tests/e2e/go | tee e2e.log

e2e-dra: ## e2e — DRA scenario
	$(MAKE) e2e E2E_GINKGO_FLAGS='--label-filter=dra'

e2e-gpu-operator: ## e2e — GPU Operator scenario
	$(MAKE) e2e E2E_GINKGO_FLAGS='--label-filter=gpu-operator'

e2e-multi-node: ## e2e — heterogeneous a100/t4 cluster
	$(MAKE) e2e E2E_PROFILES=a100,t4 E2E_GINKGO_FLAGS='--label-filter=multi-node'

e2e-nri: ## e2e — NRI ambient-injection scenario
	$(MAKE) e2e E2E_GINKGO_FLAGS='--label-filter=nri'

# E2E_PROFILES is pinned rather than inherited from DefaultProfiles. The nfd
# spec hardcodes a100 (scenario_nfd_test.go), because the PCI vendor label is
# vendor-only and byte-identical across profiles. Without this the harness
# inherits gb200 and announces a profile the run never instantiates — a green
# log then reads exactly like one that did exercise gb200.
e2e-nfd: ## e2e — NFD label-provenance scenario (pinned to a100)
	$(MAKE) e2e E2E_PROFILES=a100 E2E_GINKGO_FLAGS='--label-filter=nfd'
