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
.SHELLFLAGS := -o pipefail -ec

GO_CMD ?= go
GO_SRC := $(shell find . -type f -name '*.go')
# First-party Go source directories. gofumpt / golangci-lint walk these
# rather than "." so they don't dive into tmp/ (which may hold an untracked
# clone of a sibling repo — e.g. tmp/topograph/).
GO_PKG_DIRS := cmd pkg tests

BIN_DIR=$(PWD)/tmp/bin

VERSION := 0.0.1

VERSION_PACKAGE := github.com/NVIDIA/k8s-test-infra/internal/version
COMMIT ?= $(shell git describe --dirty --long --always --abbrev=15 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS_COMMON := "-X $(VERSION_PACKAGE).Version=$(VERSION) -X $(VERSION_PACKAGE).GitCommit=$(COMMIT) -X $(VERSION_PACKAGE).BuildDate=$(BUILD_DATE)"

IMAGE_REGISTRY ?= ghcr.io/nvidia
IMAGE_TAG_NAME ?= $(VERSION)
IMAGE_NAME := k8s-test-infra
IMAGE_REPO := $(IMAGE_REGISTRY)/$(IMAGE_NAME)
IMAGE_TAG := $(IMAGE_REPO):$(IMAGE_TAG_NAME)

# Mokka control-plane publication inputs. The image repository and newline-
# delimited tags are intentionally required at runtime: publishing must never
# fall back to a developer-oriented image name. Labels are newline-delimited
# too, matching docker/metadata-action output without shell word splitting.
MOKKA_CONTROL_PLANE_IMAGE ?=
MOKKA_CONTROL_PLANE_TAGS ?=
MOKKA_CONTROL_PLANE_LABELS ?=
MOKKA_CONTROL_PLANE_CONTEXT ?= .
MOKKA_CONTROL_PLANE_DOCKERFILE ?= deployments/control-plane/Dockerfile
MOKKA_CONTROL_PLANE_PLATFORMS ?= linux/amd64,linux/arm64
MOKKA_CONTROL_PLANE_CACHE_FROM ?=
MOKKA_CONTROL_PLANE_CACHE_TO ?=
MOKKA_CONTROL_PLANE_GOPROXY ?=
MOKKA_CONTROL_PLANE_GOAUTH_FILE ?=
MOKKA_CONTROL_PLANE_DIGEST_FILE ?= dist/mokka-control-plane.digest
MOKKA_CONTROL_PLANE_SBOM_FILE ?= dist/mokka-control-plane.spdx.json
MOKKA_CONTROL_PLANE_DOCKER ?= docker
MOKKA_CONTROL_PLANE_SYFT ?= syft
MOKKA_CONTROL_PLANE_COSIGN ?= cosign

export MOKKA_CONTROL_PLANE_IMAGE MOKKA_CONTROL_PLANE_TAGS MOKKA_CONTROL_PLANE_LABELS
export MOKKA_CONTROL_PLANE_CONTEXT MOKKA_CONTROL_PLANE_DOCKERFILE MOKKA_CONTROL_PLANE_PLATFORMS
export MOKKA_CONTROL_PLANE_CACHE_FROM MOKKA_CONTROL_PLANE_CACHE_TO
export MOKKA_CONTROL_PLANE_GOPROXY MOKKA_CONTROL_PLANE_GOAUTH_FILE
export MOKKA_CONTROL_PLANE_DIGEST_FILE MOKKA_CONTROL_PLANE_SBOM_FILE
export MOKKA_CONTROL_PLANE_DOCKER MOKKA_CONTROL_PLANE_SYFT MOKKA_CONTROL_PLANE_COSIGN

PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

BIN_DIR := $(PROJECT_DIR)/tmp/bin
GOBIN := $(BIN_DIR)

export GOBIN

.PHONY: help
help:
	@echo "🛠️ Dev Commands\n"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: test-kwok-contract kwok-scale kwok-scale-matrix
test-kwok-contract:
	$(GO_CMD) test -race ./tests/kwok/...

# Explicit opt-in: this owns a Docker-backed KWOK cluster and can consume
# substantial host resources at the larger tiers. It is not part of unit CI.
KWOK_NODE_COUNT ?= 200
KWOK_MATRIX ?= 1000 10000 25000 50000 100000
kwok-scale:
	@test "$(KWOK_SCALE)" = 1 || { echo "set KWOK_SCALE=1 to run the opt-in KWOK POC"; exit 2; }
	KWOK_NODE_COUNT=$(KWOK_NODE_COUNT) ./tests/kwok/run.sh

kwok-scale-matrix:
	@test "$(KWOK_SCALE)" = 1 || { echo "set KWOK_SCALE=1 to run the opt-in KWOK POC"; exit 2; }
	@for count in $(KWOK_MATRIX); do \
		KWOK_NODE_COUNT=$$count KWOK_CLUSTER_NAME=mokka-controller-kwok-$$count ./tests/kwok/run.sh || exit; \
	done

CONTROLLER_GEN_VERSION ?= v0.20.1

.PHONY: tools
tools: ## Install static checkers & other binaries
	@echo "🚚 Downloading tools.."
	@mkdir -p $(GOBIN)
	@ \
	test -x $(BIN_DIR)/golangci-lint || curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(BIN_DIR) v2.12.2 & \
	test -x $(BIN_DIR)/govulncheck || go install golang.org/x/vuln/cmd/govulncheck@latest & \
	test -x $(BIN_DIR)/controller-gen || go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION) & \
	wait

