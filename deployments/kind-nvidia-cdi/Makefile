# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Build and push the Kind node image with nvidia-container-toolkit + CDI baked in.
#
#   make build              # build IMAGE into the local docker daemon
#   make push IMAGE=<registry>/kind-nvidia-cdi:<tag>

DOCKER ?= docker

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

.PHONY: build push check-registry

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
