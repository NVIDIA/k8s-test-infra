//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cluster reads Kind cluster node topology via the `kind` CLI, using
// Kind/kubectl's default kubeconfig. Cluster provisioning itself lives outside
// this package (Tilt / `make cluster-create`); the suite only observes.
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

// Cluster is an existing Kind cluster the suite attaches to.
type Cluster struct {
	Name    string
	Context string
	nodes   []Node
}

var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

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
