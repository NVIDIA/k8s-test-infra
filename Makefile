# Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
.PHONY: build fmt verify release lint vendor check-vendor

GO_CMD ?= go
GO_FMT ?= gofmt
GO_SRC := $(shell find . -type f -name '*.go' -not -path "./vendor/*")

BINARY_NAME ?= nv-ci-bot

VERSION := 0.0.1

IMAGE_REGISTRY ?= ghcr.io/nvidia
IMAGE_TAG_NAME ?= $(VERSION)
IMAGE_NAME := k8s-test-infra
IMAGE_REPO := $(IMAGE_REGISTRY)/$(IMAGE_NAME)
IMAGE_TAG := $(IMAGE_REPO):$(IMAGE_TAG_NAME)

PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

build:
	@rm -rf bin
	$(GO_CMD) build -o bin/$(BINARY_NAME) cmd/nv-ci-bot/main.go

fmt:
	@$(GO_FMT) -w -l $$(find . -name '*.go')

verify:
	@out=`$(GO_FMT) -w -l -d $$(find . -name '*.go')`; \
	if [ -n "$$out" ]; then \
	    echo "$$out"; \
	    exit 1; \
	fi

lint:
	golangci-lint run ./...

vendor:
	go mod tidy
	go mod vendor
	go mod verify

check-vendor: vendor
	git diff --quiet HEAD -- go.mod go.sum vendor
