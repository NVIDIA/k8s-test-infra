// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"maps"
	"slices"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// nodePlacement checks where things sit on a Node: that no two claim one PCI
// address, and that GPUs, NIC devices and NIC switch ports form one PCIe tree
// below the Node's root ports.
type nodePlacement struct {
	pr          *problems
	owners      map[string]string // PCI address -> what claims it
	rootPorts   map[string]string // address -> root complex
	switchPorts map[string]switchPort
	parents     []parentRef
}

type switchPort struct{ rootComplex, parent string }

// parentRef is a parent something names, checked once the whole Node is
// known: a GPU may sit below a switch port of any slot.
type parentRef struct{ who, parent, rootComplex string }

func newNodePlacement(pr *problems) *nodePlacement {
	return &nodePlacement{pr: pr, owners: map[string]string{}, rootPorts: map[string]string{}, switchPorts: map[string]switchPort{}}
}

func (n *nodePlacement) claim(addr, owner string) {
	prev, taken := n.owners[addr]
	n.pr.check(!taken, "PCI address %s is used by %s and %s", addr, prev, owner)
	n.owners[addr] = owner
}

// addRootPorts checks the Node's root ports, each declared once.
func (n *nodePlacement) addRootPorts(ports []v1alpha1.RootPort) {
	for _, rp := range ports {
		where := "root port " + rp.Address
		n.pr.check(rootComplex.MatchString(rp.RootComplex), "%s: rootComplex %q is not pciDDDD:BB", where, rp.RootComplex)
		n.pr.check(inDomain(bdf, rp.Address, domainOf(rp.RootComplex)), "%s: not a PCI address in %s", where, rp.RootComplex)
		if rp.ID != nil {
			checkPCIID(n.pr, where+": id", *rp.ID)
		}
		n.claim(rp.Address, where)
		n.rootPorts[rp.Address] = rp.RootComplex
	}
}

func (n *nodePlacement) addSwitchPort(b v1alpha1.PCIBridge, rootComplex, who string) {
	n.switchPorts[b.Address] = switchPort{rootComplex: rootComplex, parent: b.Parent}
	n.addParent(who+": bridge "+b.Address, b.Parent, rootComplex)
}

func (n *nodePlacement) addParent(who, parent, rootComplex string) {
	n.parents = append(n.parents, parentRef{who: who, parent: parent, rootComplex: rootComplex})
}

// checkTree checks every parent named on the Node: each is a root port or a
// switch port in the same root complex, and switch ports form no cycle.
func (n *nodePlacement) checkTree() {
	for _, r := range n.parents {
		rc, ok := n.rootComplexOf(r.parent)
		n.pr.check(ok, "%s: parent %q is not a root port or a switch port of the node", r.who, r.parent)
		n.pr.check(!ok || rc == r.rootComplex, "%s: parent %s is in %s, not %s", r.who, r.parent, rc, r.rootComplex)
	}
	for _, addr := range slices.Sorted(maps.Keys(n.switchPorts)) {
		n.pr.check(!n.inCycle(addr), "switch port %s: its parents form a cycle", addr)
	}
}

func (n *nodePlacement) rootComplexOf(addr string) (string, bool) {
	if rc, ok := n.rootPorts[addr]; ok {
		return rc, true
	}
	sp, ok := n.switchPorts[addr]
	return sp.rootComplex, ok
}

// inCycle reports whether walking up from a switch port comes back to a port
// it already passed instead of reaching a root port. A parent that is missing
// ends the walk; checkTree reports it on its own.
func (n *nodePlacement) inCycle(addr string) bool {
	seen := map[string]bool{}
	for a := addr; ; {
		sp, ok := n.switchPorts[a]
		if !ok {
			return false
		}
		if seen[a] {
			return true
		}
		seen[a] = true
		a = sp.parent
	}
}
