// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"maps"
	"sync"

	"k8s.io/apimachinery/pkg/types"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/rackstate"
)

// All racks of one inventory share the same immutable policy evaluation and
// profile observations. Only the elected leader uses these to publish status.
type deliveryInputs struct {
	*policy.Evaluation
	admitted bool
	profiles map[string]*api.SGPURackProfile
	versions map[types.UID]int64
}

func newDeliveryInputs(e *policy.Evaluation, profiles map[string]*api.SGPURackProfile) *deliveryInputs {
	versions := map[types.UID]int64{e.InventoryUID: e.InventoryGeneration}
	for _, p := range profiles {
		versions[p.UID] = p.Generation
	}
	for _, decision := range e.Decisions {
		if decision.Outcome.Accepted() {
			versions[decision.Policy.UID] = decision.Policy.Generation
		}
	}
	return &deliveryInputs{Evaluation: e, profiles: profiles, versions: versions}
}

func (d *deliveryInputs) manifest(rack *api.SGPURack) (*api.RackDelivery, error) {
	if d == nil || !d.admitted || d.InventoryUID != rack.Spec.InventoryRef.UID || !rack.OwnerMatchesInventoryRef() {
		return nil, nil
	}
	ref := rack.Spec.ProfileRef
	p := d.profiles[ref.Name]
	if p == nil || p.UID != ref.UID || p.Generation != ref.Generation {
		return nil, nil
	}
	next, _, err := rackstate.DependencyManifest(rack, p, d.Evaluation)
	if err != nil {
		return nil, err
	}
	if next.ProfileRef.Revision != ref.Revision {
		return nil, nil
	}
	return &next, nil
}

type deliveryCatalog struct {
	mu          sync.RWMutex
	inventories map[string]*deliveryInputs
}

func (c *deliveryCatalog) get(name string) *deliveryInputs {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inventories[name]
}

// Serialize the rack index read with publication of the new inputs. A worker
// for a concurrently added rack either appears in the list or reads the new inputs.
func (c *deliveryCatalog) update(d *deliveryInputs, snapshot *informerCache) ([]*api.SGPURack, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.inventories[d.InventoryName]
	if old != nil && old.admitted == d.admitted && maps.Equal(old.versions, d.versions) {
		return nil, nil
	}
	if d.InventoryUID == "" {
		delete(c.inventories, d.InventoryName)
		return nil, nil
	}
	racks, err := snapshot.RacksByInventoryUID(d.InventoryUID)
	if err != nil {
		return nil, err
	}
	c.inventories[d.InventoryName] = d
	return racks, nil
}
