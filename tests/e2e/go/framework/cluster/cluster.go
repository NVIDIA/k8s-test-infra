//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cluster owns the Kind cluster lifecycle via the `kind` CLI. It uses
// Kind/kubectl's default kubeconfig. Creation is idempotent
// ("delete-if-exists then create").
// Node-name resolution is centralized here (parse `kind get nodes` once into
// typed roles) instead of scattering `grep worker | sort` across scenarios.
package cluster

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

// Role is a Kind node role.
type Role string

const (
	RoleControlPlane Role = "control-plane"
	RoleWorker       Role = "worker"
)

// Node is a Kind node; Name is also the docker container name.
type Node struct {
	Name string
	Role Role
}

// Cluster is a created Kind cluster.
type Cluster struct {
	Name    string
	Context string
	nodes   []Node
}

var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

const kindConfigStdinPath = "/dev/stdin"

// ValidateName enforces a short, DNS-ish, deterministic cluster name. Kind
// prefixes node container names with this, so keep it well under docker limits.
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > 40 {
		return fmt.Errorf("cluster name %q must be 1..40 chars", name)
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("cluster name %q must match %s", name, nameRE.String())
	}
	return nil
}

// Create makes a Kind cluster named name. If configYAML is non-empty it is
// streamed to `kind create cluster --config /dev/stdin`. The cluster is always
// returned (even on create error) so the caller can DeferCleanup it.
func Create(ctx context.Context, name string, configYAML []byte) (*Cluster, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	c := &Cluster{Name: name, Context: KindContext(name)}

	// delete-if-exists (idempotent on a dirty machine / re-run).
	_, _ = runner.Run(ctx, "kind", "delete", "cluster", "--name", name)

	args := createArgs(name, len(configYAML) > 0)
	var resErr error
	if len(configYAML) > 0 {
		_, resErr = runner.RunInput(ctx, string(configYAML), "kind", args...)
	} else {
		_, resErr = runner.Run(ctx, "kind", args...)
	}
	if resErr != nil {
		return c, fmt.Errorf("kind create cluster %q: %w", name, resErr)
	}
	return c, nil
}

func createArgs(name string, hasConfig bool) []string {
	args := []string{"create", "cluster", "--name", name, "--wait", "180s"}
	if hasConfig {
		args = append(args, "--config", kindConfigStdinPath)
	}
	return args
}

// KindContext returns the kubeconfig context name Kind creates for a cluster.
func KindContext(name string) string {
	return "kind-" + name
}

// LoadImage loads a local docker image into the cluster.
func (c *Cluster) LoadImage(ctx context.Context, ref string) error {
	if _, err := runner.Run(ctx, "kind", "load", "docker-image", ref, "--name", c.Name); err != nil {
		return fmt.Errorf("kind load docker-image %q: %w", ref, err)
	}
	return nil
}

// Nodes returns all nodes (cached), parsed once from `kind get nodes`.
func (c *Cluster) Nodes(ctx context.Context) ([]Node, error) {
	if c.nodes != nil {
		return c.nodes, nil
	}
	res, err := runner.Run(ctx, "kind", "get", "nodes", "--name", c.Name)
	if err != nil {
		return nil, fmt.Errorf("kind get nodes %q: %w", c.Name, err)
	}
	var ns []Node
	for _, line := range strings.Split(res.Stdout, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		role := RoleControlPlane
		if strings.Contains(name, "worker") {
			role = RoleWorker
		}
		ns = append(ns, Node{Name: name, Role: role})
	}
	// Deterministic ordering: workers sorted by name so worker1<worker2.
	sort.Slice(ns, func(i, j int) bool { return ns[i].Name < ns[j].Name })
	c.nodes = ns
	return ns, nil
}

// ControlPlane returns the (first) control-plane node.
func (c *Cluster) ControlPlane(ctx context.Context) (Node, error) {
	ns, err := c.Nodes(ctx)
	if err != nil {
		return Node{}, err
	}
	for _, n := range ns {
		if n.Role == RoleControlPlane {
			return n, nil
		}
	}
	return Node{}, fmt.Errorf("cluster %q has no control-plane node", c.Name)
}

// Workers returns worker nodes sorted by name.
func (c *Cluster) Workers(ctx context.Context) ([]Node, error) {
	ns, err := c.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	var ws []Node
	for _, n := range ns {
		if n.Role == RoleWorker {
			ws = append(ws, n)
		}
	}
	return ws, nil
}

// Delete tears down the cluster.
func (c *Cluster) Delete(ctx context.Context) error {
	_, err := runner.Run(ctx, "kind", "delete", "cluster", "--name", c.Name)
	return err
}
