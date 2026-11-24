// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package state

import (
	"cmp"
	"slices"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/declaration"
)

// tree is the Node's one PCIe tree: its root ports and the switch ports every
// NIC slot declares, by address.
type tree struct {
	rootPorts   map[string]v1alpha1.RootPort
	switchPorts map[string]treeSwitchPort
}

type treeSwitchPort struct {
	parent string
	id     PCIID
}

func newTree(topology *v1alpha1.SGPUTopology, slots []v1alpha1.NICSlot, profiles declaration.Profiles) tree {
	t := tree{rootPorts: map[string]v1alpha1.RootPort{}, switchPorts: map[string]treeSwitchPort{}}
	for _, rp := range topology.RootPorts {
		t.rootPorts[rp.Address] = rp
	}
	for _, s := range slots {
		sw := profiles[s.Profile].Spec.SwitchPort
		for _, d := range s.Devices {
			for _, b := range d.Bridges {
				t.switchPorts[b.Address] = treeSwitchPort{parent: b.Parent, id: *sw}
			}
		}
	}
	return t
}

// above walks up from a parent to its root port: the root port, its IDs and
// the IDs of the switch ports on the way, top first. A device on the root bus
// has no parent and nothing above it.
func (t tree) above(parent string) (string, *PCIID, []PCIID) {
	var ids []PCIID
	a := parent
	for {
		sp, ok := t.switchPorts[a]
		if !ok {
			break
		}
		ids = append(ids, sp.id)
		a = sp.parent
	}
	slices.Reverse(ids)
	rp, ok := t.rootPorts[a]
	if !ok {
		return "", nil, ids
	}
	return rp.Address, rp.ID.DeepCopy(), ids
}

// addTree carries the root ports and the GPUs' places, which pcibus renders
// in the tree the NICs share.
func (r *resolver) addTree(topology *v1alpha1.SGPUTopology) {
	for _, rp := range topology.RootPorts {
		r.out.RootPorts = append(r.out.RootPorts, RootPort{Address: rp.Address, RootComplex: rp.RootComplex, ID: rp.ID.DeepCopy()})
	}
	slices.SortFunc(r.out.RootPorts, func(a, b RootPort) int { return strings.Compare(a.Address, b.Address) })
	for _, g := range topology.GPUSlots {
		r.out.GPUs = append(r.out.GPUs, GPU{
			Index: g.Index, Address: g.PCIAddress, RootComplex: g.RootComplex, NUMANode: g.NumaNode, Parent: g.Parent,
		})
	}
	slices.SortFunc(r.out.GPUs, func(a, b GPU) int { return cmp.Compare(a.Index, b.Index) })
}
