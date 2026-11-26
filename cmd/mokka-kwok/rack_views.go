// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"sync"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

// Each period of ownership has a distinct queue key, even if the same rack
// leaves this shard and returns while its old HTTP request is in flight.
type trackedRack struct {
	ref             state.Object
	wanted, applied int64
	members         map[types.UID]string
	firstRead       chan struct{}
}

type nodeVersion struct{ revision, generation int64 }

func (v nodeVersion) precedes(other nodeVersion) bool {
	return v.revision < other.revision || v.revision == other.revision && v.generation < other.generation
}

type rackViews struct {
	mu     sync.RWMutex
	owned  map[string]*trackedRack
	nodes  map[types.UID]state.NodePayload
	floors map[types.UID]nodeVersion
	pods   *shardPods
	notify func(string)
}

func newRackViews(pods *shardPods, notify func(string)) *rackViews {
	return &rackViews{owned: make(map[string]*trackedRack), nodes: make(map[types.UID]state.NodePayload), floors: make(map[types.UID]nodeVersion), pods: pods, notify: notify}
}

func (f *rackFetcher) observe(obj any) {
	rack := obj.(*api.SGPURack)
	if rack.Spec.NodeType != api.NodeTypeKWOK {
		f.remove(rack)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	item := f.owned[rack.Name]
	if item == nil || item.ref.UID != rack.UID {
		if item != nil {
			f.drop(item)
		}
		item = &trackedRack{ref: state.Object{Name: rack.Name, UID: rack.UID}, firstRead: make(chan struct{})}
		f.owned[rack.Name] = item
	}
	if d := rack.Status.Delivery; d != nil && d.Generation > item.wanted {
		// A pending item already has a queued request, an in-flight request,
		// or a retry timer. Only the transition from current to pending needs Add.
		wasCurrent := item.wanted <= item.applied
		item.wanted = d.Generation
		if wasCurrent && item.wanted > item.applied {
			f.queue.Add(item)
		}
	}
}

func (f *rackFetcher) remove(obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	rack, ok := obj.(*api.SGPURack)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if item := f.owned[rack.Name]; item != nil && item.ref.UID == rack.UID {
		f.drop(item)
	}
}

func (v *rackViews) drop(item *trackedRack) {
	item.finishFirstRead()
	v.clearNodes(item)
	v.pods.setRack(item.ref.Name, nil)
	delete(v.owned, item.ref.Name)
}

func (v *rackViews) clearNodes(item *trackedRack) {
	for uid, name := range item.members {
		if n, ok := v.nodes[uid]; ok && n.Hardware.Rack == item.ref {
			delete(v.nodes, uid)
			v.notify(name)
		}
	}
}

func (v *rackViews) request(item *trackedRack) (state.RackRequest, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return state.RackRequest{Object: item.ref, MinRackDeliveryGeneration: item.wanted}, v.owned[item.ref.Name] == item && item.wanted > item.applied
}

// waitInitialReads takes a finite snapshot after the rack handler has synced.
// Later arrivals use the normal per-Node backfill. An explicit unavailable
// result defers that rack without blocking Pod tracking for healthy racks.
func (v *rackViews) waitInitialReads(ctx context.Context) bool {
	v.mu.RLock()
	var pending []<-chan struct{}
	for _, item := range v.owned {
		if item.wanted > 0 && item.firstRead != nil {
			pending = append(pending, item.firstRead)
		}
	}
	v.mu.RUnlock()
	for _, ch := range pending {
		select {
		case <-ch:
		case <-ctx.Done():
			return false
		}
	}
	return ctx.Err() == nil
}

// Called under the view lock on the first usable state, an explicit per-rack
// unavailable result, or the end of this ownership period.
func (item *trackedRack) finishFirstRead() {
	if item.firstRead != nil {
		close(item.firstRead)
		item.firstRead = nil
	}
}

// accept holds the ownership check and publication under the same lock. A
// failed read keeps the last usable state until the watch removes ownership.
func (v *rackViews) accept(item *trackedRack, requested int64, result state.RackResult) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.owned[item.ref.Name] != item {
		return true
	}
	if result.Status == state.NotReady || result.Status == state.NotFound {
		item.finishFirstRead()
		return false
	}
	r := result.Rack
	if r == nil || r.Hardware.Rack != item.ref || r.RackDeliveryGeneration < requested {
		return false
	}
	if v.regresses(r) {
		return false
	}
	v.clearNodes(item)
	members := make(map[types.UID]string, len(r.Nodes))
	names := sets.New[string]()
	for _, slot := range r.Nodes {
		ver := nodeVersion{slot.AssignmentRevision, r.RackDeliveryGeneration}
		if ver.precedes(v.floors[slot.Node.UID]) {
			continue
		}
		v.nodes[slot.Node.UID] = state.NodePayload{APIVersion: state.Version, Hardware: r.Hardware, Slot: slot}
		v.floors[slot.Node.UID] = ver
		members[slot.Node.UID] = slot.Node.Name
		names.Insert(slot.Node.Name)
		v.notify(slot.Node.Name)
	}
	item.members = members
	item.applied = r.RackDeliveryGeneration
	v.pods.setRack(item.ref.Name, names)
	item.finishFirstRead()
	zap.L().Debug("Applied KWOK rack state", zap.String("rack", item.ref.Name), zap.Int64("generation", item.applied))
	return true
}

// Preserve a newer accepted slot if the server offers an older revision for this rack.
func (v *rackViews) regresses(r *state.Rack) bool {
	for _, slot := range r.Nodes {
		old, ok := v.nodes[slot.Node.UID]
		version := nodeVersion{slot.AssignmentRevision, r.RackDeliveryGeneration}
		if ok && old.Hardware.Rack == r.Hardware.Rack && version.precedes(v.floors[slot.Node.UID]) {
			return true
		}
	}
	return false
}

func (v *rackViews) node(uid types.UID) (state.NodePayload, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	n, ok := v.nodes[uid]
	return n, ok
}

func (v *rackViews) forgetNode(uid types.UID) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.floors, uid)
	delete(v.nodes, uid)
}
