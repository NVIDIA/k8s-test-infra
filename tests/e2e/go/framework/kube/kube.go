//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package kube provides Kubernetes access for assertions —
// Node/allocatable/pod-phase/DaemonSet/ResourceSlice — plus pod exec and
// apply. It is implemented on top of `kubectl ... -o json` (decoded into typed
// Go structs) and `kubectl exec/apply`, using kubectl's default kubeconfig with
// an explicit context.
//
// DELIBERATE DEVIATION from the proposed "client-go typed clientset": the
// clientset config loader `k8s.io/client-go/tools/clientcmd` and
// `k8s.io/client-go/dynamic` are NOT in vendor/modules.txt (only
// clientcmd/api is), so importing them would force a `go mod vendor` and grow
// vendor/. The binding constraints require ZERO new dependencies and an empty
// `git diff` on go.mod/go.sum/vendor. Decoding `kubectl -o json` into typed
// structs keeps the assertions strongly typed (no jsonpath/jq string-fishing),
// threads context.Context into every call, and adds no dependency. The Execer
// transport is shell `kubectl exec` per the user-resolved decision regardless.
package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

// GPUResourceName is the extended resource the device plugin / operator expose.
const GPUResourceName = "nvidia.com/gpu"

// Client runs kubectl against a specific context in the default kubeconfig.
type Client struct {
	Context string
}

// New returns a client. An empty context uses kubectl's current context.
func New(context string) (*Client, error) {
	return &Client{Context: context}, nil
}

func (c *Client) base() []string {
	if c.Context == "" {
		return nil
	}
	return []string{"--context", c.Context}
}

func (c *Client) kubectl(ctx context.Context, args ...string) (runner.Result, error) {
	full := append(c.base(), args...)
	return runner.Run(ctx, "kubectl", full...)
}

