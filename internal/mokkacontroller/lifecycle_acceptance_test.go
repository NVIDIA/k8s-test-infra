// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"

	controllerprojection "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/projection"
	controllerack "github.com/NVIDIA/k8s-test-infra/internal/mokkacontroller/rack"
	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/pkg/apis/mokka/v1alpha1"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/pkg/mokka/materialize"
)

func TestControllerLifecycleAcceptance(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := newAcceptanceNodeClient()
	mokka := mokkafake.NewSimpleClientset()
	installAcceptanceAPIReactors(t, mokka)
	controller, err := newForNodes(nodes, mokka, Options{Workers: 2, StatusDebounce: 0})
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Eventually(t, controller.Ready, 5*time.Second, 10*time.Millisecond)

	profile := acceptanceProfile(2)
	inventory := acceptanceInventory()
	nodes.create(acceptanceNode("node-a", "node-a-v1", 1))
	nodes.create(acceptanceNode("node-b", "node-b-v1", 2))
	_, err = mokka.MokkaV1alpha1().SGPUProfiles().Create(ctx, profile, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Create(ctx, inventory, metav1.CreateOptions{})
	require.NoError(t, err)

	rackName := materialize.RackName(inventory.Name, inventory.UID, "compute", 0)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(t, mokka, rackName)
		return len(rack.Spec.Slots) == 2 &&
			rack.Spec.Slots[0].NodeRef != nil && rack.Spec.Slots[0].NodeRef.UID == "node-a-v1" &&
			rack.Spec.Slots[1].NodeRef != nil && rack.Spec.Slots[1].NodeRef.UID == "node-b-v1" &&
			nodeIsProjected(nodes.snapshot("node-a"), "node-a-v1") &&
			nodeIsProjected(nodes.snapshot("node-b"), "node-b-v1")
	}, 10*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		rack := getAcceptanceRack(t, mokka, rackName)
		return current.Status.ObservedGeneration == current.Generation &&
			slices.Contains(current.Finalizers, controllerack.InventoryFinalizer) &&
			current.Status.Capacity.NodeSlots == 2 &&
			current.Status.Usage.AllocatedNodes == 2 &&
			current.Status.Usage.ProjectedNodes == 2 &&
			slices.Contains(rack.Finalizers, controllerack.RackFinalizer) &&
			rack.Status.AssignedSlots == 2 && rack.Status.ProjectedSlots == 2
	}, 10*time.Second, 20*time.Millisecond)

	currentInventory, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	currentInventory.Spec.RackGroups[0].Count = 0
	currentInventory.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, currentInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		return !nodeHasProjection(nodes.snapshot("node-a")) &&
			!nodeHasProjection(nodes.snapshot("node-b"))
	}, 10*time.Second, 20*time.Millisecond, "capacity shrink must wait for projection cleanup")

	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil || current.Status.Capacity.NodeSlots != 0 ||
			current.Status.Usage.AllocatedNodes != 0 || current.Status.Usage.ProjectedNodes != 0 {
			return false
		}
		racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
		return err == nil && len(racks.Items) == 0 && current.Status.Capacity.NodeSlots == 0 &&
			current.Status.Usage.AllocatedNodes == 0 && current.Status.Usage.ProjectedNodes == 0
	}, 10*time.Second, 20*time.Millisecond)

	nodes.delete("node-b")
	nodes.replace("node-a", acceptanceNode("node-a", "node-a-v2", 3))
	currentInventory, err = mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	currentInventory.Spec.RackGroups[0].Count = 1
	currentInventory.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, currentInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		rack := getAcceptanceRack(t, mokka, rackName)
		return len(rack.Spec.Slots) == 2 && rack.Spec.Slots[0].NodeRef != nil &&
			rack.Spec.Slots[0].NodeRef.UID == "node-a-v2" &&
			nodeIsProjected(nodes.snapshot("node-a"), "node-a-v2")
	}, 10*time.Second, 20*time.Millisecond, "same-name replacement must receive a new exact-UID binding")

	currentInventory, err = mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	currentInventory.Spec.RackGroups[0].Count = 0
	currentInventory.Generation++
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, currentInventory, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
		return err == nil && len(racks.Items) == 0 && !nodeHasProjection(nodes.snapshot("node-a"))
	}, 10*time.Second, 20*time.Millisecond)

	deleting, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
	require.NoError(t, err)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	_, err = mokka.MokkaV1alpha1().SGPUInventories().Update(ctx, deleting, metav1.UpdateOptions{})
	require.NoError(t, err)
	controller.queues.inventories.Add(inventory.Name)
	require.Eventually(t, func() bool {
		current, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		if err != nil || len(current.Finalizers) != 0 || nodeHasProjection(nodes.snapshot("node-a")) {
			return false
		}
		racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
		return err == nil && len(racks.Items) == 0
	}, 10*time.Second, 20*time.Millisecond, "inventory deletion must clean Nodes and generated racks before releasing its finalizer")
}

