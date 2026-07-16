//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"errors"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

func TestAttachClusterPropagatesKubeClientError(t *testing.T) {
	wantErr := errors.New("kube client failed")
	h := &Harness{}

	err := h.attachCluster(&cluster.Cluster{
		Name:    "nvml-mock-e2e",
		Context: "kind-nvml-mock-e2e",
	}, func(string) (*kube.Client, error) {
		return nil, wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("expected kube client error %v, got %v", wantErr, err)
	}
	if h.Cluster == nil {
		t.Fatal("expected cluster to remain attached for cleanup")
	}
	if h.Kube != nil {
		t.Fatalf("expected kube client to remain nil, got %#v", h.Kube)
	}
}
