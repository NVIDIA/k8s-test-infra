# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Build and push the Kind node image with nvidia-container-toolkit + CDI baked in.
#
#   make build              # build IMAGE into the local docker daemon
#   make push IMAGE=<registry>/kind-nvidia-cdi:<tag>

DOCKER ?= docker
COSIGN ?= cosign
KIND ?= kind
KUBECTL ?= kubectl

REPOSITORY ?= kind-nvidia-cdi
TAG ?= local
# Registry-less by default: `kind create cluster --image` reads node images from
# the local docker daemon, so the common case never needs a registry. Pushing is
# opt-in by overriding IMAGE with a registry-qualified ref.
IMAGE ?= $(REPOSITORY):$(TAG)

# Base Kind node image; override to match your kind/kind-action version.
KIND_NODE_IMAGE ?= kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f

# nvidia-container-toolkit version. Keep this in step with the ARG default in
# Dockerfile. All four toolkit packages are installed at this exact version.
NVIDIA_CONTAINER_TOOLKIT_VERSION ?= 1.19.1-1

# Publication smoke-test defaults. Keeping the pinned KIND binary and the
# commands exercised by CI here makes the release path reproducible outside
# GitHub Actions.
KIND_VERSION ?= v0.31.0
KIND_AMD64_SHA256 ?= eb244cbafcc157dff60cf68693c14c9a75c4e6e6fedaf9cd71c58117cb93e3fa
KIND_CLUSTER_NAME ?= kind-node-smoke
SMOKE_POD ?= runtime-smoke
SMOKE_POD_IMAGE ?= registry.k8s.io/pause:3.10

DIGEST ?=
MANIFEST_OUTPUT ?= $(GITHUB_OUTPUT)
AMD64_DIGEST ?=
ARM64_DIGEST ?=
AMD64_SBOM ?= sbom-amd64.spdx.json
ARM64_SBOM ?= sbom-arm64.spdx.json
REGISTRY ?= ghcr.io

.PHONY: build push check-registry install-kind-linux-amd64 smoke-create smoke-verify-runtime \
	smoke-pod smoke-diagnostics smoke-delete check-digest resolve-manifests sign \
	attest-platform-sboms verify-anonymous-pull

build:
	$(DOCKER) build \
		--build-arg KIND_NODE_IMAGE=$(KIND_NODE_IMAGE) \
		--build-arg NVIDIA_CONTAINER_TOOLKIT_VERSION=$(NVIDIA_CONTAINER_TOOLKIT_VERSION) \
		-t $(IMAGE) \
		-f Dockerfile .

# check-registry comes first so an unpushable IMAGE fails before the long build.
push: check-registry build
	$(DOCKER) push $(IMAGE)