.PHONY: lint
lint: tools gen-check ## Lint the source code
	@echo "🧹 Tidying go.mod.."
	@go mod tidy
	@git diff --exit-code -- go.mod go.sum || { \
		echo "ERROR: go.mod / go.sum are out of sync. Run 'make lint-fix' and commit the result."; \
		exit 1; }
	@echo "🧹 Checking that no vendor directory has reappeared.."
	@found=$$(find . -type d -name vendor -not -path "./tmp/*"); \
	if [ -n "$$found" ]; then \
		echo "ERROR: dependencies resolve through the Go proxy; a vendor directory switches every build back to vendor mode silently:"; \
		echo "$$found"; \
		exit 1; \
	fi
	@echo "🛡️ Verifying the vendored Go proxy action.."
	@./hack/check-depproxy-kit.sh
	@echo "🧹 Vetting.."
	@go vet ./...
	@echo "🧹 GoCI Lint.."
	@$(BIN_DIR)/golangci-lint run ./...
	@echo "🛡️ govulncheck.."
	@$(BIN_DIR)/govulncheck -tags=e2e,integration ./...

.PHONY: lint-fix
lint-fix: tools gen ## Same checks as `lint`, but auto-fix what can be fixed; report the rest
	@echo "🔧 Tidying go.mod.."
	@go mod tidy
	@echo "🔧 golangci-lint --fix.."
	@$(BIN_DIR)/golangci-lint run --fix ./...
	@echo "🧹 Vetting.."
	@go vet ./...
	@echo "🛡️ govulncheck.."
	@$(BIN_DIR)/govulncheck -tags=e2e,integration ./...

.PHONY: deps-verify
deps-verify: ## Re-download every module into a throwaway cache and re-verify it against go.sum
	@echo "🛡️ Verifying modules against go.sum.."
	@cache=$$(mktemp -d); \
	GOMODCACHE="$$cache" GOFLAGS=-mod=readonly go mod download; \
	status=$$?; \
	GOMODCACHE="$$cache" go clean -modcache >/dev/null 2>&1 || true; \
	exit $$status

CRDS_OUT     := deployments/mokka-crds/helm/mokka-crds/templates
API_PKG_PATH := ./internal/controlplane/api/...
# The bridge generator reads go-nvml's nvml.go and nvml.h. Their module cache path
# is version-stamped, so resolve it here instead of hardcoding the pinned version.
GO_NVML_MOD  := github.com/NVIDIA/go-nvml
MOKKA_CODEGEN := ./hack/update-mokka-codegen.sh

.PHONY: gen
gen: tools ## Generate machine-controlled code
	@echo "Generating NVML Bridge.."
	@go mod download $(GO_NVML_MOD)
	@GO_NVML_DIR=$$(go list -m -f '{{.Dir}}' $(GO_NVML_MOD)) go generate ./pkg/gpu/mocknvml/bridge/...
	@echo "Generating deepcopy for $(API_PKG_PATH).."
	@$(BIN_DIR)/controller-gen object paths="$(API_PKG_PATH)"
	@echo "Generating CRD manifests into $(CRDS_OUT).."
	@mkdir -p $(CRDS_OUT)
	@$(BIN_DIR)/controller-gen crd:allowDangerousTypes=true \
		paths="$(API_PKG_PATH)" \
		output:crd:artifacts:config=$(CRDS_OUT)
	@echo "Generating Mokka clients, listers, and informers.."
	@$(MOKKA_CODEGEN)

