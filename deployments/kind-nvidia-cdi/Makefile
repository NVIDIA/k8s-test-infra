# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Build and push the Kind node image with nvidia-container-toolkit + CDI baked in.
#
#   make build              # build IMAGE locally
#   make push               # build and push IMAGE
#   make build IMAGE=ttl.sh/kind-nvidia-cdi:1h

DOCKER ?= docker

REGISTRY ?= ttl.sh
REPOSITORY ?= kind-nvidia-cdi
TAG ?= local
IMAGE ?= $(REGISTRY)/$(REPOSITORY):$(TAG)

# Base Kind node image; override to match your kind/kind-action version.
KIND_NODE_IMAGE ?= kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f

.PHONY: build push

build:
	$(DOCKER) build \
		--build-arg KIND_NODE_IMAGE=$(KIND_NODE_IMAGE) \
		-t $(IMAGE) \
		-f Dockerfile .

push: build
	$(DOCKER) push $(IMAGE)
