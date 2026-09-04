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
// binding constraints require ZERO new dependencies and an empty `git diff` on
// go.mod/go.sum, which rules out importing
// `k8s.io/client-go/tools/clientcmd` and `k8s.io/client-go/dynamic` (only
// clientcmd/api is already a dependency). Decoding `kubectl -o json` into
// typed structs keeps the assertions strongly typed (no jsonpath/jq
// string-fishing), threads context.Context into every call, and adds no
// dependency. The Execer transport is shell `kubectl exec` per the
// user-resolved decision regardless.
package kube

import (
	"context"
	"encoding/json"
	"errors"
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
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
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

type containerStatus struct {
	Name         string `json:"name"`
	RestartCount int    `json:"restartCount"`
}

type podObj struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase                 string            `json:"phase"`
		PodIP                 string            `json:"podIP"`
		ContainerStatuses     []containerStatus `json:"containerStatuses"`
		InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
	} `json:"status"`
}

// restarted reports whether any container of the pod has been restarted, which
// is the precondition for `kubectl logs --previous` having anything to return.
func (p podObj) restarted() bool {
	for _, group := range [][]containerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
		for _, cs := range group {
			if cs.RestartCount > 0 {
				return true
			}
		}
	}
	return false
}

type configMapObj struct {
	Metadata struct {
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
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
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
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
		ObservedGeneration     int64 `json:"observedGeneration"`
		DesiredNumberScheduled int   `json:"desiredNumberScheduled"`
		UpdatedNumberScheduled int   `json:"updatedNumberScheduled"`
		NumberReady            int   `json:"numberReady"`
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
		return "", errors.New("no nodes in cluster")
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

// NodeAnnotation returns a node annotation value and whether it was set.
func (c *Client) NodeAnnotation(ctx context.Context, node, key string) (string, bool, error) {
	var n nodeObj
	if err := c.getJSON(ctx, &n, "node", node); err != nil {
		return "", false, err
	}
	v, ok := n.Metadata.Annotations[key]
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

// ConfigMap is the subset of a ConfigMap the assertions need: the labels it
// carries and its data keys. Fetched by exact name, the same way a consumer
// doing a `Get` would see it — a List by label selector would not notice a
// wrong name, which is one of the fields under test.
type ConfigMap struct {
	Labels map[string]string
	Data   map[string]string
}

// GetConfigMap returns a single ConfigMap by exact name. The error surfaces
// kubectl's own NotFound, so a caller can tell "wrong name" from "wrong
// contents".
func (c *Client) GetConfigMap(ctx context.Context, ns, name string) (*ConfigMap, error) {
	var cm configMapObj
	if err := c.getJSON(ctx, &cm, "configmap", "-n", ns, name); err != nil {
		return nil, err
	}
	return &ConfigMap{Labels: cm.Metadata.Labels, Data: cm.Data}, nil
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

// rolledOutAndReady reports whether the DaemonSet's current spec is fully rolled
// out and every desired pod is ready. A ready count alone would also accept a
// DaemonSet that has not started rolling yet, whose ready pods still belong to
// the previous generation.
func (ds daemonSetObj) rolledOutAndReady() bool {
	// A status the controller has not caught up to describes the previous spec,
	// so it cannot answer whether this one rolled out.
	if ds.Status.ObservedGeneration < ds.Metadata.Generation {
		return false
	}
	d := ds.Status.DesiredNumberScheduled
	return d > 0 &&
		ds.Status.UpdatedNumberScheduled == d &&
		ds.Status.NumberReady == d
}

// DaemonSetReady reports whether every desired DaemonSet pod is ready and
// running the current spec.
func (c *Client) DaemonSetReady(ctx context.Context, ns, name string) (bool, error) {
	var ds daemonSetObj
	if err := c.getJSON(ctx, &ds, "daemonset", "-n", ns, name); err != nil {
		return false, err
	}
	return ds.rolledOutAndReady(), nil
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

// ResourceSliceDeviceCounts returns len(Devices) for every ResourceSlice
// currently published, pinned to the served resource.k8s.io/v1beta1. The DRA
// driver publishes one ResourceSlice per node with the mock's advertised GPU
// count, so per-slice counts are the load-bearing invariant — summing them
// blends node cardinality with per-node accuracy and hides regressions
// (e.g. one worker's mock silently short by a device).
func (c *Client) ResourceSliceDeviceCounts(ctx context.Context) ([]int, error) {
	var list struct {
		Items []struct {
			Spec struct {
				Devices []json.RawMessage `json:"devices"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, &list, "resourceslices.v1beta1.resource.k8s.io"); err != nil {
		return nil, err
	}
	counts := make([]int, len(list.Items))
	for i, it := range list.Items {
		counts[i] = len(it.Spec.Devices)
	}
	return counts, nil
}

// DescribePod returns `kubectl describe pod` output (failure classification,
// e.g. the DRA "empty device edits" string).
func (c *Client) DescribePod(ctx context.Context, ns, name string) (string, error) {
	res, err := c.kubectl(ctx, "describe", "pod", "-n", ns, name)
	return res.Combined(), err
}

// logsArgs builds the `kubectl logs` argv for a label selector.
//
// --all-containers is unconditional. Without it kubectl silently picks a
// multi-container pod's default container and prints `Defaulted container "x"
// out of: x, y`, so every sidecar's output is lost from the diagnostics dump.
// The nvml-mock pod is multi-container whenever an optional feature is enabled,
// and any sidecar added later inherits the same blind spot.
//
// --previous is opt-in: it errors when a container has no previous instance,
// which is the normal case. See PreviousLogs.
func logsArgs(ns, selector string, tail int, previous bool) []string {
	args := []string{
		"logs", "-n", ns,
		"-l", selector,
		"--all-containers=true", fmt.Sprintf("--tail=%d", tail),
	}
	if previous {
		args = append(args, "--previous")
	}
	return args
}

// PodLogs returns one container's logs from one pod. Diagnostics that need a
// specific container use this; Logs fans out across a selector instead.
func (c *Client) PodLogs(ctx context.Context, ns, pod, container string, tail int) (string, error) {
	res, err := c.kubectl(ctx, "logs", "-n", ns, pod, "-c", container, fmt.Sprintf("--tail=%d", tail))
	return res.Combined(), err
}

// Logs returns current pod logs for a label selector, across every container of
// every matching pod (best-effort diagnostics).
func (c *Client) Logs(ctx context.Context, ns, selector string, tail int) (string, error) {
	res, err := c.kubectl(ctx, logsArgs(ns, selector, tail, false)...)
	return res.Combined(), err
}

// PreviousLogs returns the logs of the PREVIOUS instance of every container of
// every matching pod — the only way to see why a container that is now
// restarting died.
//
// Callers must gate this on RestartedPods: kubectl fails the request outright
// when a container has no previous instance, so calling it unconditionally
// turns a working diagnostic into a failing one on every healthy pod. Treat a
// returned error as "no previous logs available", never as a spec failure.
func (c *Client) PreviousLogs(ctx context.Context, ns, selector string, tail int) (string, error) {
	res, err := c.kubectl(ctx, logsArgs(ns, selector, tail, true)...)
	return res.Combined(), err
}

// RestartedPods returns the names of pods matching selector that have at least
// one container with a non-zero restart count.
func (c *Client) RestartedPods(ctx context.Context, ns, selector string) ([]string, error) {
	var pl podList
	if err := c.getJSON(ctx, &pl, "pods", "-n", ns, "-l", selector); err != nil {
		return nil, err
	}
	var out []string
	for _, p := range pl.Items {
		if p.restarted() {
			out = append(out, p.Metadata.Name)
		}
	}
	return out, nil
}

// KubectlCombined runs an arbitrary kubectl subcommand and returns combined
// output (best-effort diagnostics).
func (c *Client) KubectlCombined(ctx context.Context, args ...string) (string, error) {
	res, err := c.kubectl(ctx, args...)
	return res.Combined(), err
}

// GetRawQuiet fetches an API path via `kubectl get --raw` without streaming the
// (potentially large) response body to the Ginkgo writer. The body is still
// captured and returned.
func (c *Client) GetRawQuiet(ctx context.Context, path string) (string, error) {
	full := append(c.base(), "get", "--raw", path)
	res, err := runner.RunQuiet(ctx, "kubectl", full...)
	return res.Combined(), err
}
