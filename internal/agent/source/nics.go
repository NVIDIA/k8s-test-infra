// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package source

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/declaration"
	nicstate "github.com/NVIDIA/k8s-test-infra/internal/nic/state"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// compileNetworking fills the network shape the ib simulator renders, from
// the profile's infiniband block, and the node's NIC state, from its NIC
// declaration (MEP-0016). The NIC state is reported but not rendered until
// the ib simulator renders it (#1355); until then the infiniband block decides
// what the node shows, and NIC configuration choices change nothing a workload
// sees.
//
// retained is the NIC declaration last accepted on this node, or nil; see
// failOpen.
func compileNetworking(data []byte, cfg *engine.YAMLConfig, state *agent.State, numDevices int, retained *declaration.Document) error {
	network, err := compileNetwork(data, numDevices)
	if err != nil {
		return err
	}
	state.NodeShape.Network = network
	compileNICs(data, cfg, nodeName(state), state, retained)
	return nil
}

// compileNICs resolves the profile document's NIC declaration for this node
// into state.NICs. The node's whole PCI tree is checked together: its GPUs,
// its NVSwitches and its NIC tree. A declaration that doesn't resolve fails
// open: state.NICError says why, and failOpen decides the NICs.
func compileNICs(data []byte, cfg *engine.YAMLConfig, node string, state *agent.State, retained *declaration.Document) {
	gpus := nicGPUSlots(cfg)
	doc, err := declaration.DecodeDocument(data)
	if err == nil && !doc.Declares() {
		return
	}
	parented := false
	var nics *nicstate.NICs
	if err == nil {
		gpus, err = doc.GPUSlots(gpus)
		parented = err == nil
	}
	if err == nil {
		nics, err = resolveDocument(cfg, node, doc, gpus)
	}
	if err == nil {
		state.NICs = *nics
		return
	}
	state.NICError = err.Error()
	var rootPorts []v1alpha1.RootPort
	if doc != nil {
		rootPorts = doc.RootPorts
	}
	state.NICs, state.NICsKept = failOpen(cfg, node, gpus, parented, rootPorts, retained)
}

// resolveDocument resolves a NIC document for the node with its GPU slots
// and checks that no PCI address is claimed twice across GPUs, NVSwitches and
// the NIC tree.
func resolveDocument(cfg *engine.YAMLConfig, node string, doc *declaration.Document, gpus []v1alpha1.GPUSlot) (*nicstate.NICs, error) {
	nics, err := nicstate.Resolve(nicstate.Input{
		Node:           nicstate.FileNode(node),
		Topology:       &v1alpha1.SGPUTopology{RootPorts: doc.RootPorts, GPUSlots: gpus, NICs: doc.Slots},
		Configurations: doc.Configurations,
		RDMA:           doc.RDMA,
		Profiles:       doc.Profiles,
	})
	if err != nil {
		return nil, err
	}
	if err := checkUniqueAddresses(cfg, nics); err != nil {
		return nil, err
	}
	return nics, nil
}

// failOpen decides the NICs of a node whose NIC declaration doesn't resolve.
// The node keeps the NICs it last accepted if that declaration still resolves
// with the node's current GPUs and NVSwitches, so the whole tree is checked
// again: unique addresses, parents, root complexes and buses. Otherwise, and
// on a node that never accepted one, it runs without NICs; see noNICs. gpus
// are the node's GPU slots; when parented, the new document placed them, and
// those parents count, since a GPU's place in the tree is the current state,
// else they keep the ones the accepted declaration gave them.
func failOpen(cfg *engine.YAMLConfig, node string, gpus []v1alpha1.GPUSlot, parented bool,
	rootPorts []v1alpha1.RootPort, retained *declaration.Document) (nicstate.NICs, bool) {
	if retained == nil {
		return noNICs(cfg, node, gpus, rootPorts), false
	}
	gpus = placedGPUs(gpus, parented, retained)
	if nics, err := resolveDocument(cfg, node, retained, gpus); err == nil {
		return *nics, true
	}
	return noNICs(cfg, node, gpus, rootPorts, retained.RootPorts), false
}

// noNICs is the PCI tree of a node without NICs: its root ports and each GPU
// directly below its root port. The root ports come from the first of the
// candidates that holds up as a tree with the GPUs and NVSwitches, the
// rejected document's and then the last accepted ones, so a root port the
// node just rejected never becomes a GPU's parent. When none does, the GPUs
// have no parent.
func noNICs(cfg *engine.YAMLConfig, node string, gpus []v1alpha1.GPUSlot, candidates ...[]v1alpha1.RootPort) nicstate.NICs {
	for _, ports := range candidates {
		if len(ports) == 0 {
			continue
		}
		below := slices.Clone(gpus)
		for i := range below {
			below[i].Parent = rootPortOf(below[i], ports)
		}
		if nics, err := resolveDocument(cfg, node, &declaration.Document{RootPorts: ports}, below); err == nil {
			return nicstate.NICs{RootPorts: nics.RootPorts, GPUs: nics.GPUs}
		}
	}
	out := nicstate.NICs{}
	for _, s := range gpus {
		out.GPUs = append(out.GPUs, nicstate.GPU{Index: s.Index, Address: s.PCIAddress, RootComplex: s.RootComplex, NUMANode: s.NumaNode})
	}
	return out
}

