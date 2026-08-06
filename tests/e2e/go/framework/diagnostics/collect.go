//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package diagnostics mirrors the "Collect logs on failure" blocks of the bash
// jobs: on a failed spec it dumps cluster state to an artifacts directory that
// CI uploads. Everything is best-effort — diagnostics must never panic or fail
// a spec.
package diagnostics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// Collector writes diagnostics under a per-spec directory.
type Collector struct {
	Dir               string
	Kube              *kube.Client
	Clust             *cluster.Cluster
	NvmlMockNamespace string
}

// New returns a Collector writing under baseDir/<sub...>.
func New(baseDir string, k *kube.Client, c *cluster.Cluster, sub ...string) *Collector {
	parts := append([]string{baseDir}, sub...)
	return &Collector{Dir: filepath.Join(parts...), Kube: k, Clust: c}
}

func (c *Collector) write(name, content string) {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "diagnostics: mkdir %q: %v\n", c.Dir, err)
		return
	}
	p := filepath.Join(c.Dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "diagnostics: write %q: %v\n", p, err)
	}
}

// Kubectl dumps an arbitrary kubectl invocation to name.
func (c *Collector) Kubectl(ctx context.Context, name string, args ...string) {
	if c.Kube == nil {
		return
	}
	out, _ := c.Kube.KubectlCombined(ctx, args...)
	c.write(name, out)
}

// PodLogs dumps <name>-logs.txt holding the current logs of every container of
// every pod matching selector.
//
// When a container has restarted it also dumps <name>-logs-previous.txt, which
// is where the cause of a crash loop actually lives — the current instance of a
// restarting container has usually logged nothing yet. That second dump is
// gated on an observed restart because `kubectl logs --previous` errors when no
// previous instance exists, and it is still best-effort on top of the gate: a
// pod where only one of several containers restarted can fail the request even
// though the gate passed.
func (c *Collector) PodLogs(ctx context.Context, name, ns, selector string, tail int) {
	if c.Kube == nil {
		return
	}
	if out, err := c.Kube.Logs(ctx, ns, selector, tail); err == nil {
		c.write(name+"-logs.txt", out)
	}
	restarted, err := c.Kube.RestartedPods(ctx, ns, selector)
	if err != nil || len(restarted) == 0 {
		return
	}
	if out, err := c.Kube.PreviousLogs(ctx, ns, selector, tail); err == nil {
		c.write(name+"-logs-previous.txt", out)
	}
}

// Common dumps the dump set shared by every job's failure block.
func (c *Collector) Common(ctx context.Context) {
	if c.Kube == nil {
		return
	}
	c.Kubectl(ctx, "pods-all.txt", "get", "pods", "-A", "-o", "wide")
	c.Kubectl(ctx, "nodes-describe.txt", "describe", "nodes")
	c.Kubectl(ctx, "events.txt", "get", "events", "-A", "--sort-by=.lastTimestamp")
	if c.NvmlMockNamespace == "" {
		return
	}
	c.PodLogs(ctx, "nvml-mock", c.NvmlMockNamespace, "app.kubernetes.io/name=nvml-mock", 100)
}
