//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package harness composes the per-scenario adapters (cluster + kube + helm)
// bound to one isolated Kind cluster, and owns its lifecycle.
package harness

import (
	"context"
	"fmt"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/helm"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

type kubeClientFactory func(context string) (*kube.Client, error)

// Harness is the wired set of adapters for one cluster.
type Harness struct {
	Cluster *cluster.Cluster
	Kube    *kube.Client
	Helm    *helm.Client
	Image   string
}

// Setup creates a Kind cluster named name (delete-if-exists then create) with
// the given kind config, wires adapters to its Kind context in the default
// kubeconfig, and kind-loads image. The Harness is returned even on error (with
// whatever was created) so callers can DeferCleanup it.
func Setup(ctx context.Context, name string, kindConfig []byte, image string) (*Harness, error) {
	h := &Harness{Image: image}

	c, err := cluster.Create(ctx, name, kindConfig)
	if err != nil {
		// Preserve the create failure as the primary error. In failed Kind
		// creates, kube client setup commonly fails too and would otherwise mask
		// the actionable cluster-create output.
		h.Cluster = c
		return h, err
	}
	if err := h.attachCluster(c, kube.New); err != nil {
		return h, err
	}
	if image != "" {
		if err := c.LoadImage(ctx, image); err != nil {
			return h, fmt.Errorf("load image %q into %q: %w", image, name, err)
		}
	}
	return h, nil
}

// AttachExisting wires adapters to a pre-existing cluster/context without
// creating it or loading images. Used when an external tool (e.g. `tilt ci`)
// owns cluster provisioning and rollout; the harness only observes and tests.
// clusterName is the Kind cluster name (used by `kind get nodes --name` in
// node-role assertions); kubeContext is the kubeconfig context to route
// kubectl/helm through. image is the ref already present in the cluster and is
// carried on the Harness for scenarios that reference it (they must not attempt
// to (re)load it).
func AttachExisting(ctx context.Context, clusterName, kubeContext, image string) (*Harness, error) {
	if err := cluster.ValidateName(clusterName); err != nil {
		return nil, err
	}
	if kubeContext == "" {
		return nil, fmt.Errorf("attach existing: kubeContext must not be empty")
	}
	h := &Harness{Image: image}
	c := &cluster.Cluster{Name: clusterName, Context: kubeContext}
	if err := h.attachCluster(c, kube.New); err != nil {
		return h, err
	}
	return h, nil
}

func (h *Harness) attachCluster(c *cluster.Cluster, newKube kubeClientFactory) error {
	h.Cluster = c
	k, err := newKube(c.Context)
	if err != nil {
		return fmt.Errorf("create kube client for context %q: %w", c.Context, err)
	}
	h.Kube = k
	h.Helm = helm.New(c.Context)
	return nil
}

// Teardown deletes the cluster (unless keep is set).
func (h *Harness) Teardown(ctx context.Context, keep bool) error {
	if h == nil || h.Cluster == nil {
		return nil
	}
	if keep {
		return nil
	}
	return h.Cluster.Delete(ctx)
}