func installAcceptanceAPIReactors(t *testing.T, client *mokkafake.Clientset) {
	t.Helper()
	client.PrependReactor("create", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		created := action.(k8stesting.CreateAction).GetObject().(*mokkav1alpha1.SGPURack).DeepCopy()
		created.UID = types.UID("uid-" + created.Name)
		created.ResourceVersion = "1"
		err := client.Tracker().Create(mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks"), created, "")
		return true, created, err
	})
	client.PrependReactor("update", "sgpuracks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "status" {
			return false, nil, nil
		}
		candidate := action.(k8stesting.UpdateAction).GetObject().(*mokkav1alpha1.SGPURack)
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuracks")
		stored, err := client.Tracker().Get(resource, "", candidate.Name)
		if err != nil {
			return true, nil, err
		}
		updated := stored.(*mokkav1alpha1.SGPURack).DeepCopy()
		updated.Status = candidate.Status
		err = client.Tracker().Update(resource, updated, "")
		return true, updated, err
	})
	client.PrependReactor("update", "sgpuinventories", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "status" {
			return false, nil, nil
		}
		candidate := action.(k8stesting.UpdateAction).GetObject().(*mokkav1alpha1.SGPUInventory)
		resource := mokkav1alpha1.SchemeGroupVersion.WithResource("sgpuinventories")
		stored, err := client.Tracker().Get(resource, "", candidate.Name)
		if err != nil {
			return true, nil, err
		}
		updated := stored.(*mokkav1alpha1.SGPUInventory).DeepCopy()
		updated.Status = candidate.Status
		err = client.Tracker().Update(resource, updated, "")
		return true, updated, err
	})
}

func acceptanceProfile(nodesPerRack int32) *mokkav1alpha1.SGPUProfile {
	return &mokkav1alpha1.SGPUProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "acceptance-profile", UID: "acceptance-profile-uid", Generation: 1, ResourceVersion: "1",
		},
		Spec: mokkav1alpha1.SGPUProfileSpec{
			Rack: mokkav1alpha1.SGPUProfileRack{NodesPerRack: nodesPerRack},
			Node: mokkav1alpha1.SGPUProfileNode{
				GPUs: mokkav1alpha1.SGPUHardware{Count: 1},
				Topology: mokkav1alpha1.SGPUNodeTopology{
					GPUSlots: []mokkav1alpha1.SGPUGPUSlot{{Index: 0, PCIAddress: "0000:01:00.0", RootComplex: "pci0000:00"}},
					GPUFabric: &mokkav1alpha1.SGPUGPUFabric{
						Type: "NVLink", Domain: mokkav1alpha1.SGPUGPUFabricDomain{Scope: "Rack", GPUCount: nodesPerRack},
					},
				},
			},
			Software: mokkav1alpha1.SGPUSoftware{DriverVersion: "580.1", NVMLVersion: "13", CUDAVersion: "13.1"},
		},
	}
}

func acceptanceInventory() *mokkav1alpha1.SGPUInventory {
	return &mokkav1alpha1.SGPUInventory{
		TypeMeta: metav1.TypeMeta{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPUInventory"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "acceptance", UID: "acceptance-inventory-uid", Generation: 1, ResourceVersion: "1",
		},
		Spec: mokkav1alpha1.SGPUInventorySpec{RackGroups: []mokkav1alpha1.SGPURackGroup{{
			ID: "compute", Count: 1,
			ProfileRef: corev1.LocalObjectReference{Name: "acceptance-profile"},
			Placement: &mokkav1alpha1.SGPUPlacement{NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "acceptance"},
			}},
		}}},
	}
}

func acceptanceNode(name string, uid types.UID, created int64) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: name, UID: uid, ResourceVersion: "1",
		CreationTimestamp: metav1.NewTime(time.Unix(created, 0)),
		Labels:            map[string]string{allocate.EligibleNodeLabel: "true", "pool": "acceptance"},
	}}
}

func getAcceptanceRack(t *testing.T, client *mokkafake.Clientset, name string) *mokkav1alpha1.SGPURack {
	t.Helper()
	rack, err := client.MokkaV1alpha1().SGPURacks().Get(context.Background(), name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return &mokkav1alpha1.SGPURack{}
	}
	require.NoError(t, err)
	return rack
}

func nodeHasProjection(node *corev1.Node) bool {
	return node != nil && (node.Labels[controllerprojection.AssignedLabel] != "" ||
		node.Labels[controllerprojection.CliqueLabel] != "" ||
		node.Annotations[controllerprojection.AssignmentAnnotation] != "")
}