.PHONY: gen-check
gen-check: gen ## Check whether all generated code is up to date
	@git diff --quiet HEAD -- \
		./pkg/gpu/mocknvml/bridge/ \
		./internal/controlplane/api/ \
		./pkg/generated/ \
		$(CRDS_OUT) || { \
		echo "ERROR: generated code is out of date. Run 'make gen' and commit the result."; \
		git diff -- ./internal/controlplane/api/ $(CRDS_OUT); \
		exit 1; }

DIST_DIR ?= dist

.PHONY: build
build: ## Build all CLIs
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)
	@echo "Building CLI.."
	@for pkg in $$(find ./cmd -type f -name main.go -exec dirname {} \; | sort -u); do \
	    name=$$(basename $$pkg); \
	    parent=$$(basename $$(dirname $$pkg)); \
	    if [ "$$parent" != "cmd" ]; then name=$$parent-$$name; fi; \
	    echo "🔨 $$name"; \
	    $(GO_CMD) build -o $(DIST_DIR)/$$name $$pkg || exit 1; \
	done
	@echo "Building Golang shims.."
	@for pkg in $$(find ./shims -type f -name main.go -exec dirname {} \; | sort -u); do \
	    name=$$(basename $$pkg); \
	    echo "🔨 $$name"; \
	    $(GO_CMD) build -o $(DIST_DIR)/$$name $$pkg || exit 1; \
	done

.PHONY: mokka-control-plane-publish mokka-control-plane-image-push
.PHONY: mokka-control-plane-image-sign mokka-control-plane-image-sbom
.PHONY: mokka-control-plane-image-attest

# Keep the four operations independently runnable so a local release can be
# resumed from an already-pushed digest. The aggregate target deliberately
# sequences recursive makes even under `make -j`: attestation must never race
# the digest, signature, or SBOM it binds together.
mokka-control-plane-publish: ## Build, push, sign, generate an SPDX SBOM, and attest the Mokka control-plane image
	+@$(MAKE) mokka-control-plane-image-push
	+@$(MAKE) mokka-control-plane-image-sign
	+@$(MAKE) mokka-control-plane-image-sbom
	+@$(MAKE) mokka-control-plane-image-attest

