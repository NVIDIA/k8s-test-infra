#!/bin/bash
#
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

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# Change to the mocknvml directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

echo -e "${GREEN}=== Mock NVML Test Suite ===${NC}"
echo ""

# Check if we're in Docker or can run tests locally
if command -v gcc &> /dev/null; then
    echo "Running tests locally..."
    RUN_MODE="local"
else
    echo "GCC not found, will run tests in Docker..."
    RUN_MODE="docker"
fi

# Function to run tests locally
run_tests_local() {
    echo -e "${YELLOW}Building library...${NC}"
    make clean
    make
    
    echo ""
    echo -e "${YELLOW}Running basic tests...${NC}"
    make test-basic
    
    echo ""
    echo -e "${YELLOW}Running comprehensive tests...${NC}"
    make test-comprehensive
    
    # Run valgrind tests if available
    if command -v valgrind &> /dev/null; then
        echo ""
        echo -e "${YELLOW}Running memory leak tests with valgrind...${NC}"
        make test-valgrind
    else
        echo -e "${YELLOW}Skipping valgrind tests (valgrind not installed)${NC}"
    fi
}

# Function to run tests in Docker
run_tests_docker() {
    # Create test Dockerfile
    cat > Dockerfile.test << 'EOF'
FROM debian:bookworm-slim

# Install build tools and valgrind
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    make \
    libc6-dev \
    valgrind \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

# Copy source files
COPY . .

# Run tests
CMD ["bash", "-c", "\
    echo '=== Building library ===' && \
    make clean && make && \
    echo && \
    echo '=== Running basic tests ===' && \
    make test-basic && \
    echo && \
    echo '=== Running comprehensive tests ===' && \
    make test-comprehensive && \
    echo && \
    echo '=== Running valgrind tests ===' && \
    make test-valgrind \
"]
EOF

    echo -e "${YELLOW}Building test Docker image...${NC}"
    docker build -f Dockerfile.test -t mocknvml-test .
    
    echo ""
    echo -e "${YELLOW}Running tests in Docker...${NC}"
    docker run --rm mocknvml-test
    
    # Cleanup
    rm -f Dockerfile.test
}

# Function to run go-nvml integration tests
run_go_nvml_tests() {
    echo ""
    echo -e "${YELLOW}Running go-nvml integration tests...${NC}"
    
    if [ -d "test-go-nvml" ]; then
        cd test-go-nvml
        if [ -f "test.sh" ]; then
            ./test.sh
        else
            echo -e "${RED}test-go-nvml/test.sh not found${NC}"
        fi
        cd ..
    else
        echo -e "${YELLOW}Skipping go-nvml tests (test-go-nvml directory not found)${NC}"
    fi
}

# Main execution
if [ "$RUN_MODE" = "local" ]; then
    run_tests_local
else
    run_tests_docker
fi

# Run additional integration tests
run_go_nvml_tests

echo ""
echo -e "${GREEN}=== All tests completed successfully! ===${NC}"
