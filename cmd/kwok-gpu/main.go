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
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"

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
	core  coreclient.CoreV1Interface
	racks mokkaclient.SGPURackInterface
}

func main() {
	kubeconfig := flag.String("kubeconfig", "", "kubeconfig file (default: in-cluster credentials)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *kubeconfig); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, kubeconfig string) error {
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
	c := controller{core: core, racks: mokka.SGPURacks()}
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

func (c controller) sync(ctx context.Context) error {
	nodes, err := c.core.Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	var errs []error
	for i := range nodes.Items {
		n := &nodes.Items[i]
		_, owned := n.Annotations[ledgerKey]
		eligible := n.Annotations["kwok.x-k8s.io/node"] == "fake" &&
			n.Labels["mokka.nvidia.com/node-type"] == "KWOK" && n.Labels["mokka.nvidia.com/sgpu-node"] == "true"
		if !eligible && !owned {
			continue
		}
		if err := c.syncNode(ctx, n, eligible, owned); err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", n.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (c controller) devices(ctx context.Context, n *corev1.Node) ([]string, error) {
	raw := n.Annotations[metadata.AssignmentAnnotation]
	if raw == "" {
		return nil, nil
	}
	a, err := assignment.DecodeAssignment(raw)
	if err != nil {
		return nil, err
	}
	rack, err := c.racks.Get(ctx, a.Rack.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	slot := rack.Spec.NodeByIndex(a.NodeIndex)
	if rack.DeletionTimestamp != nil || !projection.MatchesBinding(n, rack, slot) {
		return nil, nil
	}
	ids := make([]string, 0, len(slot.GPUs))
	for _, gpu := range slot.GPUs {
		ids = append(ids, gpu.UUID)
	}
	return ids, nil
}

func (c controller) syncNode(ctx context.Context, n *corev1.Node, eligible, owned bool) error {
	var ids []string
	var err error
	if eligible {
		ids, err = c.devices(ctx, n)
		if err != nil {
			return err
		}
	}
	if !owned {
		if len(ids) == 0 {
			return nil
		}
		_, capExists := n.Status.Capacity[gpuResource]
		_, allocExists := n.Status.Allocatable[gpuResource]
		if capExists || allocExists {
			return errors.New("node already advertises GPUs from another source")
		}
	}
	return c.reconcileDevices(ctx, n, ids)
}

func (c controller) reconcileDevices(ctx context.Context, n *corev1.Node, ids []string) error {
	pods, err := c.core.Pods("").List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("spec.nodeName", n.Name).String()})
	if err != nil {
		return err
	}
	n, next, err := c.reserve(ctx, n, ids, pods.Items)
	if err != nil {
		return err
	}
	n, err = c.publish(ctx, n, len(ids))
	if err != nil {
		return err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if err := c.publishPod(ctx, pod, next[pod.UID]); err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		_, err = c.patchNodeAnnotation(ctx, n, nil)
	}
	return err
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
