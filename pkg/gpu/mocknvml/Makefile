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
LIB_VERSION ?= 550.163.01
LIB_NAME := libnvidia-ml.so
LIB_SONAME := $(LIB_NAME).1
LIB_REALNAME := $(LIB_NAME).$(LIB_VERSION)

REPO_ROOT := $(shell git rev-parse --show-toplevel)

.PHONY: all clean docker-build

all: $(LIB_NAME)

$(LIB_NAME): $(LIB_REALNAME)
	ln -sf $(LIB_REALNAME) $(LIB_SONAME)
	ln -sf $(LIB_SONAME) $(LIB_NAME)

# Version aliases: NVML header macros map unversioned names to versioned ones
# (e.g., nvmlDeviceGetPciInfo → nvmlDeviceGetPciInfo_v3). nvidia-smi dlsym()s
# the unversioned names, so we need linker aliases.
VERSION_ALIASES := \
	-Wl,--defsym,nvmlDeviceGetPciInfo=nvmlDeviceGetPciInfo_v3 \
	-Wl,--defsym,nvmlDeviceGetHandleByPciBusId=nvmlDeviceGetHandleByPciBusId_v2 \
	-Wl,--defsym,nvmlDeviceGetNvLinkRemotePciInfo=nvmlDeviceGetNvLinkRemotePciInfo_v2 \
	-Wl,--defsym,nvmlDeviceRemoveGpu=nvmlDeviceRemoveGpu_v2 \
	-Wl,--defsym,nvmlDeviceGetGridLicensableFeatures=nvmlDeviceGetGridLicensableFeatures_v4 \
	-Wl,--defsym,nvmlEventSetWait=nvmlEventSetWait_v2 \
	-Wl,--defsym,nvmlDeviceGetAttributes=nvmlDeviceGetAttributes_v2 \
	-Wl,--defsym,nvmlComputeInstanceGetInfo=nvmlComputeInstanceGetInfo_v2 \
	-Wl,--defsym,nvmlDeviceGetComputeRunningProcesses=nvmlDeviceGetComputeRunningProcesses_v3 \
	-Wl,--defsym,nvmlDeviceGetGraphicsRunningProcesses=nvmlDeviceGetGraphicsRunningProcesses_v3 \
	-Wl,--defsym,nvmlDeviceGetMPSComputeRunningProcesses=nvmlDeviceGetMPSComputeRunningProcesses_v3 \
	-Wl,--defsym,nvmlDeviceGetGpuInstancePossiblePlacements=nvmlDeviceGetGpuInstancePossiblePlacements_v2 \
	-Wl,--defsym,nvmlVgpuInstanceGetLicenseInfo=nvmlVgpuInstanceGetLicenseInfo_v2 \
	-Wl,--defsym,nvmlDeviceGetDriverModel=nvmlDeviceGetDriverModel_v2

$(LIB_REALNAME):
	CGO_LDFLAGS_ALLOW='--defsym' CGO_LDFLAGS='$(VERSION_ALIASES)' \
		go build -buildmode=c-shared -o $(LIB_REALNAME) ./bridge

clean:
	rm -f $(LIB_NAME)*

.build-image: Dockerfile
	$(DOCKER) build \
		--build-arg GOLANG_VERSION=$(GOLANG_VERSION) \
		-t $(BUILDIMAGE) \
		-f Dockerfile .

docker-build: .build-image
	@echo "Building mock NVML library in container image $(BUILDIMAGE)"
	$(DOCKER) run \
		--rm \
		-e GOCACHE=/tmp/.cache/go \
		-e GOMODCACHE=/tmp/.cache/gomod \
		-v $(REPO_ROOT):/work \
		-w /work/pkg/gpu/mocknvml \
		--user $$(id -u):$$(id -g) \
		$(BUILDIMAGE) \
			make
