# Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

DOCKER ?= docker
IMAGE_NAME ?= mock-nvml-test
IMAGE_TAG ?= latest

REPO_ROOT := $(shell git rev-parse --show-toplevel)
MOCK_NVML_DIR := $(REPO_ROOT)/pkg/gpu/mocknvml

# Written by .github/actions/setup-dgxc-goproxy. Present only in CI, and only
# after the proxy has been configured.
GOAUTH_HELPER := $(RUNNER_TEMP)/dgxc-goauth

.PHONY: all build test clean

all: test

# Build the Docker image (builds the library inside Docker)
# The builder stages resolve through Artifactory when a credential exists and
# publicly otherwise, so this works unchanged on a developer machine. GOPROXY
# comes from the ambient environment rather than a literal, which is what the
# proxy action has already set by the time CI reaches here.
build:
	@echo "Building Docker image $(IMAGE_NAME):$(IMAGE_TAG)..."
	@cd $(REPO_ROOT) && \
	proxy_args=""; \
	if [ -f "$(GOAUTH_HELPER)" ]; then \
		echo "Routing dependencies through $$(go env GOPROXY)"; \
		proxy_args="--secret id=goauth,src=$(GOAUTH_HELPER) --build-arg GOPROXY=$$(go env GOPROXY)"; \
	fi; \
	$(DOCKER) build $$proxy_args \
		-f tests/mocknvml/Dockerfile \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		.

# Run the test
test: build
	@echo ""
	@echo "Running mock NVML integration test in Docker..."
	@echo "================================================"
	@echo ""
	$(DOCKER) run --rm -e MOCK_NVML_CONFIG=/app/util-test-config.yaml $(IMAGE_NAME):$(IMAGE_TAG)
	@echo ""
	@echo "================================================"
	@echo "✓ Mock NVML integration test completed successfully!"

clean:
	$(DOCKER) rmi -f $(IMAGE_NAME):$(IMAGE_TAG) 2>/dev/null || true