func nodeIsProjected(node *corev1.Node, uid types.UID) bool {
	if node == nil || node.Labels[controllerprojection.AssignedLabel] != "true" ||
		node.Annotations[controllerprojection.AssignmentAnnotation] == "" {
		return false
	}
	assignment, err := controllerprojection.DecodeAssignment(node.Annotations[controllerprojection.AssignmentAnnotation])
	return err == nil && assignment.NodeUID == uid
}

type acceptanceNodeClient struct {
	corev1client.NodeInterface
	mu      sync.Mutex
	nodes   map[string]*corev1.Node
	watcher *watch.RaceFreeFakeWatcher
	nextRV  int64
}

func newAcceptanceNodeClient() *acceptanceNodeClient {
	return &acceptanceNodeClient{nodes: make(map[string]*corev1.Node), nextRV: 1}
}

func (c *acceptanceNodeClient) List(context.Context, metav1.ListOptions) (*corev1.NodeList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := &corev1.NodeList{}
	for _, node := range c.nodes {
		if node.Labels[allocate.EligibleNodeLabel] == "true" {
			list.Items = append(list.Items, *node.DeepCopy())
		}
	}
	return list, nil
}

func (c *acceptanceNodeClient) Watch(_ context.Context, options metav1.ListOptions) (watch.Interface, error) {
	c.mu.Lock()
	if c.watcher == nil {
		c.watcher = watch.NewRaceFreeFake()
	}
	watcher := c.watcher
	initial := make([]*corev1.Node, 0, len(c.nodes))
	if options.SendInitialEvents != nil && *options.SendInitialEvents {
		for _, node := range c.nodes {
			if node.Labels[allocate.EligibleNodeLabel] == "true" {
				initial = append(initial, node.DeepCopy())
			}
		}
	}
	c.mu.Unlock()
	if options.SendInitialEvents != nil && *options.SendInitialEvents {
		go func() {
			for _, node := range initial {
				watcher.Add(node)
			}
			watcher.Action(watch.Bookmark, &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				ResourceVersion: "1", Annotations: map[string]string{metav1.InitialEventsAnnotationKey: "true"},
			}})
		}()
	}
	return watcher, nil
}

func (c *acceptanceNodeClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*corev1.Node, error) {
	node := c.snapshot(name)
	if node == nil {
		return nil, apierrors.NewNotFound(corev1.Resource("nodes"), name)
	}
	return node, nil
}

func (c *acceptanceNodeClient) Patch(
	_ context.Context,
	name string,
	patchType types.PatchType,
	data []byte,
	_ metav1.PatchOptions,
	_ ...string,
) (*corev1.Node, error) {
	if patchType != types.ApplyPatchType {
		return nil, fmt.Errorf("unexpected patch type %q", patchType)
	}
	var payload struct {
		Metadata struct {
			Labels      map[string]*string `json:"labels"`
			Annotations map[string]*string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	c.mu.Lock()
	node := c.nodes[name]
	if node == nil {
		c.mu.Unlock()
		return nil, apierrors.NewNotFound(corev1.Resource("nodes"), name)
	}
	updated := node.DeepCopy()
	updated.Labels = patchStringMap(updated.Labels, payload.Metadata.Labels)
	updated.Annotations = patchStringMap(updated.Annotations, payload.Metadata.Annotations)
	c.nextRV++
	updated.ResourceVersion = strconv.FormatInt(c.nextRV, 10)
	c.nodes[name] = updated
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil {
		watcher.Modify(updated.DeepCopy())
	}
	return updated.DeepCopy(), nil
}

func (c *acceptanceNodeClient) create(node *corev1.Node) {
	c.mu.Lock()
	c.nodes[node.Name] = node.DeepCopy()
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil && node.Labels[allocate.EligibleNodeLabel] == "true" {
		watcher.Add(node.DeepCopy())
	}
}

func (c *acceptanceNodeClient) replace(name string, replacement *corev1.Node) {
	c.mu.Lock()
	old := c.nodes[name]
	delete(c.nodes, name)
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil && old != nil && old.Labels[allocate.EligibleNodeLabel] == "true" {
		watcher.Delete(old.DeepCopy())
	}
	c.create(replacement)
}

func (c *acceptanceNodeClient) delete(name string) {
	c.mu.Lock()
	old := c.nodes[name]
	delete(c.nodes, name)
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil && old != nil && old.Labels[allocate.EligibleNodeLabel] == "true" {
		watcher.Delete(old.DeepCopy())
	}
}

func (c *acceptanceNodeClient) snapshot(name string) *corev1.Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nodes[name] == nil {
		return nil
	}
	return c.nodes[name].DeepCopy()
}

func patchStringMap(current map[string]string, patch map[string]*string) map[string]string {
	if current == nil {
		current = make(map[string]string)
	}
	for key, value := range patch {
		if value == nil {
			delete(current, key)
			continue
		}
		current[key] = *value
	}
	return current
}

var _ corev1client.NodeInterface = (*acceptanceNodeClient)(nil)
