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

DOCKER   ?= docker
MKDIR    ?= mkdir

GOLANG_VERSION ?= 1.25.0
IMAGE_TAG ?= mock-nvml-builder
BUILDIMAGE ?= $(IMAGE_TAG)

# NVML library versioning (matches NVIDIA driver version format)
LIB_VERSION ?= 550.54.15
LIB_NAME := libnvidia-ml.so
LIB_SONAME := $(LIB_NAME).1
LIB_REALNAME := $(LIB_NAME).$(LIB_VERSION)

REPO_ROOT := $(shell git rev-parse --show-toplevel)

all: $(LIB_NAME)

$(LIB_NAME): $(LIB_REALNAME)
	ln -sf $(LIB_REALNAME) $(LIB_SONAME)
	ln -sf $(LIB_SONAME) $(LIB_NAME)

$(LIB_REALNAME):
	go build -buildmode=c-shared -o $(LIB_REALNAME) ./bridge

clean:
	rm -f $(LIB_NAME)*

.build-image: Dockerfile
	$(DOCKER) build \
		--build-arg GOLANG_VERSION=$(GOLANG_VERSION) \
		-t $(BUILDIMAGE) \
		-f Dockerfile .

docker-build: .build-image
	@echo "Running 'make' in container image $(BUILDIMAGE)"
	$(DOCKER) run \
		--rm \
		-e GOCACHE=/tmp/.cache/go \
		-e GOMODCACHE=/tmp/.cache/gomod \
		-v $(REPO_ROOT):/work \
		-w /work/pkg/gpu/mocknvml \
		--user $$(id -u):$$(id -g) \
		$(BUILDIMAGE) \
			make
