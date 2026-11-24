// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// kwok-gpu publishes Mokka whole-GPU rack assignments on existing KWOK Nodes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/assignment"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/metadata"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/projection"
	mokkaclient "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/typed/api/v1alpha1"
)

const (
	ledgerKey = "mokka.nvidia.com/kwok-gpu-reservations"
	podKey    = "mokka.nvidia.com/gpu-allocation"
)

type controller struct {
	core    coreclient.CoreV1Interface
	mokka   mokkaclient.MokkaV1alpha1Interface
	metrics *metricsEndpoint
}

func main() {
	kubeconfig := flag.String("kubeconfig", "", "kubeconfig file (default: in-cluster credentials)")
	listen := flag.String("metrics-listen", "", "listen address for simulated DCGM metrics (disabled when empty)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *kubeconfig, *listen); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, kubeconfig, listen string) error {
	logger, err := zap.NewProduction()
	if err != nil {
		return err
	}
	zap.ReplaceGlobals(logger)
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return err
	}
	core, err := coreclient.NewForConfig(config)
	if err != nil {
		return err
	}
	mokka, err := mokkaclient.NewForConfig(config)
	if err != nil {
		return err
	}
	c := controller{core: core, mokka: mokka}
	if listen != "" {
		endpoint, closeServer, err := serveMetrics(listen)
		if err != nil {
			return err
		}
		defer closeServer()
		c.metrics = endpoint
	}
	return c.poll(ctx)
}