check-registry:
	@case "$(IMAGE)" in \
	*/*) ;; \
	*) echo "ERROR: IMAGE=$(IMAGE) has no registry, so docker push would target Docker Hub."; \
	   echo "       Retry as: make push IMAGE=<registry>/$(REPOSITORY):<tag>"; \
	   exit 1 ;; \
	esac

# GitHub's smoke runner is Linux/amd64. Local users on other platforms should
# install the pinned KIND version with their native package manager.
install-kind-linux-amd64:
	curl -fsSLo ./kind "https://kind.sigs.k8s.io/dl/$(KIND_VERSION)/kind-linux-amd64"
	echo "$(KIND_AMD64_SHA256)  ./kind" | sha256sum --check
	chmod +x ./kind
	sudo mv ./kind /usr/local/bin/kind
	$(KIND) version

smoke-create:
	$(KIND) create cluster \
		--name "$(KIND_CLUSTER_NAME)" \
		--image "$(IMAGE)" \
		--wait 120s
	$(KUBECTL) wait --for=condition=Ready node --all --timeout=30s

smoke-verify-runtime:
	@set -eu; \
	node="$(KIND_CLUSTER_NAME)-control-plane"; \
	config=$$(mktemp); \
	trap 'rm -f "$$config"' EXIT; \
	$(DOCKER) exec "$$node" containerd config dump > "$$config"; \
	grep -Eq 'enable_cdi[[:space:]]*=[[:space:]]*true' "$$config"; \
	grep -Eq "default_runtime_name[[:space:]]*=[[:space:]]*['\"]nvidia['\"]" "$$config"; \
	grep -Eq 'runtimes\.nvidia' "$$config"; \
	grep -Eq "BinaryName[[:space:]]*=[[:space:]]*['\"]/usr/bin/nvidia-container-runtime['\"]" "$$config"; \
	$(DOCKER) exec "$$node" test -x /usr/bin/nvidia-container-runtime; \
	$(DOCKER) exec "$$node" grep -Eq 'mode[[:space:]]*=[[:space:]]*"cdi"' \
		/etc/nvidia-container-runtime/config.toml

smoke-pod:
	$(KUBECTL) delete pod "$(SMOKE_POD)" --ignore-not-found --wait=true
	$(KUBECTL) run "$(SMOKE_POD)" --image="$(SMOKE_POD_IMAGE)" --restart=Never
	$(KUBECTL) wait --for=condition=Ready pod/"$(SMOKE_POD)" --timeout=60s

smoke-diagnostics:
	@node="$(KIND_CLUSTER_NAME)-control-plane"; \
	$(KUBECTL) get nodes -o wide || true; \
	$(KUBECTL) get pods --all-namespaces -o wide || true; \
	$(DOCKER) logs "$$node" || true; \
	$(DOCKER) exec "$$node" journalctl -u containerd -u kubelet --no-pager || true

smoke-delete:
	$(KIND) delete cluster --name "$(KIND_CLUSTER_NAME)"

check-digest:
	@test -n "$(DIGEST)" || { echo "ERROR: DIGEST is required"; exit 1; }

resolve-manifests: check-digest
	@test -n "$(MANIFEST_OUTPUT)" || { echo "ERROR: MANIFEST_OUTPUT or GITHUB_OUTPUT is required"; exit 1; }
	@set -eu; \
	index=$$(mktemp); \
	trap 'rm -f "$$index"' EXIT; \
	$(DOCKER) buildx imagetools inspect --raw "$(IMAGE)@$(DIGEST)" > "$$index"; \
	amd64=$$(jq -er '.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64") | .digest' "$$index"); \
	arm64=$$(jq -er '.manifests[] | select(.platform.os == "linux" and .platform.architecture == "arm64") | .digest' "$$index"); \
	test "$$amd64" != "$$arm64"; \
	echo "amd64=$$amd64" >> "$(MANIFEST_OUTPUT)"; \
	echo "arm64=$$arm64" >> "$(MANIFEST_OUTPUT)"

sign: check-digest
	$(COSIGN) sign --yes "$(IMAGE)@$(DIGEST)"

attest-platform-sboms:
	@test -n "$(AMD64_DIGEST)" || { echo "ERROR: AMD64_DIGEST is required"; exit 1; }
	@test -n "$(ARM64_DIGEST)" || { echo "ERROR: ARM64_DIGEST is required"; exit 1; }
	$(COSIGN) attest --yes --predicate "$(AMD64_SBOM)" --type spdxjson \
		"$(IMAGE)@$(AMD64_DIGEST)"
	$(COSIGN) attest --yes --predicate "$(ARM64_SBOM)" --type spdxjson \
		"$(IMAGE)@$(ARM64_DIGEST)"

# Logging out is intentional: this target proves the documented pull works for
# a user who has no GHCR credentials, rather than reusing the publisher token.
verify-anonymous-pull: check-digest
	$(DOCKER) logout "$(REGISTRY)"
	$(DOCKER) buildx imagetools inspect "$(IMAGE)@$(DIGEST)"
