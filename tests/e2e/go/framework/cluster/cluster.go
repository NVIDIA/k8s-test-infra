//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cluster reads Kind cluster node topology from the Kubernetes API,
// using Kind/kubectl's default kubeconfig. Cluster provisioning itself lives
// outside this package (Tilt / `make cluster-create`); the suite only observes.
package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

// Role is a Kind node role.
type Role string

// Role values match the "control-plane" / "worker" labels Kind applies to nodes.
const (
	RoleControlPlane Role = "control-plane"
	RoleWorker       Role = "worker"
)

// Node is a Kind node. Name is the Kubernetes node name; Container is the
// docker container hosting it. They diverge whenever the cluster config pins
// nodeRegistration.name (see local/kind/default.kind.yaml), so callers must
// choose deliberately: kubectl takes Name, `docker exec` takes Container.
type Node struct {
	Name      string
	Container string
	Role      Role
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

// controlPlaneLabel is kubeadm's control-plane marker. Roles are read from it
// rather than from the node name: a config that pins nodeRegistration.name is
// free to choose names that say nothing about the role.
const controlPlaneLabel = "node-role.kubernetes.io/control-plane"

// nodeList is the subset of `kubectl get nodes -o json` the suite reads.
type nodeList struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			ProviderID string `json:"providerID"`
		} `json:"spec"`
	} `json:"items"`
}

// Nodes returns all nodes (cached), discovered from the Kubernetes API rather
// than from `kind get nodes`: the latter reports container names, which are not
// the node names once the cluster config pins nodeRegistration.name.
func (c *Cluster) Nodes(ctx context.Context) ([]Node, error) {
	if c.nodes != nil {
		return c.nodes, nil
	}
	res, err := runner.RunQuiet(ctx, "kubectl", "--context", c.Context, "get", "nodes", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("kubectl get nodes (context %q): %w", c.Context, err)
	}
	ns, err := parseNodes([]byte(res.Stdout))
	if err != nil {
		return nil, err
	}
	c.nodes = ns
	return ns, nil
}

func parseNodes(stdout []byte) ([]Node, error) {
	var nl nodeList
	if err := json.Unmarshal(stdout, &nl); err != nil {
		return nil, fmt.Errorf("parse kubectl get nodes output: %w", err)
	}
	ns := make([]Node, 0, len(nl.Items))
	for _, item := range nl.Items {
		role := RoleWorker
		if _, ok := item.Metadata.Labels[controlPlaneLabel]; ok {
			role = RoleControlPlane
		}
		ns = append(ns, Node{
			Name:      item.Metadata.Name,
			Container: containerName(item.Spec.ProviderID, item.Metadata.Name),
			Role:      role,
		})
	}
	// Deterministic ordering: scenarios pair workers[i] with a GPU profile.
	sort.Slice(ns, func(i, j int) bool { return ns[i].Name < ns[j].Name })
	return ns, nil
}

// containerName reads the container out of Kind's provider ID, which has the
// form kind://docker/<cluster>/<container>. Anything else means the node was
// not provisioned by Kind, and the node name is the best guess available.
func containerName(providerID, nodeName string) string {
	if !strings.HasPrefix(providerID, "kind://") {
		return nodeName
	}
	if i := strings.LastIndex(providerID, "/"); i >= 0 && i+1 < len(providerID) {
		return providerID[i+1:]
	}
	return nodeName
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
