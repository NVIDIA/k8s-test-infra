# Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
#
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

# Mock NVML Library Makefile
#
# This Makefile supports two implementations:
#   - C-based (traditional, src/*.c)
#   - Go-based (new, bridge/*.go + engine/*.go)
#
# Use 'make' or 'make build-c' for C implementation
# Use 'make build-go' for Go implementation
# Use 'make build-all' for both implementations

SHELL := /bin/bash

# Common configuration
VERSION = 550.54.15
MAJOR = 1

#==============================================================================
# C-BASED IMPLEMENTATION
#==============================================================================

# C compiler and flags
CC ?= gcc
CFLAGS = -fPIC -Wall -Wextra -O2 -I./include -D_GNU_SOURCE
LDFLAGS = -shared -Wl,-soname,libnvidia-ml.so.$(MAJOR) -lpthread

# C source files
SRCS = $(wildcard src/*.c)
OBJS = $(SRCS:src/%.c=build/%.o)

# C output directories
BUILD_DIR = build
LIB_DIR = $(BUILD_DIR)/lib64

# C target library
TARGET = $(LIB_DIR)/libnvidia-ml.so.$(VERSION)
SONAME = libnvidia-ml.so.$(MAJOR)
LINKNAME = libnvidia-ml.so

# Default target (C-based for backward compatibility)
all: $(TARGET) symlinks

# Create directories
$(BUILD_DIR) $(LIB_DIR):
	@mkdir -p $@

# Compile object files
build/%.o: src/%.c | $(BUILD_DIR)
	$(CC) $(CFLAGS) -c $< -o $@

# Link shared library
$(TARGET): $(OBJS) | $(LIB_DIR)
	$(CC) $(LDFLAGS) -o $@ $^

# Create symlinks
symlinks: $(TARGET)
	@cd $(LIB_DIR) && \
		ln -sf libnvidia-ml.so.$(VERSION) $(SONAME) && \
		ln -sf $(SONAME) $(LINKNAME)

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

# Install to a directory (for testing)
install: all
	@echo "Installing to $(DESTDIR)/lib64..."
	@mkdir -p $(DESTDIR)/lib64
	@cp -a $(LIB_DIR)/* $(DESTDIR)/lib64/

# Test compilation
test: all test-basic test-comprehensive

test-basic: all
	@echo "Building basic test program..."
	@mkdir -p $(BUILD_DIR)/test
	$(CC) -o $(BUILD_DIR)/test/test_nvml test/test_nvml.c \
		-L$(LIB_DIR) -lnvidia-ml -Wl,-rpath,$(LIB_DIR)
	@echo "Running basic test..."
	$(BUILD_DIR)/test/test_nvml

test-comprehensive: all
	@echo "Building comprehensive test suite..."
	@mkdir -p $(BUILD_DIR)/test
	$(CC) -o $(BUILD_DIR)/test/test_nvml_comprehensive \
		test/test_nvml_comprehensive.c \
		-L$(LIB_DIR) -lnvidia-ml -lpthread -Wl,-rpath,$(LIB_DIR)
	@echo "Running comprehensive tests..."
	$(BUILD_DIR)/test/test_nvml_comprehensive

test-valgrind: all
	@echo "Running tests with valgrind..."
	@mkdir -p $(BUILD_DIR)/test
	$(CC) -g -o $(BUILD_DIR)/test/test_nvml_comprehensive \
		test/test_nvml_comprehensive.c \
		-L$(LIB_DIR) -lnvidia-ml -lpthread -Wl,-rpath,$(LIB_DIR)
	valgrind --leak-check=full --error-exitcode=1 \
		$(BUILD_DIR)/test/test_nvml_comprehensive

# Show library info
info: $(TARGET)
	@echo "=== C-based Mock NVML Library Information ==="
	@file $(TARGET)
	@echo ""
	@echo "Library version: $(VERSION)"
	@echo "SONAME: $(SONAME)"
	@echo ""
	@echo "Exported symbols (first 20):"
	@nm -D $(TARGET) | grep " T " | cut -d' ' -f3 | sort | head -20
	@echo "..."
	@echo "Total exported symbols: $$(nm -D $(TARGET) | grep " T " | wc -l)"
	@echo ""
	@echo "Library dependencies:"
	@ldd $(TARGET)

#==============================================================================
# GO-BASED IMPLEMENTATION
#==============================================================================

# Go build configuration
GO := go
GO_BUILD_FLAGS := -buildmode=c-shared
GO_LDFLAGS := -s -w

# Go output directories
GO_BUILD_DIR := build-go
GO_LIB_DIR := $(GO_BUILD_DIR)/lib64

# Go target library files
GO_TARGET := $(GO_LIB_DIR)/libnvidia-ml.so.$(VERSION)
GO_SONAME := $(GO_LIB_DIR)/libnvidia-ml.so.1
GO_LINKNAME := $(GO_LIB_DIR)/libnvidia-ml.so

# Build Go-based library
build-go: $(GO_TARGET) $(GO_SONAME) $(GO_LINKNAME)

# Build the shared library from Go source
$(GO_TARGET): bridge/*.go engine/*.go
	@echo "Building Go-based mock NVML library..."
	@mkdir -p $(GO_LIB_DIR)
	cd bridge && $(GO) build $(GO_BUILD_FLAGS) \
		-ldflags "$(GO_LDFLAGS)" \
		-o ../$(GO_TARGET) \
		.
	@echo "Removing generated .h file (not needed for runtime)"
	@rm -f $(GO_LIB_DIR)/libnvidia-ml.h
	@echo "Library built: $(GO_TARGET)"

# Create SONAME symlink
$(GO_SONAME): $(GO_TARGET)
	@echo "Creating SONAME symlink: $(GO_SONAME)"
	@cd $(GO_LIB_DIR) && \
		ln -sf libnvidia-ml.so.$(VERSION) libnvidia-ml.so.1

# Create development symlink
$(GO_LINKNAME): $(GO_SONAME)
	@echo "Creating dev symlink: $(GO_LINKNAME)"
	@cd $(GO_LIB_DIR) && \
		ln -sf libnvidia-ml.so.1 libnvidia-ml.so

# Clean Go build artifacts
clean-go:
	rm -rf $(GO_BUILD_DIR)

# Test Go-based library
test-go: $(GO_TARGET) $(GO_SONAME) $(GO_LINKNAME)
	@echo "Building and running Go bridge test..."
	@mkdir -p $(GO_BUILD_DIR)/test
	gcc -o $(GO_BUILD_DIR)/test/test_bridge test/test_bridge.c \
		-L$(GO_LIB_DIR) -lnvidia-ml \
		-Wl,-rpath,$(PWD)/$(GO_LIB_DIR)
	@echo "Running basic test..."
	DYLD_LIBRARY_PATH=$(PWD)/$(GO_LIB_DIR):$$DYLD_LIBRARY_PATH \
		$(GO_BUILD_DIR)/test/test_bridge
	@echo ""
	@echo "Building extended test..."
	gcc -o $(GO_BUILD_DIR)/test/test_extended test/test_extended.c \
		-L$(GO_LIB_DIR) -lnvidia-ml \
		-Wl,-rpath,$(PWD)/$(GO_LIB_DIR)
	@echo "Running extended test..."
	DYLD_LIBRARY_PATH=$(PWD)/$(GO_LIB_DIR):$$DYLD_LIBRARY_PATH \
		$(GO_BUILD_DIR)/test/test_extended

# Test Go-based library with environment variables
test-go-env: $(GO_TARGET) $(GO_SONAME) $(GO_LINKNAME)
	@echo "Testing environment variable configuration..."
	@mkdir -p $(GO_BUILD_DIR)/test
	gcc -o $(GO_BUILD_DIR)/test/test_bridge test/test_bridge.c \
		-L$(GO_LIB_DIR) -lnvidia-ml \
		-Wl,-rpath,$(PWD)/$(GO_LIB_DIR)
	@echo "Test 1: Default (8 devices)"
	DYLD_LIBRARY_PATH=$(PWD)/$(GO_LIB_DIR):$$DYLD_LIBRARY_PATH \
		$(GO_BUILD_DIR)/test/test_bridge
	@echo ""
	@echo "Test 2: Override to 4 devices"
	DYLD_LIBRARY_PATH=$(PWD)/$(GO_LIB_DIR):$$DYLD_LIBRARY_PATH \
		MOCK_NVML_NUM_DEVICES=4 $(GO_BUILD_DIR)/test/test_bridge
	@echo ""
	@echo "Test 3: Override to 2 devices"
	DYLD_LIBRARY_PATH=$(PWD)/$(GO_LIB_DIR):$$DYLD_LIBRARY_PATH \
		MOCK_NVML_NUM_DEVICES=2 $(GO_BUILD_DIR)/test/test_bridge

# Show Go library info
info-go: $(GO_TARGET)
	@echo "=== Go-based Mock NVML Library Information ==="
	@file $(GO_TARGET)
	@echo ""
	@echo "Library version: $(VERSION)"
	@echo "SONAME: libnvidia-ml.so.1"
	@echo ""
	@if [ -f "$(GO_TARGET)" ]; then \
		echo "Exported symbols (first 20):"; \
		nm -D $(GO_TARGET) | grep "nvml" | head -20; \
		echo "... (showing first 20)"; \
	fi

#==============================================================================
# CONVENIENCE TARGETS
#==============================================================================

# Aliases for C implementation (backward compatibility)
build-c: all

test-c: test

# Build both implementations
build-all: build-c build-go

# Test both implementations
test-all: test-c test-go

# Clean both implementations
clean-all: clean clean-go

# Show info for both implementations
info-all: info info-go

#==============================================================================
# PHONY TARGETS
#==============================================================================

.PHONY: all clean install test test-basic test-comprehensive test-valgrind
.PHONY: info symlinks
.PHONY: build-go test-go test-go-env clean-go info-go
.PHONY: build-c test-c
.PHONY: build-all test-all clean-all info-all
