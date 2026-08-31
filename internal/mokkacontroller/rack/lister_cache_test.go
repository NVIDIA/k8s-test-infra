// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/listers"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
)

type listerCache struct {
	inventories mokkalisters.SGPUInventoryLister
	profiles    mokkalisters.SGPURackProfileLister
	racks       cache.Indexer
	nodes       nodeLister
}

func (c *listerCache) AllocationNodeGeneration() uint64 { return 0 }

type nodeLister interface {
	List(selector labels.Selector) ([]*corev1.Node, error)
}

func newListerCache(
	inventories mokkalisters.SGPUInventoryLister,
	profiles mokkalisters.SGPURackProfileLister,
	racks cache.Indexer,
	nodes nodeLister,
) *listerCache {
	return &listerCache{inventories: inventories, profiles: profiles, racks: racks, nodes: nodes}
}

func (c *listerCache) Inventory(name string) (*mokkav1alpha1.SGPUInventory, error) {
	return c.inventories.Get(name)
}

func (c *listerCache) Inventories() ([]*mokkav1alpha1.SGPUInventory, error) {
	return c.inventories.List(labels.Everything())
}

func (c *listerCache) Profile(name string) (*mokkav1alpha1.SGPURackProfile, error) {
	return c.profiles.Get(name)
}

func (c *listerCache) Rack(name string) (*mokkav1alpha1.SGPURack, error) {
	obj, exists, err := c.racks.GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuracks"), name)
	}
	rack, ok := obj.(*mokkav1alpha1.SGPURack)
	if !ok {
		return nil, fmt.Errorf("rack cache contained %T", obj)
	}
	return rack, nil
}

func (c *listerCache) Racks() ([]*mokkav1alpha1.SGPURack, error) {
	objects := c.racks.List()
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(objects))
	for _, obj := range objects {
		rack, ok := obj.(*mokkav1alpha1.SGPURack)
		if !ok {
			return nil, fmt.Errorf("rack cache contained %T", obj)
		}
		racks = append(racks, rack)
	}
	return racks, nil
}

func (c *listerCache) RacksByInventoryUID(uid types.UID) ([]*mokkav1alpha1.SGPURack, error) {
	objects, err := c.racks.ByIndex(RackByInventoryUIDIndex, string(uid))
	if err != nil {
		return nil, err
	}
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(objects))
	for _, obj := range objects {
		rack, ok := obj.(*mokkav1alpha1.SGPURack)
		if !ok {
			return nil, fmt.Errorf("rack cache contained %T", obj)
		}
		racks = append(racks, rack)
	}
	return racks, nil
}

func (c *listerCache) RacksByInventoryGroup(uid types.UID, group string) ([]*mokkav1alpha1.SGPURack, error) {
	objects, err := c.racks.ByIndex(RackByInventoryGroupIndex, InventoryGroupIndexKey(uid, group))
	if err != nil {
		return nil, err
	}
	racks := make([]*mokkav1alpha1.SGPURack, 0, len(objects))
	for _, obj := range objects {
		rack, ok := obj.(*mokkav1alpha1.SGPURack)
		if !ok {
			return nil, fmt.Errorf("rack cache contained %T", obj)
		}
		racks = append(racks, rack)
	}
	return racks, nil
}

func (c *listerCache) AllocationNodes() ([]allocate.Node, error) {
	nodes, err := c.nodes.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	allocationNodes := make([]allocate.Node, 0, len(nodes))
	for _, node := range nodes {
		allocationNodes = append(allocationNodes, allocate.Node{
			Name: node.Name, UID: node.UID,
			CreationTimestamp: node.CreationTimestamp.Time,
			Terminating:       node.DeletionTimestamp != nil,
			Labels:            node.Labels,
		})
	}
	return allocationNodes, nil
}

func newNodeLister(indexer cache.Indexer) nodeLister {
	return listers.New[*corev1.Node](indexer, corev1.Resource("nodes"))
}