func (c *Client) getJSON(ctx context.Context, out any, args ...string) error {
	a := append(c.base(), "get", "-o", "json")
	a = append(a, args...)
	// Quiet: these reads are polled inside Eventually loops; their JSON bodies
	// are pure noise in `-v` output (the `+ kubectl ...` trace line still prints,
	// and the body is retained for CmdError on failure).
	res, err := runner.RunQuiet(ctx, "kubectl", a...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(res.Stdout), out); err != nil {
		return fmt.Errorf("decode `kubectl %v` json: %w", args, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Minimal typed views of the objects we read.
// ---------------------------------------------------------------------------

type objectMeta struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type nodeCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

type nodeObj struct {
	Metadata objectMeta `json:"metadata"`
	Status   struct {
		Allocatable map[string]string `json:"allocatable"`
		Conditions  []nodeCondition   `json:"conditions"`
	} `json:"status"`
}

type podObj struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
		PodIP string `json:"podIP"`
	} `json:"status"`
}

type configMapObj struct {
	Data map[string]string `json:"data"`
}

type podList struct {
	Items []podObj `json:"items"`
}

type nodeList struct {
	Items []nodeObj `json:"items"`
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type daemonSetObj struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Env []envVar `json:"env"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		DesiredNumberScheduled int `json:"desiredNumberScheduled"`
		NumberReady            int `json:"numberReady"`
	} `json:"status"`
}

type jobObj struct {
	Status struct {
		Succeeded int `json:"succeeded"`
	} `json:"status"`
}

// ---------------------------------------------------------------------------
// Typed reads
// ---------------------------------------------------------------------------

// FirstNodeName returns the first node's name.
func (c *Client) FirstNodeName(ctx context.Context) (string, error) {
	var nl nodeList
	if err := c.getJSON(ctx, &nl, "nodes"); err != nil {
		return "", err
	}
	if len(nl.Items) == 0 {
		return "", fmt.Errorf("no nodes in cluster")
	}
	return nl.Items[0].Metadata.Name, nil
}

// NodeLabel returns a node label value and whether it was set.
func (c *Client) NodeLabel(ctx context.Context, node, key string) (string, bool, error) {
	var n nodeObj
	if err := c.getJSON(ctx, &n, "node", node); err != nil {
		return "", false, err
	}
	v, ok := n.Metadata.Labels[key]
	return v, ok, nil
}

// NodeReady reports the node's Ready condition.
func (c *Client) NodeReady(ctx context.Context, node string) (bool, error) {
	var n nodeObj
	if err := c.getJSON(ctx, &n, "node", node); err != nil {
		return false, err
	}
	for _, cond := range n.Status.Conditions {
		if cond.Type == "Ready" {
			return cond.Status == "True", nil
		}
	}
	return false, nil
}

// AllocatableGPU returns the integer allocatable nvidia.com/gpu on a node.
func (c *Client) AllocatableGPU(ctx context.Context, node string) (int, error) {
	var n nodeObj
	if err := c.getJSON(ctx, &n, "node", node); err != nil {
		return 0, err
	}
	v, ok := n.Status.Allocatable[GPUResourceName]
	if !ok {
		return 0, nil
	}
	q, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("allocatable %s=%q not an integer: %w", GPUResourceName, v, err)
	}
	return q, nil
}

// PodPhase returns a pod's phase string.
func (c *Client) PodPhase(ctx context.Context, ns, name string) (string, error) {
	var p podObj
	if err := c.getJSON(ctx, &p, "pod", "-n", ns, name); err != nil {
		return "", err
	}
	return p.Status.Phase, nil
}

// PodIP returns a pod's IP.
func (c *Client) PodIP(ctx context.Context, ns, name string) (string, error) {
	var p podObj
	if err := c.getJSON(ctx, &p, "pod", "-n", ns, name); err != nil {
		return "", err
	}
	return p.Status.PodIP, nil
}

// FirstPodName returns the first pod matching the label selector.
func (c *Client) FirstPodName(ctx context.Context, ns, selector string) (string, error) {
	var pl podList
	if err := c.getJSON(ctx, &pl, "pods", "-n", ns, "-l", selector); err != nil {
		return "", err
	}
	if len(pl.Items) == 0 {
		return "", fmt.Errorf("no pods in ns %q matching %q", ns, selector)
	}
	return pl.Items[0].Metadata.Name, nil
}

// RunningPodNames returns the names of all Running pods matching the selector
// (used to pick distinct server/client pods for cross-node ibping).
func (c *Client) RunningPodNames(ctx context.Context, ns, selector string) ([]string, error) {
	var pl podList
	if err := c.getJSON(ctx, &pl, "pods", "-n", ns, "-l", selector); err != nil {
		return nil, err
	}
	var out []string
	for _, p := range pl.Items {
		if p.Status.Phase == "Running" {
			out = append(out, p.Metadata.Name)
		}
	}
	return out, nil
}

// PodNode returns the Kubernetes node a pod is scheduled on.
func (c *Client) PodNode(ctx context.Context, ns, name string) (string, error) {
	var p podObj
	if err := c.getJSON(ctx, &p, "pod", "-n", ns, name); err != nil {
		return "", err
	}
	return p.Spec.NodeName, nil
}

// CountConfigMaps returns the number of ConfigMaps in ns matching the selector
// (parity with the demo's `kubectl get cm -l run.ai/gpu-profile=true | wc -l`).
func (c *Client) CountConfigMaps(ctx context.Context, ns, selector string) (int, error) {
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := c.getJSON(ctx, &list, "configmaps", "-n", ns, "-l", selector); err != nil {
		return 0, err
	}
	return len(list.Items), nil
}

// ConfigMapData returns a single key from a ConfigMap's data field.
func (c *Client) ConfigMapData(ctx context.Context, ns, name, key string) (string, error) {
	var cm configMapObj
	if err := c.getJSON(ctx, &cm, "configmap", "-n", ns, name); err != nil {
		return "", err
	}
	v, ok := cm.Data[key]
	if !ok {
		return "", fmt.Errorf("configmap %s/%s missing data key %q", ns, name, key)
	}
	return v, nil
}

// DaemonSetReady reports whether all desired DaemonSet pods are ready.
func (c *Client) DaemonSetReady(ctx context.Context, ns, name string) (bool, error) {
	var ds daemonSetObj
	if err := c.getJSON(ctx, &ds, "daemonset", "-n", ns, name); err != nil {
		return false, err
	}
	d := ds.Status.DesiredNumberScheduled
	return d > 0 && ds.Status.NumberReady == d, nil
}

// DaemonSetContainerEnv returns the value of an env var on the DaemonSet's
// first container (parity with reading MOCK_FABRICMANAGER off the deployed
// daemonset). Returns ("", false, nil) when unset.
func (c *Client) DaemonSetContainerEnv(ctx context.Context, ns, name, envName string) (string, bool, error) {
	var ds daemonSetObj
	if err := c.getJSON(ctx, &ds, "daemonset", "-n", ns, name); err != nil {
		return "", false, err
	}
	if len(ds.Spec.Template.Spec.Containers) == 0 {
		return "", false, fmt.Errorf("daemonset %s/%s has no containers", ns, name)
	}
	for _, e := range ds.Spec.Template.Spec.Containers[0].Env {
		if e.Name == envName {
			return e.Value, true, nil
		}
	}
	return "", false, nil
}

// JobComplete reports whether a Job has at least one successful completion.
func (c *Client) JobComplete(ctx context.Context, ns, name string) (bool, error) {
	var job jobObj
	if err := c.getJSON(ctx, &job, "job", "-n", ns, name); err != nil {
		return false, err
	}
	return job.Status.Succeeded > 0, nil
}

// ---------------------------------------------------------------------------
// exec / apply / ResourceSlice
// ---------------------------------------------------------------------------

// PodRef identifies a pod (and optional container) for exec.
type PodRef struct {
	Namespace string
	Pod       string
	Container string
}

// Exec runs argv in a pod via `kubectl exec`.
func (c *Client) Exec(ctx context.Context, ref PodRef, argv ...string) (runner.Result, error) {
	return c.kubectl(ctx, execArgs(ref, argv...)...)
}

// ExecQuiet is Exec without streaming stdout to the Ginkgo writer. The output is
// still captured for assertions and command errors.
func (c *Client) ExecQuiet(ctx context.Context, ref PodRef, argv ...string) (runner.Result, error) {
	full := append(c.base(), execArgs(ref, argv...)...)
	return runner.RunQuiet(ctx, "kubectl", full...)
}

// ExecTruncated is Exec but streams only the first maxStdoutLines stdout lines.
// Full stdout is still captured for assertions and command errors.
func (c *Client) ExecTruncated(ctx context.Context, ref PodRef, maxStdoutLines int, argv ...string) (runner.Result, error) {
	full := append(c.base(), execArgs(ref, argv...)...)
	return runner.RunTruncated(ctx, maxStdoutLines, "kubectl", full...)
}

func execArgs(ref PodRef, argv ...string) []string {
	args := []string{"exec"}
	if ref.Namespace != "" {
		args = append(args, "-n", ref.Namespace)
	}
	args = append(args, ref.Pod)
	if ref.Container != "" {
		args = append(args, "-c", ref.Container)
	}
	args = append(args, "--")
	args = append(args, argv...)
	return args
}

// ExecSh runs `sh -c shCmd` in a pod via `kubectl exec`.
func (c *Client) ExecSh(ctx context.Context, ref PodRef, shCmd string) (runner.Result, error) {
	return c.Exec(ctx, ref, "sh", "-c", shCmd)
}

// Apply applies a manifest via `kubectl apply -f -`.
func (c *Client) Apply(ctx context.Context, manifest []byte) error {
	full := append(c.base(), "apply", "-f", "-")
	_, err := runner.RunInput(ctx, string(manifest), "kubectl", full...)
	return err
}

// Delete deletes manifest objects, ignoring not-found.
func (c *Client) Delete(ctx context.Context, manifest []byte) error {
	full := append(c.base(), "delete", "--ignore-not-found", "-f", "-")
	_, err := runner.RunInput(ctx, string(manifest), "kubectl", full...)
	return err
}

// DeletePodsByLabel deletes pods matching selector in ns, ignoring not-found.
func (c *Client) DeletePodsByLabel(ctx context.Context, ns, selector string) error {
	_, err := c.kubectl(ctx, "delete", "pods", "-n", ns, "-l", selector, "--ignore-not-found")
	return err
}

// ResourceSliceGPUTotal sums devices across all ResourceSlices, pinned to the
// served resource.k8s.io/v1beta1 (matches kind-dra-config.yaml).
func (c *Client) ResourceSliceGPUTotal(ctx context.Context) (int, error) {
	var list struct {
		Items []struct {
			Spec struct {
				Devices []json.RawMessage `json:"devices"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, &list, "resourceslices.v1beta1.resource.k8s.io"); err != nil {
		return 0, err
	}
	total := 0
	for _, it := range list.Items {
		total += len(it.Spec.Devices)
	}
	return total, nil
}

// DescribePod returns `kubectl describe pod` output (failure classification,
// e.g. the DRA "empty device edits" string).
func (c *Client) DescribePod(ctx context.Context, ns, name string) (string, error) {
	res, err := c.kubectl(ctx, "describe", "pod", "-n", ns, name)
	return res.Combined(), err
}

// Logs returns pod logs for a label selector (best-effort diagnostics).
func (c *Client) Logs(ctx context.Context, ns, selector string, tail int) (string, error) {
	res, err := c.kubectl(ctx, "logs", "-n", ns, "-l", selector, fmt.Sprintf("--tail=%d", tail))
	return res.Combined(), err
}

// KubectlCombined runs an arbitrary kubectl subcommand and returns combined
// output (best-effort diagnostics).
func (c *Client) KubectlCombined(ctx context.Context, args ...string) (string, error) {
	res, err := c.kubectl(ctx, args...)
	return res.Combined(), err
}