// placedGPUs are the GPU slots with the parents the new document gave them,
// or, when it gave none, the ones the accepted declaration did.
func placedGPUs(gpus []v1alpha1.GPUSlot, parented bool, accepted *declaration.Document) []v1alpha1.GPUSlot {
	if parented {
		return gpus
	}
	placed, err := accepted.GPUSlots(gpus)
	if err != nil {
		return gpus
	}
	return placed
}

// rootPortOf is the parent a GPU keeps without NICs: its own when that is a
// root port, else the root port of its root complex, or none.
func rootPortOf(g v1alpha1.GPUSlot, ports []v1alpha1.RootPort) string {
	for _, rp := range ports {
		if rp.Address == g.Parent {
			return g.Parent
		}
	}
	for _, rp := range ports {
		if rp.RootComplex == g.RootComplex {
			return rp.Address
		}
	}
	return ""
}

// checkUniqueAddresses rejects a PCI address two of the node's functions
// claim, across its GPUs, its NVSwitches and its NIC tree: root ports, NIC
// switch ports and NIC functions. The NVML engine compares GPUs with
// NVSwitches and the NIC rules compare GPUs with the NIC tree; only the node
// agent has all three. Switches count whether or not they are on the PCIe
// bus, as they do in the engine's check.
func checkUniqueAddresses(cfg *engine.YAMLConfig, nics *nicstate.NICs) error {
	owners := map[string]string{}
	var errs []error
	claim := func(addr, owner string) {
		addr = strings.ToLower(strings.TrimSpace(addr))
		if addr == "" {
			return
		}
		if prev, dup := owners[addr]; dup {
			errs = append(errs, fmt.Errorf("PCI address %s is both %s and %s", addr, prev, owner))
			return
		}
		owners[addr] = owner
	}
	for i := range declaredDeviceCount(*cfg) {
		claim(buildDeviceSpec(i, cfg.DeviceDefaults, cfg.Devices).PCIBusID, fmt.Sprintf("GPU %d", i))
	}
	if cfg.NVLink != nil {
		for i, sw := range cfg.NVLink.Switches {
			claim(sw.BDF, fmt.Sprintf("NVSwitch %d", i))
		}
	}
	for _, rp := range nics.RootPorts {
		claim(rp.Address, "a root port")
	}
	for _, b := range nics.Bridges {
		claim(b.Address, "a switch port of NIC slot "+b.Slot)
	}
	for _, f := range nics.Functions {
		claim(f.Address, "a function of NIC slot "+f.Slot)
	}
	return errors.Join(errs...)
}

// nodeName is the coordinate a file-mode node's NIC identifiers come from.
// Every node of a release reads the same document, so without its name every
// node would show the same GUIDs and MACs.
func nodeName(state *agent.State) string {
	if state.Node.NodeName != "" {
		return state.Node.NodeName
	}
	zap.L().Warn("NODE_NAME is unset; NIC identifiers come from the host name, which may repeat across nodes",
		zap.String("hostname", state.Node.Hostname))
	return state.Node.Hostname
}

// nicGPUSlots places the profile's GPUs as NIC slots refer to them: by index,
// at the address and under the root complex the profile declares. Every GPU
// the profile declares counts, so trimming GPUs with GPU_COUNT never makes a
// NIC slot that serves one invalid.
func nicGPUSlots(cfg *engine.YAMLConfig) []v1alpha1.GPUSlot {
	roots := map[string]engine.RootComplexConfig{}
	if cfg.PCIeTopology != nil {
		for _, rc := range cfg.PCIeTopology.RootComplexes {
			for _, bdf := range rc.Devices {
				roots[strings.ToLower(bdf)] = rc
			}
		}
	}
	var slots []v1alpha1.GPUSlot
	for i := range declaredDeviceCount(*cfg) {
		bdf := strings.ToLower(buildDeviceSpec(i, cfg.DeviceDefaults, cfg.Devices).PCIBusID)
		if !agent.ValidBDF(bdf) {
			continue
		}
		slot := v1alpha1.GPUSlot{Index: int32(i), PCIAddress: bdf, RootComplex: "pci" + bdf[:7], NumaNode: -1}
		if rc, ok := roots[bdf]; ok {
			slot.RootComplex, slot.NumaNode = rc.ID, int32(rc.NUMANode)
		}
		slots = append(slots, slot)
	}
	return slots
}
