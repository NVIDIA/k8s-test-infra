# Makefile for Mock GPU Environment
#
# This Makefile provides targets for building, deploying, and managing
# the mock NVIDIA GPU environment using both static manifests and Helm.

SHELL := /bin/bash
APP := gpu-mockctl
IMAGE ?= local/$(APP):dev
CLUSTER ?= gpu-mock
KIND_IMAGE ?= kindest/node:v1.30.0
KUBECTL ?= kubectl
HELM ?= helm
REPO_ROOT := $(shell cd ../../.. && pwd)

# Helm configuration
HELM_RELEASE ?= gpu-mock
HELM_NAMESPACE ?= gpu-mock

# Main targets
.PHONY: all clean help

# Build targets
.PHONY: build-image load-image

# Static deployment targets
.PHONY: deploy-static test-static

# Helm deployment targets
.PHONY: helm-install helm-upgrade helm-uninstall helm-test

# Utility targets
.PHONY: kind-up kind-down status logs

# Default target: build and deploy with Helm
all: kind-up build-image load-image helm-install test-gpu

# Help target
help:
	@echo "Mock GPU Environment Makefile"
	@echo ""
	@echo "Main targets:"
	@echo "  all                - Full deployment using Helm (default)"
	@echo "  deploy-static      - Deploy using static YAML manifests"
	@echo "  helm-install       - Install using Helm chart"
	@echo "  test-gpu           - Test GPU allocation"
	@echo "  clean              - Delete KIND cluster"
	@echo ""
	@echo "Build targets:"
	@echo "  build-image        - Build gpu-mockctl Docker image"
	@echo "  load-image         - Load image into KIND cluster"
	@echo ""
	@echo "Helm targets:"
	@echo "  helm-install       - Install Helm chart"
	@echo "  helm-upgrade       - Upgrade Helm release"
	@echo "  helm-uninstall     - Uninstall Helm release"
	@echo ""
	@echo "Variables:"
	@echo "  IMAGE=$(IMAGE)"
	@echo "  CLUSTER=$(CLUSTER)"
	@echo "  HELM_RELEASE=$(HELM_RELEASE)"

# Create KIND cluster
kind-up:
	@echo "Creating KIND cluster '$(CLUSTER)'..."
	kind create cluster --name $(CLUSTER) --image $(KIND_IMAGE) || true

# Delete KIND cluster
kind-down clean:
	@echo "Deleting KIND cluster '$(CLUSTER)'..."
	kind delete cluster --name $(CLUSTER) || true

# Build gpu-mockctl image
build-image:
	@echo "Building gpu-mockctl image..."
	cd $(REPO_ROOT) && docker build -t $(IMAGE) -f deployments/devel/gpu-mock/container/Dockerfile .

# Load image into KIND
load-image: build-image
	@echo "Loading image into KIND cluster..."
	kind load docker-image $(IMAGE) --name $(CLUSTER)

# Deploy using static manifests
deploy-static: kind-up load-image
	@echo "Deploying using static manifests..."
	$(KUBECTL) apply -f static/00-namespace.yaml
	$(KUBECTL) apply -f static/30-daemonset-mock-driver.yaml
	$(KUBECTL) -n gpu-mock rollout status ds/nvidia-mock-driver --timeout=120s
	$(KUBECTL) apply -f static/40-nvidia-container-toolkit.yaml
	$(KUBECTL) apply -f static/45-runtime-class.yaml
	$(KUBECTL) -n gpu-mock rollout status ds/nvidia-container-toolkit --timeout=120s
	@echo "Static deployment complete!"

# Install using Helm
helm-install: kind-up load-image
	@echo "Installing Mock GPU environment using Helm..."
	$(HELM) install $(HELM_RELEASE) ./helm/gpu-mock \
		--namespace $(HELM_NAMESPACE) \
		--set namespace.create=false \
		--create-namespace \
		--wait \
		--timeout 5m
	@echo "Helm installation complete!"

# Upgrade Helm release
helm-upgrade:
	@echo "Upgrading Mock GPU Helm release..."
	$(HELM) upgrade $(HELM_RELEASE) ./helm/gpu-mock \
		--namespace $(HELM_NAMESPACE) \
		--wait \
		--timeout 5m

# Uninstall Helm release
helm-uninstall:
	@echo "Uninstalling Mock GPU Helm release..."
	$(HELM) uninstall $(HELM_RELEASE) \
		--namespace $(HELM_NAMESPACE) \
		--wait

# Test GPU allocation
test-gpu:
	@echo "Testing GPU allocation..."
	$(KUBECTL) delete pod test-gpu-allocation -n $(HELM_NAMESPACE) --ignore-not-found=true
	$(KUBECTL) apply -f test/80-test-gpu-pod.yaml
	@echo "Waiting for test pod to complete..."
	@sleep 5
	$(KUBECTL) wait --for=condition=Ready pod/test-gpu-allocation -n $(HELM_NAMESPACE) --timeout=30s || true
	$(KUBECTL) wait --for=condition=Completed pod/test-gpu-allocation -n $(HELM_NAMESPACE) --timeout=30s || true
	@echo ""
	@echo "=== Test Pod Logs ==="
	$(KUBECTL) logs test-gpu-allocation -n $(HELM_NAMESPACE) || true
	@echo ""
	@echo "=== Node GPU Capacity ==="
	$(KUBECTL) get nodes -o custom-columns=NAME:.metadata.name,GPUs:.status.capacity.nvidia\\.com/gpu

# Show deployment status
status:
	@echo "=== Helm Releases ==="
	$(HELM) list -n $(HELM_NAMESPACE)
	@echo ""
	@echo "=== Namespace ==="
	$(KUBECTL) get ns $(HELM_NAMESPACE) --ignore-not-found=true
	@echo ""
	@echo "=== DaemonSets ==="
	$(KUBECTL) get ds -n $(HELM_NAMESPACE)
	@echo ""
	@echo "=== Pods ==="
	$(KUBECTL) get pods -n $(HELM_NAMESPACE) -o wide
	@echo ""
	@echo "=== Node Labels ==="
	$(KUBECTL) get nodes --show-labels | grep -E "(NAME|nvidia.com/gpu)" || true

# Show all logs
logs:
	@echo "=== Mock Driver Logs ==="
	$(KUBECTL) logs -n $(HELM_NAMESPACE) -l app.kubernetes.io/component=mock-driver --tail=50 --all-containers=true --prefix=true || true
	@echo ""
	@echo "=== Container Toolkit Logs ==="
	$(KUBECTL) logs -n $(HELM_NAMESPACE) -l app.kubernetes.io/component=container-toolkit --tail=50 --all-containers=true --prefix=true || true

# Test with different configurations
test-helm-minimal:
	@echo "Testing minimal installation (driver only)..."
	$(HELM) install test-minimal ./helm/gpu-mock \
		--namespace test-minimal \
		--create-namespace \
		--set containerToolkit.enabled=false \
		--wait

test-helm-full:
	@echo "Testing full installation..."
	$(HELM) install test-full ./helm/gpu-mock \
		--namespace test-full \
		--create-namespace \
		--wait

# Package Helm chart
helm-package:
	@echo "Packaging Helm chart..."
	$(HELM) package ./helm/gpu-mock

# Lint Helm chart
helm-lint:
	@echo "Linting Helm chart..."
	$(HELM) lint ./helm/gpu-mock