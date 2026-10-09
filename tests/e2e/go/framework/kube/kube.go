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
	"slices"
	"strconv"
	"strings"

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
	// A terminating pod reports phase Running until its containers exit, so the
	// phase alone does not distinguish one an exec can reach.
	DeletionTimestamp string `json:"deletionTimestamp"`
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

type templateContainer struct {
	Name string   `json:"name"`
	Env  []envVar `json:"env"`
}

type daemonSetObj struct {
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				InitContainers []templateContainer `json:"initContainers"`
				Containers     []templateContainer `json:"containers"`
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

// RunningPodOnNode returns a pod matching the selector that is on node and that
// an exec can reach: Running and not terminating.
//
// FirstPodName is the wrong tool for a DaemonSet whose pod a caller means to
// exec into. It returns Items[0] with no filter, so a Terminating or Pending pod
// matches as readily as the Running one, and Items[0] is not necessarily on the
// node the surrounding specs assert labels about — the spec could report about
// hardware it never checked.
func (c *Client) RunningPodOnNode(ctx context.Context, ns, selector, node string) (string, error) {
	var pl podList
	if err := c.getJSON(ctx, &pl, "pods", "-n", ns, "-l", selector, "--field-selector", "spec.nodeName="+node); err != nil {
		return "", err
	}
	for _, p := range pl.Items {
		if p.Status.Phase == "Running" && p.Metadata.DeletionTimestamp == "" {
			return p.Metadata.Name, nil
		}
	}
	return "", fmt.Errorf("no running pod in ns %q matching %q on node %q", ns, selector, node)
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

type nodeFeatureObj struct {
	Spec struct {
		Features struct {
			Attributes map[string]struct {
				Elements map[string]string `json:"elements"`
			} `json:"attributes"`
		} `json:"features"`
	} `json:"spec"`
}

// NodeFeatureAttribute returns the elements of one attribute feature, such as
// system.dmiid, from the NodeFeature object an NFD worker in ns publishes for
// node. The worker names that object after the node. A feature the worker did
// not publish yields an empty map; a missing object yields kubectl's NotFound.
func (c *Client) NodeFeatureAttribute(ctx context.Context, ns, node, feature string) (map[string]string, error) {
	var nf nodeFeatureObj
	if err := c.getJSON(ctx, &nf, "nodefeature", "-n", ns, node); err != nil {
		return nil, err
	}
	return nf.Spec.Features.Attributes[feature].Elements, nil
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

// DaemonSetContainerEnv returns the value of an env var on the named container
// of the DaemonSet (parity with reading MOCK_FABRICMANAGER off the deployed
// daemonset). Returns ("", false, nil) when unset.
func (c *Client) DaemonSetContainerEnv(ctx context.Context, ns, name, container, envName string) (string, bool, error) {
	var ds daemonSetObj
	if err := c.getJSON(ctx, &ds, "daemonset", "-n", ns, name); err != nil {
		return "", false, err
	}
	value, found, err := ds.containerEnv(container, envName)
	if err != nil {
		return "", false, fmt.Errorf("daemonset %s/%s: %w", ns, name, err)
	}
	return value, found, nil
}

// containerEnv looks the container up by name, among init containers too: with
// the NRI plugin on, the chart runs the node agent as a native sidecar, which
// is a restartable init container. A missing container is an error, so a
// layout change cannot read as the variable being unset.
func (ds daemonSetObj) containerEnv(container, envName string) (string, bool, error) {
	spec := ds.Spec.Template.Spec
	for _, ctr := range slices.Concat(spec.InitContainers, spec.Containers) {
		if ctr.Name != container {
			continue
		}
		for _, e := range ctr.Env {
			if e.Name == envName {
				return e.Value, true, nil
			}
		}
		return "", false, nil
	}
	return "", false, fmt.Errorf("no container %q", container)
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

// DRAAllocatedGPUUUIDs returns the UUIDs of the devices the scheduler
// allocated to the ResourceClaims one container uses, resolved through the
// ResourceSlices the driver published. An empty ref.Container selects the
// pod's first container. It is the DRA counterpart of reading
// NVIDIA_VISIBLE_DEVICES for a device plugin allocation: the source of truth
// for which GPUs the container was given, independent of what it then sees. It
// reads the API server's preferred resource.k8s.io version and accepts both
// the v1beta1 and v1 device shapes, so it keeps working once v1beta1 is no
// longer served.
func (c *Client) DRAAllocatedGPUUUIDs(ctx context.Context, ref PodRef) ([]string, error) {
	refs, err := c.draAllocatedDevices(ctx, ref)
	if err != nil {
		return nil, err
	}
	var published draSliceList
	if err := c.getJSON(ctx, &published, "resourceslices.resource.k8s.io"); err != nil {
		return nil, err
	}
	return published.uuidsOf(refs)
}

// draDeviceRef names one device the way a ResourceClaim allocation does.
type draDeviceRef struct{ driver, pool, device string }

// draContainer is a pod container's references to the pod's claims.
type draContainer struct {
	Name      string `json:"name"`
	Resources struct {
		Claims []struct {
			Name    string `json:"name"`
			Request string `json:"request"`
		} `json:"claims"`
	} `json:"resources"`
}

// draPodObj is the part of a pod that ties its containers to ResourceClaims.
type draPodObj struct {
	Spec struct {
		Containers     []draContainer `json:"containers"`
		ResourceClaims []struct {
			Name              string  `json:"name"`
			ResourceClaimName *string `json:"resourceClaimName"`
		} `json:"resourceClaims"`
	} `json:"spec"`
	Status struct {
		ResourceClaimStatuses []struct {
			Name              string  `json:"name"`
			ResourceClaimName *string `json:"resourceClaimName"`
		} `json:"resourceClaimStatuses"`
	} `json:"status"`
}

// draClaimUse is one ResourceClaim a container uses, narrowed to one of its
// requests when the container names it.
type draClaimUse struct{ claim, request string }

// claimUses resolves the claims a container references to ResourceClaim
// names. A pod-level claim either names an existing ResourceClaim, or comes
// from a template, in which case only the pod status records the claim that
// was generated for it. An empty container selects the first one.
func (p draPodObj) claimUses(container string) ([]draClaimUse, error) {
	ctrs := p.Spec.Containers
	idx := 0
	if container != "" {
		idx = slices.IndexFunc(ctrs, func(ctr draContainer) bool { return ctr.Name == container })
	}
	if idx < 0 || idx >= len(ctrs) {
		return nil, fmt.Errorf("no container %q", container)
	}
	var uses []draClaimUse
	for _, ref := range ctrs[idx].Resources.Claims {
		claim, err := p.claimName(ref.Name)
		if err != nil {
			return nil, err
		}
		uses = append(uses, draClaimUse{claim, ref.Request})
	}
	return uses, nil
}

func (p draPodObj) claimName(podClaim string) (string, error) {
	for _, rc := range p.Spec.ResourceClaims {
		if rc.Name != podClaim {
			continue
		}
		if rc.ResourceClaimName != nil {
			return *rc.ResourceClaimName, nil
		}
		for _, st := range p.Status.ResourceClaimStatuses {
			if st.Name == podClaim && st.ResourceClaimName != nil {
				return *st.ResourceClaimName, nil
			}
		}
		return "", fmt.Errorf("pod claim %q has no generated ResourceClaim yet", podClaim)
	}
	return "", fmt.Errorf("pod declares no claim %q", podClaim)
}

type draClaimObj struct {
	Status struct {
		Allocation *struct {
			Devices struct {
				Results []struct {
					Request string `json:"request"`
					Driver  string `json:"driver"`
					Pool    string `json:"pool"`
					Device  string `json:"device"`
				} `json:"results"`
			} `json:"devices"`
		} `json:"allocation"`
	} `json:"status"`
}

// allocatedDevices returns the devices allocated to the claim, or to one of
// its requests when request is set, and false while the scheduler has not
// allocated the claim yet. A device chosen for a subrequest of a
// prioritized-list request reports its request as "<request>/<subrequest>".
func (cl draClaimObj) allocatedDevices(request string) ([]draDeviceRef, bool) {
	if cl.Status.Allocation == nil {
		return nil, false
	}
	var refs []draDeviceRef
	for _, r := range cl.Status.Allocation.Devices.Results {
		if request != "" && r.Request != request && !strings.HasPrefix(r.Request, request+"/") {
			continue
		}
		refs = append(refs, draDeviceRef{r.Driver, r.Pool, r.Device})
	}
	return refs, true
}

type draSliceList struct {
	Items []struct {
		Spec struct {
			Driver string `json:"driver"`
			Pool   struct {
				Name string `json:"name"`
			} `json:"pool"`
			Devices []struct {
				Name string `json:"name"`
				// v1beta1 nests attributes under basic; v1 has them on the
				// device itself.
				Basic struct {
					Attributes draAttributes `json:"attributes"`
				} `json:"basic"`
				Attributes draAttributes `json:"attributes"`
			} `json:"devices"`
		} `json:"spec"`
	} `json:"items"`
}

// uuidsOf resolves each device to the uuid attribute its slice publishes.
// Device names repeat across pools, so a device is matched on driver, pool and
// name together.
func (l draSliceList) uuidsOf(refs []draDeviceRef) ([]string, error) {
	published := map[draDeviceRef]string{}
	for _, it := range l.Items {
		for _, d := range it.Spec.Devices {
			attrs := d.Attributes
			if attrs == nil {
				attrs = d.Basic.Attributes
			}
			if a, ok := attrs["uuid"]; ok && a.String != nil {
				published[draDeviceRef{it.Spec.Driver, it.Spec.Pool.Name, d.Name}] = *a.String
			}
		}
	}
	uuids := make([]string, 0, len(refs))
	for _, ref := range refs {
		uuid, ok := published[ref]
		if !ok {
			return nil, fmt.Errorf("no uuid attribute for device %s in pool %s of driver %s", ref.device, ref.pool, ref.driver)
		}
		uuids = append(uuids, uuid)
	}
	return uuids, nil
}

func (c *Client) draAllocatedDevices(ctx context.Context, ref PodRef) ([]draDeviceRef, error) {
	var p draPodObj
	if err := c.getJSON(ctx, &p, "pod", "-n", ref.Namespace, ref.Pod); err != nil {
		return nil, err
	}
	uses, err := p.claimUses(ref.Container)
	if err != nil {
		return nil, fmt.Errorf("pod %s/%s: %w", ref.Namespace, ref.Pod, err)
	}
	var refs []draDeviceRef
	for _, use := range uses {
		var claim draClaimObj
		if err := c.getJSON(ctx, &claim, "resourceclaims.resource.k8s.io", "-n", ref.Namespace, use.claim); err != nil {
			return nil, err
		}
		devices, ok := claim.allocatedDevices(use.request)
		if !ok {
			return nil, fmt.Errorf("ResourceClaim %s/%s is not allocated", ref.Namespace, use.claim)
		}
		refs = append(refs, devices...)
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("pod %s/%s holds no allocated ResourceClaim device", ref.Namespace, ref.Pod)
	}
	return refs, nil
}

// draAttributes is a ResourceSlice device's attribute map; only string values
// are read.
type draAttributes map[string]struct {
	String *string `json:"string"`
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
