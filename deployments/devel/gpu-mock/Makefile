# Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
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

all: kind-up image load apply wait logs

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

clean:
	kind delete cluster --name $(CLUSTER) || true

re: clean all

