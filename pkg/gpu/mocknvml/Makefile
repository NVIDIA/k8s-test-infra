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

# Version information
VERSION = 550.54.15
MAJOR = 1

# Compiler and flags
CC ?= gcc
CFLAGS = -fPIC -Wall -Wextra -O2 -I./include -D_GNU_SOURCE
LDFLAGS = -shared -Wl,-soname,libnvidia-ml.so.$(MAJOR) -lpthread

# Source files
SRCS = $(wildcard src/*.c)
OBJS = $(SRCS:src/%.c=build/%.o)

# Output directory
BUILD_DIR = build
LIB_DIR = $(BUILD_DIR)/lib64

# Target library
TARGET = $(LIB_DIR)/libnvidia-ml.so.$(VERSION)
SONAME = libnvidia-ml.so.$(MAJOR)
LINKNAME = libnvidia-ml.so

# Default target
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
	$(CC) -o $(BUILD_DIR)/test/test_nvml test/test_nvml.c -L$(LIB_DIR) -lnvidia-ml -Wl,-rpath,$(LIB_DIR)
	@echo "Running basic test..."
	$(BUILD_DIR)/test/test_nvml

test-comprehensive: all
	@echo "Building comprehensive test suite..."
	@mkdir -p $(BUILD_DIR)/test
	$(CC) -o $(BUILD_DIR)/test/test_nvml_comprehensive test/test_nvml_comprehensive.c -L$(LIB_DIR) -lnvidia-ml -lpthread -Wl,-rpath,$(LIB_DIR)
	@echo "Running comprehensive tests..."
	$(BUILD_DIR)/test/test_nvml_comprehensive

test-valgrind: all
	@echo "Running tests with valgrind..."
	@mkdir -p $(BUILD_DIR)/test
	$(CC) -g -o $(BUILD_DIR)/test/test_nvml_comprehensive test/test_nvml_comprehensive.c -L$(LIB_DIR) -lnvidia-ml -lpthread -Wl,-rpath,$(LIB_DIR)
	valgrind --leak-check=full --error-exitcode=1 $(BUILD_DIR)/test/test_nvml_comprehensive

# Show library info
info: $(TARGET)
	@echo "=== Mock NVML Library Information ==="
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

.PHONY: all clean install test test-basic test-comprehensive test-valgrind info symlinks
