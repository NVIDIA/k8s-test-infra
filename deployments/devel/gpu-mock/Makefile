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

SHELL := /bin/bash
APP := gpu-mockctl
IMAGE ?= local/$(APP):dev
CLUSTER ?= gpu-mock
KIND_IMAGE ?= kindest/node:v1.30.0
KUBECTL ?= kubectl
REPO_ROOT := $(shell cd ../../.. && pwd)

.PHONY: all kind-up image load apply wait logs clean re vendor
.PHONY: apply-cdi wait-cdi logs-cdi smoke all-cdi

all: kind-up image load apply wait logs

# Step 2 targets
all-cdi: all apply-cdi wait-cdi logs-cdi smoke

vendor:
	cd $(REPO_ROOT) && go mod tidy && go mod vendor

image: vendor
	cd $(REPO_ROOT) && docker build -t $(IMAGE) -f deployments/devel/gpu-mock/Dockerfile .

load:
	kind load docker-image $(IMAGE) --name $(CLUSTER)

kind-up:
	kind create cluster --name $(CLUSTER) --image $(KIND_IMAGE) || true

apply:
	$(KUBECTL) apply -f 00-namespace.yaml
	$(KUBECTL) -n gpu-mock apply -f 10-configmap-verify-script.yaml
	$(KUBECTL) -n gpu-mock apply -f 20-job-gpu-mock-verify.yaml

wait:
	$(KUBECTL) -n gpu-mock wait --for=condition=complete job/gpu-mock-verify --timeout=180s

logs:
	@pod=$$($(KUBECTL) -n gpu-mock get pods -l job-name=gpu-mock-verify -o jsonpath='{.items[0].metadata.name}'); \
	$(KUBECTL) -n gpu-mock logs $$pod

apply-cdi:
	$(KUBECTL) -n gpu-mock apply -f 30-daemonset-cdi-mock.yaml

wait-cdi:
	$(KUBECTL) -n gpu-mock rollout status ds/nvidia-cdi-mock --timeout=120s

logs-cdi:
	@echo "=== DaemonSet logs ==="
	$(KUBECTL) -n gpu-mock logs -l app=nvidia-cdi-mock --tail=-1 --all-containers=true --prefix=true || true

smoke:
	$(KUBECTL) -n gpu-mock delete job/cdi-smoke --ignore-not-found=true
	$(KUBECTL) -n gpu-mock apply -f 40-job-cdi-smoke.yaml
	$(KUBECTL) -n gpu-mock wait --for=condition=complete job/cdi-smoke --timeout=120s
	@echo ""
	@echo "=== CDI Smoke Test Logs ==="
	@pod=$$($(KUBECTL) -n gpu-mock get pods -l app=cdi-smoke -o jsonpath='{.items[0].metadata.name}'); \
	$(KUBECTL) -n gpu-mock logs $$pod

clean:
	kind delete cluster --name $(CLUSTER) || true

re: clean all

