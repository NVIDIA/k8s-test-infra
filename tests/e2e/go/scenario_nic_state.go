//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
)

// agentReadyz is the node agent's /readyz body.
type agentReadyz struct {
	OK         bool                       `json:"ok"`
	Components map[string]json.RawMessage `json:"components"`
}

// nodeAgentReadyz reads the node agent's /readyz on pod through the API
// server's pod proxy. An ignored NIC declaration doesn't fail the probe; it
// shows as a "nics" component.
func nodeAgentReadyz(ctx context.Context, h *harness.Harness, pod string) (agentReadyz, error) {
	port, err := h.Kube.KubectlCombined(ctx, "get", "pod", "-n", nvmlMockNamespace, pod, "-o",
		`jsonpath={.spec.initContainers[?(@.name=="node-agent")].ports[?(@.name=="health")].containerPort}`+
			`{.spec.containers[?(@.name=="node-agent")].ports[?(@.name=="health")].containerPort}`)
	if err != nil {
		return agentReadyz{}, fmt.Errorf("read the node agent's health port on %s: %w: %s", pod, err, port)
	}
	body, err := h.Kube.KubectlCombined(ctx, "get", "--raw",
		fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%s/proxy/readyz", nvmlMockNamespace, pod, strings.TrimSpace(port)))
	if err != nil {
		return agentReadyz{}, fmt.Errorf("read /readyz on %s: %w: %s", pod, err, body)
	}
	var r agentReadyz
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return agentReadyz{}, fmt.Errorf("parse /readyz on %s: %w: %s", pod, err, body)
	}
	return r, nil
}

// waitNoNICError waits until every node agent's /readyz is OK and names no
// ignored NIC declaration. A profile without NIC slots has none to ignore.
//
// A hand-edited ConfigMap isn't tested here: Helm applies the ConfigMap
// server-side, so the edit would own the field and fail every later helm
// upgrade in the cluster. internal/agent/source covers a bad edit through
// the file source, Events included.
func waitNoNICError(ctx context.Context, h *harness.Harness) {
	GinkgoHelper()
	Eventually(func() error {
		pods, err := h.Kube.RunningPodNames(ctx, nvmlMockNamespace, nvmlMockSelector)
		if err != nil {
			return err
		}
		if len(pods) == 0 {
			return errors.New("no running nvml-mock pods")
		}
		for _, pod := range pods {
			r, err := nodeAgentReadyz(ctx, h, pod)
			if err != nil {
				return err
			}
			if !r.OK {
				return fmt.Errorf("%s: /readyz is not OK: %v", pod, r.Components)
			}
			if nics, has := r.Components["nics"]; has {
				return fmt.Errorf("%s: /readyz names an ignored NIC declaration: %s", pod, nics)
			}
		}
		return nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Succeed())
}
