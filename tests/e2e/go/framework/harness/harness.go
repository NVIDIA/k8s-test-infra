//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package harness composes the per-scenario adapters (cluster + kube + helm)
// wired to an externally-owned Kind cluster. The suite attaches to the cluster
// (Tilt owns provisioning and rollout) and uses the wired adapters to observe
// and assert.
package harness

import (
	"context"
	"errors"
	"fmt"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// Harness is the wired set of adapters for one cluster.
type Harness struct {
	Cluster *cluster.Cluster
	Kube    *kube.Client
	Helm    *helm.Client
	Image   string
}

// New wires the harness's adapters to a pre-existing Kind cluster identified by
// clusterName + kubeContext. The cluster and its rollout are owned externally
// (Tilt via `make cluster-create` + `tilt ci`); the harness only observes and
// asserts. image is the mock image ref already loaded in the cluster and is
// carried on the Harness for scenarios that reference it — do not (re)load it.
func New(_ context.Context, clusterName, kubeContext, image string) (*Harness, error) {
	if err := cluster.ValidateName(clusterName); err != nil {
		return nil, err
	}
	if kubeContext == "" {
		return nil, errors.New("harness.New: kubeContext must not be empty")
	}
	h := &Harness{
		Image:   image,
		Cluster: &cluster.Cluster{Name: clusterName, Context: kubeContext},
	}
	k, err := kube.New(h.Cluster.Context)
	if err != nil {
		return h, fmt.Errorf("create kube client for context %q: %w", h.Cluster.Context, err)
	}
	h.Kube = k
	h.Helm = helm.New(h.Cluster.Context)
	return h, nil
}