func (c controller) poll(ctx context.Context) error {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if err := c.sync(ctx); err != nil && ctx.Err() == nil {
			zap.L().Warn("KWOK GPU reconciliation will retry", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

type inventoryView struct {
	racks    map[string]*mokkav1.SGPURack
	pods     map[string][]corev1.Pod
	profiles map[string]*mokkav1.SGPURackProfile
}

func (c controller) inventory(ctx context.Context) (*inventoryView, error) {
	view := &inventoryView{racks: map[string]*mokkav1.SGPURack{}, pods: map[string][]corev1.Pod{}, profiles: map[string]*mokkav1.SGPURackProfile{}}
	racks, err := c.mokka.SGPURacks().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range racks.Items {
		r := &racks.Items[i]
		view.racks[r.Name] = r
	}
	pods, err := c.core.Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName!="})
	if err != nil {
		return nil, err
	}
	for _, pod := range pods.Items {
		view.pods[pod.Spec.NodeName] = append(view.pods[pod.Spec.NodeName], pod)
	}
	if c.metrics != nil {
		profiles, err := c.mokka.SGPURackProfiles().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for i := range profiles.Items {
			p := &profiles.Items[i]
			view.profiles[p.Name] = p
		}
	}
	return view, nil
}

func (c controller) sync(ctx context.Context) error {
	nodes, err := c.core.Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	view, err := c.inventory(ctx)
	if err != nil {
		return err
	}
	var snapshot gpuMetrics
	var errs []error
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if err := c.syncNode(ctx, n, view, &snapshot); err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", n.Name, err))
		}
	}
	if c.metrics != nil && len(errs) == 0 {
		c.metrics.publish(&snapshot)
	}
	return errors.Join(errs...)
}

func (c controller) syncNode(ctx context.Context, n *corev1.Node, view *inventoryView, snapshot *gpuMetrics) error {
	rack, slot, err := boundSlot(n, view.racks)
	if err != nil {
		return err
	}
	_, owned := n.Annotations[ledgerKey]
	if !owned && slot == nil {
		return nil
	}
	if !owned && advertisesGPUs(n) {
		return errors.New("node already advertises GPUs from another source")
	}
	pods := view.pods[n.Name]
	next, err := c.reconcileDevices(ctx, n, slotDeviceIDs(slot), pods)
	if err != nil {
		return err
	}
	if c.metrics == nil || slot == nil {
		return nil
	}
	return snapshot.addBoundNode(n.Name, slot, rack.Spec.ProfileRef, view.profiles, pods, next)
}

func slotDeviceIDs(slot *mokkav1.SGPURackNode) []string {
	var ids []string
	if slot != nil {
		for _, gpu := range slot.GPUs {
			ids = append(ids, gpu.UUID)
		}
	}
	return ids
}

func advertisesGPUs(n *corev1.Node) bool {
	_, capacity := n.Status.Capacity[gpuResource]
	_, allocatable := n.Status.Allocatable[gpuResource]
	return capacity || allocatable
}

func boundSlot(n *corev1.Node, racks map[string]*mokkav1.SGPURack) (*mokkav1.SGPURack, *mokkav1.SGPURackNode, error) {
	if n.Annotations["kwok.x-k8s.io/node"] != "fake" || n.Labels["mokka.nvidia.com/node-type"] != "KWOK" || n.Labels["mokka.nvidia.com/sgpu-node"] != "true" {
		return nil, nil, nil
	}
	raw := n.Annotations[metadata.AssignmentAnnotation]
	if raw == "" {
		return nil, nil, nil
	}
	a, err := assignment.DecodeAssignment(raw)
	if err != nil {
		return nil, nil, err
	}
	r := racks[a.Rack.Name]
	if r == nil || r.DeletionTimestamp != nil {
		return nil, nil, nil
	}
	slot := r.Spec.NodeByIndex(a.NodeIndex)
	if !projection.MatchesBinding(n, r, slot) {
		return nil, nil, nil
	}
	return r, slot, nil
}

func (c controller) reconcileDevices(ctx context.Context, n *corev1.Node, ids []string, pods []corev1.Pod) (assignments, error) {
	n, next, err := c.reserve(ctx, n, ids, pods)
	if err != nil {
		return nil, err
	}
	n, err = c.publish(ctx, n, len(ids))
	if err != nil {
		return nil, err
	}
	for i := range pods {
		if err := c.publishPod(ctx, &pods[i], next[pods[i].UID]); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		_, err = c.patchNodeAnnotation(ctx, n, nil)
	}
	return next, err
}

func (c controller) reserve(ctx context.Context, n *corev1.Node, ids []string, pods []corev1.Pod) (*corev1.Node, assignments, error) {
	var previous assignments
	if raw, exists := n.Annotations[ledgerKey]; exists {
		if err := json.Unmarshal([]byte(raw), &previous); err != nil {
			return nil, nil, err
		}
	}
	next := allocate(ids, previous, pods)
	ledger, err := json.Marshal(next)
	if err != nil {
		return nil, nil, err
	}
	// Commit all reservations together before copying any result to a Pod.
	// Keep the ownership marker until capacity and Pod cleanup both succeed.
	if string(ledger) != n.Annotations[ledgerKey] {
		n, err = c.patchNodeAnnotation(ctx, n, string(ledger))
		if err != nil {
			return nil, nil, err
		}
	}
	return n, next, nil
}

func mergePatch(version string, fields map[string]any) ([]byte, error) {
	meta, ok := fields["metadata"].(map[string]any)
	if !ok {
		meta = map[string]any{}
		fields["metadata"] = meta
	}
	meta["resourceVersion"] = version
	return json.Marshal(fields)
}

func annotationPatch(version, key string, value any) ([]byte, error) {
	return mergePatch(version, map[string]any{"metadata": map[string]any{"annotations": map[string]any{key: value}}})
}

func (c controller) patchNodeAnnotation(ctx context.Context, n *corev1.Node, value any) (*corev1.Node, error) {
	data, err := annotationPatch(n.ResourceVersion, ledgerKey, value)
	if err != nil {
		return nil, err
	}
	return c.core.Nodes().Patch(ctx, n.Name, types.MergePatchType, data, metav1.PatchOptions{})
}

func (c controller) publish(ctx context.Context, n *corev1.Node, count int) (*corev1.Node, error) {
	capacity, hasCapacity := n.Status.Capacity[gpuResource]
	allocatable, hasAllocatable := n.Status.Allocatable[gpuResource]
	if count == 0 && !hasCapacity && !hasAllocatable || count > 0 && capacity.Value() == int64(count) && allocatable.Value() == int64(count) {
		return n, nil
	}
	var quantity any
	if count > 0 {
		quantity = strconv.Itoa(count)
	}
	resource := map[string]any{string(gpuResource): quantity}
	data, err := mergePatch(n.ResourceVersion, map[string]any{"status": map[string]any{"capacity": resource, "allocatable": resource}})
	if err != nil {
		return nil, err
	}
	return c.core.Nodes().Patch(ctx, n.Name, types.MergePatchType, data, metav1.PatchOptions{}, "status")
}

func (c controller) publishPod(ctx context.Context, pod *corev1.Pod, allocation containerDevices) error {
	old, exists := pod.Annotations[podKey]
	if len(allocation) == 0 && !exists {
		return nil
	}
	var value any
	if len(allocation) > 0 {
		data, err := json.Marshal(allocation)
		if err != nil {
			return err
		}
		if string(data) == old {
			return nil
		}
		value = string(data)
	}
	data, err := annotationPatch(pod.ResourceVersion, podKey, value)
	if err != nil {
		return err
	}
	_, err = c.core.Pods(pod.Namespace).Patch(ctx, pod.Name, types.MergePatchType, data, metav1.PatchOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
