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

# Library-size padding (issue #247). The real libnvidia-ml.so for driver
# 550.x is ~14 MiB. Some detection / security tools sanity-check the file
# size, so by default we pad the mock to land within ~10% of the real one.
#
#   TARGET_LIB_SIZE    Target on-disk size in bytes for libnvidia-ml.so.
#                      When set (the default), a two-pass build measures the
#                      unpadded library and computes padding automatically.
#   PADDING_BYTES      Explicit padding size in bytes. Overrides TARGET_LIB_SIZE.
#   NO_PADDING=1       Disable padding entirely (equivalent to BUILD_TAGS=nopadding).
#   BUILD_TAGS         Extra Go build tags. Use `nopadding` to skip padding.
TARGET_LIB_SIZE ?= 14680064
PADDING_BYTES   ?=
NO_PADDING      ?=
BUILD_TAGS      ?=

# Detect `nopadding` in BUILD_TAGS or honor NO_PADDING=1.
PADDING_DISABLED := $(if $(filter nopadding,$(BUILD_TAGS))$(filter 1,$(NO_PADDING)),1,)
GO_TAGS_FLAG     := $(if $(strip $(BUILD_TAGS)),-tags '$(strip $(BUILD_TAGS))',)

.PHONY: all clean docker-build size

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

# Set SONAME so nvidia-container-toolkit CDI spec generation creates the
# libnvidia-ml.so.1 symlink (nvidia-smi dlopen's this soname).
SONAME_FLAG := -Wl,-soname,$(LIB_SONAME)

# Portable file-size helper (Linux uses `stat -c`, macOS/BSD uses `stat -f`).
define filesize
$$(stat -c %s $(1) 2>/dev/null || stat -f %z $(1))
endef

GO_BUILD_CMD = CGO_LDFLAGS_ALLOW='--defsym|-soname' \
	CGO_LDFLAGS='$(VERSION_ALIASES) $(SONAME_FLAG)' \
	go build $(GO_TAGS_FLAG) -buildmode=c-shared -o $(LIB_REALNAME) ./bridge

$(LIB_REALNAME):
ifeq ($(PADDING_DISABLED),1)
	@echo "  Building $(LIB_REALNAME) (padding: disabled)"
	$(GO_BUILD_CMD)
else ifneq ($(strip $(PADDING_BYTES)),)
	@echo "  Building $(LIB_REALNAME) (padding: $(PADDING_BYTES) bytes, explicit)"
	CGO_CFLAGS='-DNVML_MOCK_PADDING_BYTES=$(PADDING_BYTES)' $(GO_BUILD_CMD)
else
	@echo "  Probing unpadded size for two-pass build (target: $(TARGET_LIB_SIZE) bytes)..."
	@CGO_LDFLAGS_ALLOW='--defsym|-soname' \
		CGO_LDFLAGS='$(VERSION_ALIASES) $(SONAME_FLAG)' \
		go build -tags nopadding -buildmode=c-shared -o $(LIB_REALNAME).probe ./bridge
	@base=$(call filesize,$(LIB_REALNAME).probe); \
		pad=$$(( $(TARGET_LIB_SIZE) - base )); \
		if [ $$pad -lt 0 ]; then \
			echo "  WARNING: unpadded library ($$base bytes) already >= target ($(TARGET_LIB_SIZE))"; \
			pad=0; \
		fi; \
		echo "  Padding: base=$$base bytes, padding=$$pad bytes, target=$(TARGET_LIB_SIZE) bytes"; \
		rm -f $(LIB_REALNAME).probe; \
		CGO_LDFLAGS_ALLOW='--defsym|-soname' \
		CGO_LDFLAGS='$(VERSION_ALIASES) $(SONAME_FLAG)' \
		CGO_CFLAGS="-DNVML_MOCK_PADDING_BYTES=$$pad" \
			go build -buildmode=c-shared -o $(LIB_REALNAME) ./bridge
endif
	@actual=$(call filesize,$(LIB_REALNAME)); \
		echo "  Built $(LIB_REALNAME): $$actual bytes"

# Report the built library size and how it compares to a real driver shipping
# libnvidia-ml.so (~14 MiB for driver 550.x).
size: $(LIB_REALNAME)
	@actual=$(call filesize,$(LIB_REALNAME)); \
		target=$(TARGET_LIB_SIZE); \
		pct=$$(( (actual * 100) / target )); \
		echo "$(LIB_REALNAME): $$actual bytes (target $$target, $$pct%)"

clean:
	rm -f $(LIB_NAME)* $(LIB_REALNAME).probe

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
