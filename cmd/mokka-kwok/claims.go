// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	resourceclient "k8s.io/client-go/kubernetes/typed/resource/v1"
	"k8s.io/client-go/tools/cache"
)

// ResourceClaims have no field selector for the allocated Node. Use the same
// lock as rack membership so a released Node drops its claims immediately.
type shardClaims struct {
	pods      *shardPods
	client    resourceclient.ResourceV1Interface
	driver    string
	locations map[cache.ObjectName]sets.Set[string]
	synced    bool
}

func (c *shardClaims) hasSynced() bool {
	c.pods.mu.Lock()
	defer c.pods.mu.Unlock()
	return c.synced
}

func (c *shardClaims) Add(obj any) error { return c.Update(obj) }
func (c *shardClaims) Update(obj any) error {
	c.pods.mu.Lock()
	defer c.pods.mu.Unlock()
	c.observe(obj.(*resourcev1.ResourceClaim))
	return nil
}

func (c *shardClaims) Delete(obj any) error {
	c.pods.mu.Lock()
	defer c.pods.mu.Unlock()
	c.forget(cache.MetaObjectToName(obj.(*resourcev1.ResourceClaim)))
	return nil
}

func (c *shardClaims) Resync() error { return nil }
func (c *shardClaims) Replace(objects []any, _ string) error {
	c.pods.mu.Lock()
	defer c.pods.mu.Unlock()
	for key := range c.locations {
		c.forget(key)
	}
	for _, obj := range objects {
		c.observe(obj.(*resourcev1.ResourceClaim))
	}
	c.synced = true
	return nil
}
func (c *shardClaims) forget(key cache.ObjectName) {
	for name := range c.locations[key] {
		if n := c.pods.nodes[name]; n != nil {
			delete(n.claims, key)
			c.pods.enqueueOwners(n)
		}
	}
	delete(c.locations, key)
}

func (c *shardClaims) observe(claim *resourcev1.ResourceClaim) {
	key := cache.MetaObjectToName(claim)
	c.forget(key)
	allocation := claim.Status.Allocation
	if allocation == nil {
		return
	}
	for _, r := range allocation.Devices.Results {
		switch r.Driver {
		case c.driver, domainDriver:
			c.keep(key, r.Pool, claim)
		}
	}
	// Also retain allocations for other drivers that the scheduler restricts
	// to a node in this shard. Their pools need not use the node name.
	if allocation.NodeSelector != nil {
		for _, term := range allocation.NodeSelector.NodeSelectorTerms {
			for _, field := range term.MatchFields {
				if field.Key == "metadata.name" && field.Operator == corev1.NodeSelectorOpIn {
					for _, name := range field.Values {
						c.keep(key, name, claim)
					}
				}
			}
		}
	}
}

func (c *shardClaims) keep(key cache.ObjectName, name string, claim *resourcev1.ResourceClaim) {
	n := c.pods.nodes[name]
	if n == nil {
		return
	}
	c.remember(key, name, claim, n)
	c.pods.enqueueOwners(n)
}

func (c *shardClaims) remember(key cache.ObjectName, name string, claim *resourcev1.ResourceClaim, n *nodePods) {
	if n.claims == nil {
		n.claims = make(map[cache.ObjectName]*resourcev1.ResourceClaim)
	}
	if c.locations[key] == nil {
		c.locations[key] = sets.New[string]()
	}
	c.locations[key].Insert(name)
	n.claims[key] = claim
}

func (c *shardClaims) dropNode(name string, n *nodePods) {
	for key := range n.claims {
		c.locations[key].Delete(name)
		if len(c.locations[key]) == 0 {
			delete(c.locations, key)
		}
	}
}

func (c *shardClaims) get(ctx context.Context, node string, key cache.ObjectName) (*resourcev1.ResourceClaim, error) {
	c.pods.mu.Lock()
	n := c.pods.nodes[node]
	if n == nil {
		c.pods.mu.Unlock()
		return nil, nil
	}
	if claim := n.claims[key]; claim != nil {
		c.pods.mu.Unlock()
		return claim, nil
	}
	// Subscribe before the live GET. A watch update or deletion while the
	// request is in flight takes precedence over the snapshot it returns.
	c.remember(key, node, nil, n)
	c.pods.mu.Unlock()
	claim, err := c.client.ResourceClaims(key.Namespace).Get(ctx, key.Name, metav1.GetOptions{})
	c.pods.mu.Lock()
	defer c.pods.mu.Unlock()
	current, waiting := n.claims[key]
	if n != c.pods.nodes[node] {
		return nil, nil
	}
	if !waiting || current != nil {
		return current, nil
	}
	if err == nil {
		n.claims[key] = claim
		return claim, nil
	}
	delete(n.claims, key)
	c.locations[key].Delete(node)
	if len(c.locations[key]) == 0 {
		delete(c.locations, key)
	}
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return nil, err
}