mokka-control-plane-image-push: ## Build and push the multi-platform Mokka control-plane image, recording its digest
	@set -eu; \
	image="$${MOKKA_CONTROL_PLANE_IMAGE:-}"; \
	tags="$${MOKKA_CONTROL_PLANE_TAGS:-}"; \
	labels="$${MOKKA_CONTROL_PLANE_LABELS:-}"; \
	context="$${MOKKA_CONTROL_PLANE_CONTEXT:-}"; \
	dockerfile="$${MOKKA_CONTROL_PLANE_DOCKERFILE:-}"; \
	platforms="$${MOKKA_CONTROL_PLANE_PLATFORMS:-}"; \
	digest_file="$${MOKKA_CONTROL_PLANE_DIGEST_FILE:-}"; \
	docker_cmd="$${MOKKA_CONTROL_PLANE_DOCKER:-}"; \
	goproxy="$${MOKKA_CONTROL_PLANE_GOPROXY:-}"; \
	goauth_file="$${MOKKA_CONTROL_PLANE_GOAUTH_FILE:-}"; \
	if [[ -z "$$image" || -z "$$tags" || -z "$$context" || -z "$$dockerfile" || -z "$$platforms" || -z "$$digest_file" || -z "$$docker_cmd" ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_IMAGE, MOKKA_CONTROL_PLANE_TAGS, MOKKA_CONTROL_PLANE_CONTEXT, MOKKA_CONTROL_PLANE_DOCKERFILE, MOKKA_CONTROL_PLANE_PLATFORMS, MOKKA_CONTROL_PLANE_DIGEST_FILE, and MOKKA_CONTROL_PLANE_DOCKER are required." >&2; \
		exit 2; \
	fi; \
	image_name="$${image##*/}"; \
	if [[ "$$image" == *"@"* || "$$image_name" == *":"* || "$$image" =~ [[:space:]] ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_IMAGE must be an untagged repository without whitespace: $$image" >&2; \
		exit 2; \
	fi; \
	command -v "$$docker_cmd" >/dev/null 2>&1 || { echo "ERROR: required buildx frontend not found: $$docker_cmd" >&2; exit 2; }; \
	build_args=(buildx build --file "$$dockerfile" --platform "$$platforms" --push); \
	if [[ -n "$$goproxy" ]]; then \
		build_args+=(--build-arg "GOPROXY=$$goproxy"); \
	fi; \
	if [[ -n "$$goauth_file" ]]; then \
		[[ -f "$$goauth_file" ]] || { echo "ERROR: Go proxy credential file not found: $$goauth_file" >&2; exit 2; }; \
		build_args+=(--secret "id=goauth,src=$$goauth_file"); \
	fi; \
	tag_count=0; \
	while IFS= read -r tag; do \
		[[ -z "$$tag" ]] && continue; \
		if [[ "$$tag" != "$$image":* || "$$tag" =~ [[:space:]] ]]; then \
			echo "ERROR: every MOKKA_CONTROL_PLANE_TAGS entry must tag $$image without whitespace: $$tag" >&2; \
			exit 2; \
		fi; \
		build_args+=(--tag "$$tag"); \
		tag_count=$$((tag_count + 1)); \
	done <<< "$$tags"; \
	if [[ $$tag_count -eq 0 ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_TAGS must contain at least one non-empty tag." >&2; \
		exit 2; \
	fi; \
	while IFS= read -r label; do \
		[[ -z "$$label" ]] && continue; \
		build_args+=(--label "$$label"); \
	done <<< "$$labels"; \
	if [[ -n "$${MOKKA_CONTROL_PLANE_CACHE_FROM:-}" ]]; then \
		build_args+=(--cache-from "$$MOKKA_CONTROL_PLANE_CACHE_FROM"); \
	fi; \
	if [[ -n "$${MOKKA_CONTROL_PLANE_CACHE_TO:-}" ]]; then \
		build_args+=(--cache-to "$$MOKKA_CONTROL_PLANE_CACHE_TO"); \
	fi; \
	mkdir -p "$$(dirname "$$digest_file")"; \
	digest_tmp_dir="$$(mktemp -d "$$digest_file.tmp.XXXXXX")"; \
	digest_tmp="$$digest_tmp_dir/digest"; \
	trap 'rm -f -- "$$digest_tmp"; rmdir "$$digest_tmp_dir" 2>/dev/null || true' EXIT; \
	build_args+=(--iidfile "$$digest_tmp"); \
	"$$docker_cmd" "$${build_args[@]}" "$$context"; \
	digest="$$(cat "$$digest_tmp")"; \
	digest_raw="$$(cat "$$digest_tmp"; printf x)"; \
	if [[ ! "$$digest" =~ ^sha256:[0-9a-f]{64}$$ ]] || { [[ "$$digest_raw" != "$${digest}x" ]] && [[ "$$digest_raw" != "$$digest"$$'\n'x ]]; }; then \
		echo "ERROR: buildx did not produce one immutable sha256 digest in $$digest_tmp" >&2; \
		exit 1; \
	fi; \
	mv "$$digest_tmp" "$$digest_file"; \
	rmdir "$$digest_tmp_dir"; \
	trap - EXIT; \
	echo "Published $$image@$$digest"

mokka-control-plane-image-sign: ## Sign the Mokka control-plane image named by the recorded digest
	@set -eu; \
	image="$${MOKKA_CONTROL_PLANE_IMAGE:-}"; \
	digest_file="$${MOKKA_CONTROL_PLANE_DIGEST_FILE:-}"; \
	cosign_cmd="$${MOKKA_CONTROL_PLANE_COSIGN:-}"; \
	if [[ -z "$$image" || -z "$$digest_file" || -z "$$cosign_cmd" ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_IMAGE, MOKKA_CONTROL_PLANE_DIGEST_FILE, and MOKKA_CONTROL_PLANE_COSIGN are required." >&2; \
		exit 2; \
	fi; \
	image_name="$${image##*/}"; \
	if [[ "$$image" == *"@"* || "$$image_name" == *":"* || "$$image" =~ [[:space:]] ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_IMAGE must be an untagged repository without whitespace: $$image" >&2; \
		exit 2; \
	fi; \
	command -v "$$cosign_cmd" >/dev/null 2>&1 || { echo "ERROR: required signing tool not found: $$cosign_cmd" >&2; exit 2; }; \
	[[ -f "$$digest_file" ]] || { echo "ERROR: digest file not found: $$digest_file" >&2; exit 2; }; \
	digest="$$(cat "$$digest_file")"; \
	digest_raw="$$(cat "$$digest_file"; printf x)"; \
	if [[ ! "$$digest" =~ ^sha256:[0-9a-f]{64}$$ ]] || { [[ "$$digest_raw" != "$${digest}x" ]] && [[ "$$digest_raw" != "$$digest"$$'\n'x ]]; }; then \
		echo "ERROR: $$digest_file must contain exactly one immutable sha256 digest." >&2; \
		exit 2; \
	fi; \
	"$$cosign_cmd" sign --yes "$$image@$$digest"

mokka-control-plane-image-sbom: ## Generate an SPDX JSON SBOM for each platform in the recorded Mokka control-plane image
	@set -eu; \
	image="$${MOKKA_CONTROL_PLANE_IMAGE:-}"; \
	platforms="$${MOKKA_CONTROL_PLANE_PLATFORMS:-}"; \
	digest_file="$${MOKKA_CONTROL_PLANE_DIGEST_FILE:-}"; \
	sbom_file="$${MOKKA_CONTROL_PLANE_SBOM_FILE:-}"; \
	docker_cmd="$${MOKKA_CONTROL_PLANE_DOCKER:-}"; \
	syft_cmd="$${MOKKA_CONTROL_PLANE_SYFT:-}"; \
	if [[ -z "$$image" || -z "$$platforms" || -z "$$digest_file" || -z "$$sbom_file" || -z "$$docker_cmd" || -z "$$syft_cmd" ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_IMAGE, MOKKA_CONTROL_PLANE_PLATFORMS, MOKKA_CONTROL_PLANE_DIGEST_FILE, MOKKA_CONTROL_PLANE_SBOM_FILE, MOKKA_CONTROL_PLANE_DOCKER, and MOKKA_CONTROL_PLANE_SYFT are required." >&2; \
		exit 2; \
	fi; \
	image_name="$${image##*/}"; \
	if [[ "$$image" == *"@"* || "$$image_name" == *":"* || "$$image" =~ [[:space:]] ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_IMAGE must be an untagged repository without whitespace: $$image" >&2; \
		exit 2; \
	fi; \
	command -v "$$docker_cmd" >/dev/null 2>&1 || { echo "ERROR: required buildx frontend not found: $$docker_cmd" >&2; exit 2; }; \
	command -v "$$syft_cmd" >/dev/null 2>&1 || { echo "ERROR: required SBOM tool not found: $$syft_cmd" >&2; exit 2; }; \
	[[ -f "$$digest_file" ]] || { echo "ERROR: digest file not found: $$digest_file" >&2; exit 2; }; \
	digest="$$(cat "$$digest_file")"; \
	digest_raw="$$(cat "$$digest_file"; printf x)"; \
	if [[ ! "$$digest" =~ ^sha256:[0-9a-f]{64}$$ ]] || { [[ "$$digest_raw" != "$${digest}x" ]] && [[ "$$digest_raw" != "$$digest"$$'\n'x ]]; }; then \
		echo "ERROR: $$digest_file must contain exactly one immutable sha256 digest." >&2; \
		exit 2; \
	fi; \
	mkdir -p "$$(dirname "$$sbom_file")"; \
	sbom_tmp_dir="$$(mktemp -d "$$sbom_file.tmp.XXXXXX")"; \
	manifest_file="$$sbom_tmp_dir/manifests"; \
	trap 'rm -rf -- "$$sbom_tmp_dir"' EXIT; \
	"$$docker_cmd" buildx imagetools inspect "$$image@$$digest" --format '{{range .Manifest.Manifests}}{{printf "%s/%s" .Platform.OS .Platform.Architecture}}{{with .Platform.Variant}}{{printf "/%s" .}}{{end}}{{printf " %s\n" .Digest}}{{end}}' > "$$manifest_file"; \
	IFS=',' read -r -a configured_platforms <<< "$$platforms"; \
	sbom_outputs=(); \
	sbom_destinations=(); \
	for platform in "$${configured_platforms[@]}"; do \
		child_digest="$$(awk -v platform="$$platform" '$$1 == platform || index($$1, platform "/") == 1 { print $$2 }' "$$manifest_file")"; \
		child_digest_raw="$$(awk -v platform="$$platform" '$$1 == platform || index($$1, platform "/") == 1 { print $$2 }' "$$manifest_file"; printf x)"; \
		if [[ ! "$$child_digest" =~ ^sha256:[0-9a-f]{64}$$ ]] || [[ "$$child_digest_raw" != "$$child_digest"$$'\n'x ]]; then \
			echo "ERROR: expected exactly one child manifest digest for $$platform in $$image@$$digest." >&2; \
			exit 1; \
		fi; \
		platform_key="$${platform//\//-}"; \
		sbom_tmp="$$sbom_tmp_dir/$$platform_key.spdx.json"; \
		platform_sbom="$$sbom_file.$$platform_key"; \
		"$$syft_cmd" "$$image@$$child_digest" --output "spdx-json=$$sbom_tmp"; \
		[[ -s "$$sbom_tmp" ]] || { echo "ERROR: syft did not produce a non-empty SPDX JSON SBOM for $$platform." >&2; exit 1; }; \
		sbom_outputs+=("$$sbom_tmp"); \
		sbom_destinations+=("$$platform_sbom"); \
	done; \
	for ((i = 0; i < $${#sbom_outputs[@]}; i++)); do \
		mv "$${sbom_outputs[i]}" "$${sbom_destinations[i]}"; \
	done; \
	rm "$$manifest_file"; \
	rmdir "$$sbom_tmp_dir"; \
	trap - EXIT

mokka-control-plane-image-attest: ## Attest each Mokka control-plane child manifest with its platform SPDX JSON SBOM
	@set -eu; \
	image="$${MOKKA_CONTROL_PLANE_IMAGE:-}"; \
	platforms="$${MOKKA_CONTROL_PLANE_PLATFORMS:-}"; \
	digest_file="$${MOKKA_CONTROL_PLANE_DIGEST_FILE:-}"; \
	sbom_file="$${MOKKA_CONTROL_PLANE_SBOM_FILE:-}"; \
	docker_cmd="$${MOKKA_CONTROL_PLANE_DOCKER:-}"; \
	cosign_cmd="$${MOKKA_CONTROL_PLANE_COSIGN:-}"; \
	if [[ -z "$$image" || -z "$$platforms" || -z "$$digest_file" || -z "$$sbom_file" || -z "$$docker_cmd" || -z "$$cosign_cmd" ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_IMAGE, MOKKA_CONTROL_PLANE_PLATFORMS, MOKKA_CONTROL_PLANE_DIGEST_FILE, MOKKA_CONTROL_PLANE_SBOM_FILE, MOKKA_CONTROL_PLANE_DOCKER, and MOKKA_CONTROL_PLANE_COSIGN are required." >&2; \
		exit 2; \
	fi; \
	image_name="$${image##*/}"; \
	if [[ "$$image" == *"@"* || "$$image_name" == *":"* || "$$image" =~ [[:space:]] ]]; then \
		echo "ERROR: MOKKA_CONTROL_PLANE_IMAGE must be an untagged repository without whitespace: $$image" >&2; \
		exit 2; \
	fi; \
	command -v "$$docker_cmd" >/dev/null 2>&1 || { echo "ERROR: required buildx frontend not found: $$docker_cmd" >&2; exit 2; }; \
	command -v "$$cosign_cmd" >/dev/null 2>&1 || { echo "ERROR: required signing tool not found: $$cosign_cmd" >&2; exit 2; }; \
	[[ -f "$$digest_file" ]] || { echo "ERROR: digest file not found: $$digest_file" >&2; exit 2; }; \
	digest="$$(cat "$$digest_file")"; \
	digest_raw="$$(cat "$$digest_file"; printf x)"; \
	if [[ ! "$$digest" =~ ^sha256:[0-9a-f]{64}$$ ]] || { [[ "$$digest_raw" != "$${digest}x" ]] && [[ "$$digest_raw" != "$$digest"$$'\n'x ]]; }; then \
		echo "ERROR: $$digest_file must contain exactly one immutable sha256 digest." >&2; \
		exit 2; \
	fi; \
	manifest_file="$$(mktemp)"; \
	trap 'rm -f -- "$$manifest_file"' EXIT; \
	"$$docker_cmd" buildx imagetools inspect "$$image@$$digest" --format '{{range .Manifest.Manifests}}{{printf "%s/%s" .Platform.OS .Platform.Architecture}}{{with .Platform.Variant}}{{printf "/%s" .}}{{end}}{{printf " %s\n" .Digest}}{{end}}' > "$$manifest_file"; \
	IFS=',' read -r -a configured_platforms <<< "$$platforms"; \
	child_digests=(); \
	sbom_predicates=(); \
	for platform in "$${configured_platforms[@]}"; do \
		child_digest="$$(awk -v platform="$$platform" '$$1 == platform || index($$1, platform "/") == 1 { print $$2 }' "$$manifest_file")"; \
		child_digest_raw="$$(awk -v platform="$$platform" '$$1 == platform || index($$1, platform "/") == 1 { print $$2 }' "$$manifest_file"; printf x)"; \
		if [[ ! "$$child_digest" =~ ^sha256:[0-9a-f]{64}$$ ]] || [[ "$$child_digest_raw" != "$$child_digest"$$'\n'x ]]; then \
			echo "ERROR: expected exactly one child manifest digest for $$platform in $$image@$$digest." >&2; \
			exit 1; \
		fi; \
		platform_key="$${platform//\//-}"; \
		platform_sbom="$$sbom_file.$$platform_key"; \
		[[ -s "$$platform_sbom" ]] || { echo "ERROR: SPDX JSON SBOM not found or empty for $$platform: $$platform_sbom" >&2; exit 2; }; \
		child_digests+=("$$child_digest"); \
		sbom_predicates+=("$$platform_sbom"); \
	done; \
	for ((i = 0; i < $${#child_digests[@]}; i++)); do \
		"$$cosign_cmd" attest --yes --predicate "$${sbom_predicates[i]}" --type spdxjson "$$image@$${child_digests[i]}"; \
	done; \
	rm "$$manifest_file"; \
	trap - EXIT

build-mockpcisysfs: ## Build mockpcisysfs
	@make -C shims/libpcisysfs

.PHONY: test
test: ## Run unit tests with race detection and coverage
	@$(GO_CMD) test -v -race -coverprofile=coverage.out -covermode=atomic ./...

HELM_CHART_DIR      := deployments/nvml-mock/helm/nvml-mock
CRDS_HELM_CHART_DIR := deployments/mokka-crds/helm/mokka-crds

# Drives the built libnvidia-ml.so through go-nvml over the real C ABI.
# Docker-based, hence separate from the `go test` run.
.PHONY: test-mocknvml-bridge
test-mocknvml-bridge:
	$(MAKE) -C tests/mocknvml test

.PHONY: mockpcisysfs-shim
mockpcisysfs-shim: ## Build the mockpcisysfs LD_PRELOAD shim (libpcisysfs.so)
	@$(MAKE) -C shims/libpcisysfs

.PHONY: test-mockpcisysfs
test-mockpcisysfs: mockpcisysfs-shim ## Run mockpcisysfs integration tests
	@$(GO_CMD) test -tags integration -v ./shims/libpcisysfs/...

.PHONY: test-nvidia-imex-shim
test-nvidia-imex-shim: build ## Run nvidia-imex-shim integration tests
	@$(GO_CMD) test -v ./shims/nvidia-imex-shim/...

.PHONY: verify-greptile
verify-greptile: ## Validate the .greptile review configuration
	@bash hack/verify-greptile-config.sh

.PHONY: helm-tests
helm-tests: ## Run the nvml-mock chart unit test suite
	helm unittest $(HELM_CHART_DIR)

.PHONY: helm-crds-tests
helm-crds-tests: ## Lint + template-render the mokka-crds chart
	helm lint $(CRDS_HELM_CHART_DIR)
	helm template mokka-crds $(CRDS_HELM_CHART_DIR) > /dev/null

# Unit tests for the e2e harness itself (framework/*). They are behind the `e2e`
# build tag, so the untagged CI unit-test run skips them, and `make e2e` targets
# only the Ginkgo suite package ./tests/e2e/go -- neither reaches these. They
# need no cluster and no kubectl: the one that shells out substitutes a stub on
# PATH.
.PHONY: test-e2e-framework
test-e2e-framework:
	$(GO_CMD) test -tags e2e -race ./tests/e2e/go/framework/...

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

.PHONY: image-kind-node image-load cluster-create cluster-delete
# KIND_NODE_IMAGE_PREBUILT (env, any non-empty value): skip the local docker
# build and use the pre-built $(KIND_NODE_IMAGE) already loaded in the local
# daemon. Verify with `docker image inspect` before skipping, so a botched
# staging step fails here (with a clear message) instead of surfacing later
# as an opaque `kind create cluster` pull error. CI sets this after loading
# the image from the artifact its build-kind-node-image job uploads (see
# .github/workflows/nvml-mock-e2e-go.yaml); local devs leave it unset and get
# the rebuild-when-Dockerfile-changes behavior.
image-kind-node:
	@if [ -n "$$KIND_NODE_IMAGE_PREBUILT" ]; then \
		docker image inspect $(KIND_NODE_IMAGE) >/dev/null 2>&1 || { \
			echo "ERROR: KIND_NODE_IMAGE_PREBUILT is set but $(KIND_NODE_IMAGE) is not in the local docker daemon."; \
			echo "       Ensure a preceding step loaded it, e.g. make image-load TARBALL=<tarball> IMAGE=$(KIND_NODE_IMAGE)"; \
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

# Stage a pre-built image into the local docker daemon from a tarball:
#
#   make image-load TARBALL=/tmp/nvml-mock.tar IMAGE=nvml-mock:e2e
#
# CI hands each e2e leg its images this way (run-scoped workflow artifacts
# instead of a registry), and this target is how a leg loads one.
# Asserting IMAGE matters because these refs carry no registry host: a mismatch
# between what the producer tagged and what the consumer expects would otherwise
# stay invisible until kubelet fell through to Docker Hub and the pod failed to
# start, far from the cause. TARBALL is deleted on success — it is dead weight
# once the image is in the daemon, and creating a Kind cluster and loading
# images into its nodes right afterwards needs the disk.
image-load:
	@if [ -z "$(TARBALL)" ] || [ -z "$(IMAGE)" ]; then \
		echo "ERROR: usage: make image-load TARBALL=<path/to/image.tar> IMAGE=<repo:tag>"; \
		exit 1; \
	fi
	@docker load -i "$(TARBALL)"
	@docker image inspect "$(IMAGE)" >/dev/null 2>&1 || { \
		echo "ERROR: $(TARBALL) did not yield $(IMAGE)"; \
		exit 1; \
	}
	@rm -f "$(TARBALL)"
	@echo "Loaded $(IMAGE) from $(TARBALL)"

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
# CI builds the image once per run, every leg loads it, and sets E2E_SKIP_BUILD=true + E2E_IMAGE.
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
e2e: ## Run the Ginkgo e2e suite against E2E_CLUSTER_NAME/E2E_KUBE_CONTEXT (default mokka/kind-mokka); scope with E2E_PROFILES / E2E_GINKGO_FLAGS
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

##@ Documentation

# The docs site is MkDocs Material. CI calls these same targets so a failure
# reproduces locally with one command.
PYTHON ?= python3
MKDOCS ?= mkdocs

.PHONY: docs-deps docs-build docs-serve docs-check-exclusion docs

docs-deps: ## Install the pinned MkDocs toolchain
	$(PYTHON) -m pip install -r requirements-docs.txt

docs-build: ## Build the documentation site (strict: broken links fail)
	$(MKDOCS) build --strict

docs-serve: ## Serve the documentation site locally on :8000
	$(MKDOCS) serve

# docs/plans/ and docs/superpowers/ are gitignored scratch directories, so they
# are absent in CI and grepping the built site for them would pass no matter
# what. Plant a file and prove exclude_docs drops it, so this fails if that
# config is removed.
docs-check-exclusion: ## Verify gitignored internal plans cannot reach the site
	@set -eu; \
	mkdir -p docs/plans; \
	printf '# Internal\n\nPAGES_EXCLUSION_CANARY\n' > docs/plans/_canary.md; \
	trap 'rm -rf docs/plans/_canary.md "$(CURDIR)/tmp/canary-site"; rmdir docs/plans 2>/dev/null || true' EXIT; \
	$(MKDOCS) build --strict --site-dir "$(CURDIR)/tmp/canary-site" >/dev/null; \
	if grep -rq 'PAGES_EXCLUSION_CANARY' "$(CURDIR)/tmp/canary-site"/; then \
		echo "ERROR: docs/plans/ leaked into the site; check exclude_docs in mkdocs.yml"; \
		exit 1; \
	fi; \
	echo "exclude_docs verified: internal plans are not published"

docs: docs-check-exclusion docs-build ## Verify exclusion then build the site
