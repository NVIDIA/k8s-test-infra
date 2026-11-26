// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package source

import (
	"errors"
	"fmt"
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
// retained is the NIC state last accepted on this node, or nil; see failOpen.
func compileNetworking(data []byte, cfg *engine.YAMLConfig, state *agent.State, numDevices int, retained *nicstate.NICs) error {
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
func compileNICs(data []byte, cfg *engine.YAMLConfig, node string, state *agent.State, retained *nicstate.NICs) {
	gpus := nicGPUSlots(cfg)
	doc, err := declaration.DecodeDocument(data)
	if err == nil && !doc.Declares() {
		return
	}
	var nics *nicstate.NICs
	if err == nil {
		gpus, err = doc.GPUSlots(gpus)
	}
	if err == nil {
		nics, err = nicstate.Resolve(nicstate.Input{
			Node:           nicstate.FileNode(node),
			Topology:       &v1alpha1.SGPUTopology{RootPorts: doc.RootPorts, GPUSlots: gpus, NICs: doc.Slots},
			Configurations: doc.Configurations,
			RDMA:           doc.RDMA,
			Profiles:       doc.Profiles,
		})
	}
	if err == nil {
		err = checkUniqueAddresses(cfg, nics)
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
	state.NICs, state.NICsKept = failOpen(cfg, gpus, rootPorts, retained)
}

// failOpen decides the NICs of a node whose NIC declaration doesn't resolve.
// The node keeps the NICs it last accepted, if they still form a valid PCI
// tree with its current GPUs and NVSwitches: every address unique, and every
// GPU's parent a root port or switch port of the tree. Otherwise, and on a
// node that never accepted one, it runs without NICs, and a GPU whose parent
// was a NIC switch port is placed directly below its root port. gpus are the
// node's GPU slots, with the parents the document gives when it decodes.
func failOpen(cfg *engine.YAMLConfig, gpus []v1alpha1.GPUSlot, rootPorts []v1alpha1.RootPort,
	retained *nicstate.NICs) (nicstate.NICs, bool) {
	if retained != nil {
		candidate := *retained
		candidate.GPUs = stateGPUs(gpus, retained)
		if checkUniqueAddresses(cfg, &candidate) == nil && parentsInTree(&candidate) {
			return candidate, true
		}
	}
	ports := make([]nicstate.RootPort, 0, len(rootPorts))
	for _, rp := range rootPorts {
		port := nicstate.RootPort{Address: rp.Address, RootComplex: rp.RootComplex}
		if rp.ID != nil {
			port.ID = &nicstate.PCIID{VendorID: rp.ID.VendorID, DeviceID: rp.ID.DeviceID}
		}
		ports = append(ports, port)
	}
	if len(ports) == 0 && retained != nil {
		ports = retained.RootPorts
	}
	out := nicstate.NICs{RootPorts: ports, GPUs: stateGPUs(gpus, retained)}
	for i, g := range out.GPUs {
		out.GPUs[i].Parent = rootPortOf(g, ports)
	}
	return out, false
}

// stateGPUs are GPU slots as the NIC state holds them. A GPU the document
// gave no parent, as when it doesn't decode, keeps the parent the retained
// state gave it at the same address.
func stateGPUs(slots []v1alpha1.GPUSlot, retained *nicstate.NICs) []nicstate.GPU {
	out := make([]nicstate.GPU, 0, len(slots))
	for _, s := range slots {
		g := nicstate.GPU{Index: s.Index, Address: s.PCIAddress, RootComplex: s.RootComplex, NUMANode: s.NumaNode, Parent: s.Parent}
		if g.Parent == "" && retained != nil {
			for _, r := range retained.GPUs {
				if r.Index == g.Index && r.Address == g.Address {
					g.Parent = r.Parent
				}
			}
		}
		out = append(out, g)
	}
	return out
}

// parentsInTree reports whether every GPU's parent is a root port or a switch
// port of the NIC state, in the GPU's root complex.
func parentsInTree(n *nicstate.NICs) bool {
	rootComplex := map[string]string{}
	for _, rp := range n.RootPorts {
		rootComplex[rp.Address] = rp.RootComplex
	}
	for _, b := range n.Bridges {
		rootComplex[b.Address] = b.RootComplex
	}
	for _, g := range n.GPUs {
		if rc, ok := rootComplex[g.Parent]; g.Parent != "" && (!ok || rc != g.RootComplex) {
			return false
		}
	}
	return true
}

// rootPortOf is the parent a GPU keeps without NICs: its own when that is a
// root port, else the root port of its root complex, or none.
func rootPortOf(g nicstate.GPU, ports []nicstate.RootPort) string {
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
