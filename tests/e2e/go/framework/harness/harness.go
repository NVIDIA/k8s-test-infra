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
	if c != nil {
		if kerr := h.attachCluster(c, kube.New); kerr != nil {
			return h, kerr
		}
	}
	if err != nil {
		return h, err
	}
	if image != "" {
		if err := c.LoadImage(ctx, image); err != nil {
			return h, fmt.Errorf("load image %q into %q: %w", image, name, err)
		}
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
