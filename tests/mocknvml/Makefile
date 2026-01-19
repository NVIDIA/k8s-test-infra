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

.PHONY: all build test clean

all: test

# Build the Docker image (builds the library inside Docker)
build:
	@echo "Building Docker image $(IMAGE_NAME):$(IMAGE_TAG)..."
	cd $(REPO_ROOT) && $(DOCKER) build \
		-f tests/mocknvml/Dockerfile \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		.

# Run the test
test: build
	@echo ""
	@echo "Running mock NVML integration test in Docker..."
	@echo "================================================"
	@echo ""
	$(DOCKER) run --rm $(IMAGE_NAME):$(IMAGE_TAG)
	@echo ""
	@echo "================================================"
	@echo "✓ Mock NVML integration test completed successfully!"

clean:
	$(DOCKER) rmi -f $(IMAGE_NAME):$(IMAGE_TAG) 2>/dev/null || true
