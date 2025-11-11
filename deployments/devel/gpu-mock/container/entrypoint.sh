#!/bin/bash
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

set -euo pipefail

# Constants
DRIVER_ROOT="${DRIVER_ROOT:-/host/var/lib/nvidia-mock/driver}"
HOST_DEV="${HOST_DEV:-/host/dev}"
CDI_SPEC_PATH="${CDI_SPEC_PATH:-/config/cdi-spec.yaml}"
MOCK_CONFIG_PATH="/tmp/mock-config.json"
MOCK_GPU_ARCH="${MOCK_GPU_ARCH:-dgxa100}"
DEBUG="${DEBUG:-false}"

# Logging functions
log_info() {
    echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') - $*"
}

log_error() {
    echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') - $*" >&2
}

log_debug() {
    if [ "$DEBUG" = "true" ]; then
        echo "[DEBUG] $(date '+%Y-%m-%d %H:%M:%S') - $*"
    fi
}

# Check if we're in CDI mode or default mode
detect_mode() {
    if [ -f "$CDI_SPEC_PATH" ]; then
        echo "cdi"
    else
        echo "default"
    fi
}

# Setup infrastructure based on mode
setup_infrastructure() {
    local mode=$1
    
    case "$mode" in
        cdi)
            setup_cdi_mode
            ;;
        default)
            setup_default_mode
            ;;
        *)
            log_error "Unknown mode: $mode"
            exit 1
            ;;
    esac
}

# Setup CDI mode: Parse CDI spec and generate infrastructure
setup_cdi_mode() {
    log_info "CDI mode: Parsing spec from $CDI_SPEC_PATH"
    
    # Parse CDI spec using gpu-mockctl
    if ! /usr/local/bin/gpu-mockctl cdi \
        --spec "$CDI_SPEC_PATH" \
        --output "$MOCK_CONFIG_PATH" \
        --architecture "$MOCK_GPU_ARCH"; then
        log_error "Failed to parse CDI spec"
        exit 1
    fi
    
    log_debug "CDI spec parsed successfully"
    
    # Extract configuration from parsed JSON
    GPU_COUNT=$(jq -r '.gpuCount' "$MOCK_CONFIG_PATH")
    ARCHITECTURE=$(jq -r '.architecture' "$MOCK_CONFIG_PATH")
    
    log_info "CDI mode: Detected $GPU_COUNT GPUs, architecture: $ARCHITECTURE"
    
    # Create device nodes from CDI spec
    log_info "Creating device nodes from CDI spec..."
    create_device_nodes_from_config "$MOCK_CONFIG_PATH"
    
    # Create /proc entries from CDI spec
    log_info "Creating /proc entries from CDI spec..."
    create_proc_entries_from_config "$MOCK_CONFIG_PATH"
    
    # Set environment variables for mock engine
    export MOCK_NVML_NUM_DEVICES="$GPU_COUNT"
    export MOCK_GPU_ARCH="$ARCHITECTURE"
}

# Setup default mode: Use dgxa100 defaults
setup_default_mode() {
    log_info "Default mode: Using $MOCK_GPU_ARCH (zero configuration)"
    
    # Default configuration for dgxa100
    local gpu_count=8
    
    # Allow override via environment variable
    if [ -n "${MOCK_NVML_NUM_DEVICES:-}" ]; then
        gpu_count="$MOCK_NVML_NUM_DEVICES"
    fi
    
    log_info "Default mode: Configuring $gpu_count GPUs"
    
    # Set environment variables for mock engine
    export MOCK_NVML_NUM_DEVICES="$gpu_count"
    export MOCK_GPU_ARCH="$MOCK_GPU_ARCH"
}

# Create device nodes from mock config JSON
create_device_nodes_from_config() {
    local config_file=$1
    local device_count=$(jq '.deviceNodes | length' "$config_file")
    
    log_debug "Creating $device_count device nodes..."
    
    for i in $(seq 0 $((device_count - 1))); do
        local path=$(jq -r ".deviceNodes[$i].path" "$config_file")
        local type=$(jq -r ".deviceNodes[$i].type" "$config_file")
        local major=$(jq -r ".deviceNodes[$i].major" "$config_file")
        local minor=$(jq -r ".deviceNodes[$i].minor" "$config_file")
        local mode=$(jq -r ".deviceNodes[$i].mode" "$config_file")
        
        # Convert path for host filesystem
        local host_path="${HOST_DEV}${path#/dev}"
        
        log_debug "Creating device node: $host_path (type=$type, major=$major, minor=$minor, mode=$mode)"
        
        # Create device node (as file for testing)
        if [ "${__NVCT_TESTING_DEVICES_ARE_FILES:-false}" = "true" ]; then
            touch "$host_path"
            chmod "$mode" "$host_path"
        else
            # Create real device node (requires CAP_MKNOD)
            if [ ! -e "$host_path" ]; then
                mknod "$host_path" "$type" "$major" "$minor" || {
                    log_error "Failed to create device node $host_path (may need CAP_MKNOD capability)"
                    # Fallback to file
                    touch "$host_path"
                }
                chmod "$mode" "$host_path"
            fi
        fi
    done
    
    log_info "Device nodes created successfully"
}

# Create /proc entries from mock config JSON
create_proc_entries_from_config() {
    local config_file=$1
    local proc_count=$(jq '.procEntries | length' "$config_file")
    
    log_debug "Creating $proc_count /proc entries..."
    
    for i in $(seq 0 $((proc_count - 1))); do
        local path=$(jq -r ".procEntries[$i].path" "$config_file")
        local content=$(jq -r ".procEntries[$i].content" "$config_file")
        
        # Convert path for driver root
        local full_path="${DRIVER_ROOT}${path}"
        
        log_debug "Creating /proc entry: $full_path"
        
        # Create directory structure
        mkdir -p "$(dirname "$full_path")"
        
        # Write content
        echo "$content" > "$full_path"
    done
    
    log_info "/proc entries created successfully"
}

# Main execution
main() {
    log_info "=== NVIDIA Mock GPU Infrastructure Setup ==="
    log_info "Driver Root: $DRIVER_ROOT"
    log_info "Host Device: $HOST_DEV"
    log_info "Architecture: $MOCK_GPU_ARCH"
    
    # Detect mode
    MODE=$(detect_mode)
    log_info "Operating mode: $MODE"
    
    # Setup infrastructure (only in CDI mode currently)
    # In default mode, this is mostly a no-op
    if [ "$MODE" = "cdi" ]; then
        setup_infrastructure "$MODE"
    else
        log_info "Default mode: Using built-in dgxa100 configuration"
        log_info "Environment: MOCK_GPU_ARCH=$MOCK_GPU_ARCH, MOCK_NVML_NUM_DEVICES=${MOCK_NVML_NUM_DEVICES:-8}"
    fi
    
    log_info "=== Infrastructure Setup Complete ==="
    
    # Execute the provided command (gpu-mockctl driver)
    if [ $# -gt 0 ]; then
        log_info "Executing: $*"
        exec "$@"
    else
        log_info "No command provided, exiting"
        exit 0
    fi
}

# Run main with all arguments
main "$@"

