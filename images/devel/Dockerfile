# Copyright (c) 2021, NVIDIA CORPORATION.  All rights reserved.
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

ARG GOLANG_VERSION=x.x.x
FROM golang:${GOLANG_VERSION}

ARG CLIENT_GEN_VERSION=v0.26.1
ARG CONTROLLER_GEN_VERSION=v0.9.2
ARG GOLANGCI_LINT_VERSION=v1.52.0
ARG MOQ_VERSION=v0.3.4

RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@${GOLANGCI_LINT_VERSION} \
    && go install github.com/matryer/moq@${MOQ_VERSION} \
    && go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION} \
    && go install k8s.io/code-generator/cmd/client-gen@${CLIENT_GEN_VERSION}

# We need to set the /work directory as a safe directory.
# This allows git commands to run in the container.
RUN git config --file=/.gitconfig --add safe.directory /work

