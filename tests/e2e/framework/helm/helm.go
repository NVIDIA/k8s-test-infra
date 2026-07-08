//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package helm wraps the `helm` CLI. Installs are --wait with an explicit
// timeout (no sleeps).
package helm

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/framework/runner"
)

// Client runs helm against a specific kube context in the default kubeconfig.
type Client struct {
	Context string
}

// New returns a helm client. An empty context uses Helm's current context.
func New(context string) *Client { return &Client{Context: context} }

// Release describes a `helm install`.
type Release struct {
	Name            string
	Chart           string
	Namespace       string
	CreateNamespace bool
	ReuseValues     bool
	ValuesFiles     []string
	Set             map[string]string
	Wait            bool
	Timeout         time.Duration
}

func (c *Client) base() []string {
	if c.Context == "" {
		return nil
	}
	return []string{"--kube-context", c.Context}
}

// RepoAdd adds a helm repo.
func (c *Client) RepoAdd(ctx context.Context, name, url string) error {
	args := append(c.base(), "repo", "add", name, url, "--force-update")
	_, err := runner.Run(ctx, "helm", args...)
	return err
}

// RepoUpdate refreshes repo indexes.
func (c *Client) RepoUpdate(ctx context.Context) error {
	args := append(c.base(), "repo", "update")
	_, err := runner.Run(ctx, "helm", args...)
	return err
}

// Install runs `helm install` for rel.
func (c *Client) Install(ctx context.Context, rel Release) error {
	return c.run(ctx, "install", rel)
}

// UpgradeInstall runs `helm upgrade --install` for rel. Used to re-deploy the
// nvml-mock release with a different profile onto the SAME shared cluster
// (a chart upgrade, not a cluster rebuild).
func (c *Client) UpgradeInstall(ctx context.Context, rel Release) error {
	return c.run(ctx, "upgrade", rel, "--install")
}

func (c *Client) run(ctx context.Context, verb string, rel Release, extra ...string) error {
	args := append(c.base(), verb, rel.Name, rel.Chart)
	args = append(args, extra...)
	if rel.Namespace != "" {
		args = append(args, "--namespace", rel.Namespace)
	}
	if rel.CreateNamespace {
		args = append(args, "--create-namespace")
	}
	if rel.ReuseValues {
		args = append(args, "--reuse-values")
	}
	for _, vf := range rel.ValuesFiles {
		args = append(args, "-f", vf)
	}
	// Sort --set keys for deterministic command logging.
	keys := make([]string, 0, len(rel.Set))
	for k := range rel.Set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--set", fmt.Sprintf("%s=%s", k, rel.Set[k]))
	}
	if rel.Wait {
		args = append(args, "--wait")
	}
	if rel.Timeout > 0 {
		args = append(args, "--timeout", rel.Timeout.String())
	}
	_, err := runner.Run(ctx, "helm", args...)
	return err
}

// Uninstall removes a release.
func (c *Client) Uninstall(ctx context.Context, name, namespace string) error {
	args := append(c.base(), "uninstall", name)
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	_, err := runner.Run(ctx, "helm", args...)
	return err
}
