// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package state resolves a Node's NIC declaration (MEP-0016) into the values
// its NIC surfaces show: which PCI functions, bridges, RDMA devices and
// netdevs exist, with every identifier, name and attribute decided. File mode
// and the control plane resolve with this package, so the same declaration
// and Node coordinates give the same state in both.
//
// Resolving reads only declared values. It branches on nothing but the
// declared port technology, and never on a vendor or model. Formatting the
// values into files is the simulators' job.
package state

import "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"

// NICs is a Node's resolved NIC state. Every list is in a fixed order, so two
// states of the same declaration are equal.
type NICs struct {
	// RDMA is the Node image's RDMA stack.
	RDMA RDMA
	// RootPorts are the Node's PCIe root ports, by address.
	RootPorts []RootPort
	// GPUs are where the GPUs sit in the PCIe tree they share with the NICs,
	// by index.
	GPUs []GPU
	// Instances are the NIC slots, in declaration order.
	Instances []Instance
	// Bridges are the NICs' PCIe switch ports, by address.
	Bridges []Bridge
	// Functions are the NICs' PCI functions, by address.
	Functions []Function
	// Devices are the RDMA devices, by the address of their function.
	Devices []RDMADevice
	// Netdevs are the Ethernet netdevs the driver creates, by function
	// address and port. IPoIB netdevs follow from RDMA.IPoIB and the
	// InfiniBand ports.
	Netdevs []Netdev
}

// Empty reports whether the Node has no NICs.
func (n *NICs) Empty() bool { return len(n.Instances) == 0 }

// RDMA is what the Node image decides about RDMA.
type RDMA struct {
	// UMAD and IPoIB report whether ib_umad and ib_ipoib load.
	UMAD, IPoIB bool
	// UMADMode is the file mode of umad and issm files, such as "600".
	UMADMode string
	// DriverVersion is the version the NIC driver module reports; empty when
	// it reports none.
	DriverVersion string
}

// Instance is one NIC slot as the Node runs it.
type Instance struct {
	Slot          string
	Profile       string
	Configuration string
	Model         string
	// FirmwareVersion is the slot's firmware version, or the profile's.
	FirmwareVersion string
	BoardID         string
	// Board is the board the NIC's ASIC is on; nil when the slot names none.
	Board *int32
	// GPUs are the indexes of the GPUs the NIC serves.
	GPUs []int32
	// SwitchID is the switch ID the NIC's Ethernet netdevs share.
	SwitchID string
}

// PCIID is a PCI vendor and device ID, four lowercase hex digits each.
type PCIID = v1alpha1.PCIID

// RootPort is one PCIe root port of the Node.
type RootPort struct {
	Address     string
	RootComplex string
	// ID is the root port's PCI IDs; nil when not declared.
	ID *PCIID
}

// GPU is where one GPU sits in the Node's PCIe tree.
type GPU struct {
	Index       int32
	Address     string
	RootComplex string
	NUMANode    int32
	// Parent is the root port or NIC switch port above the GPU; empty when
	// unknown.
	Parent string
}

// Bridge is one exposed port of a NIC's PCIe switch.
type Bridge struct {
	Address     string
	Parent      string
	RootComplex string
	ID          PCIID
	Slot        string
}

// Function is one PCI function of a NIC.
type Function struct {
	// Address is the function's DDDD:BB:DD.F address.
	Address string
	// Slot, ID and Type are the NIC slot, the configuration's function ID
	// and the function type it comes from.
	Slot, ID, Type string

	// PCI and Subsystem are the function's PCI IDs; an undeclared
	// subsystem is 0000:0000.
	PCI, Subsystem PCIID
	// Class is the PCI class code, such as "0x020700".
	Class string
	// Revision is the PCI revision ID; empty when the profile declares none.
	Revision string
	Driver   string

	RootComplex string
	// RootPort is the root port above the function; empty on the root bus.
	RootPort string
	// RootPortID is the root port's PCI IDs; nil when not declared.
	RootPortID *PCIID
	// Upstream are the IDs of the switch ports between the root port and the
	// function, top first.
	Upstream []PCIID
	// Parent is the root port or bridge the function's device sits under;
	// empty on the root bus.
	Parent string
	// NUMANode is -1 when unknown.
	NUMANode int32

	// TotalVFs is the SR-IOV capability; 0 for none.
	TotalVFs int32
	// VF is the identity of the VFs the function enables; nil when the
	// profile doesn't declare one.
	VF *VFType
	// Link is the PCIe link; nil when the profile declares none.
	Link *v1alpha1.NICPCIeLink
	// VPD holds the VPD keywords and their values; nil without VPD.
	VPD map[string]string
}

// VFType is the PCI identity and driver of a function's VFs.
type VFType struct {
	PCI, Subsystem PCIID
	Revision       string
	Driver         string
}

// RDMADevice is one RDMA device of a NIC.
type RDMADevice struct {
	// Name is the kernel's name for the device, such as mlx5_0.
	Name string
	// Function is the address of the function the device is on.
	Function string
	// Slot and ID are the NIC slot and the configuration's RDMA device ID.
	Slot, ID string

	// NodeType is the node_type the driver reports, such as "1: CA".
	NodeType        string
	NodeGUID        string
	SystemImageGUID string
	// Attributes are the optional device attributes the driver creates:
	// fw_ver, hca_type, board_id and hw_rev, as sysfs shows them. An
	// attribute the driver doesn't create is absent; one it creates empty is
	// "".
	Attributes map[string]string

	// FirmwareVersion, HWRev and BoardID are what verbs report, whatever
	// sysfs shows.
	FirmwareVersion string
	HWRev           string
	BoardID         string
	// VendorOUI is the vendor ID verbs report, such as "0x02c9".
	VendorOUI string
	// PartID is the function's PCI device ID, which verbs report as the
	// vendor part ID.
	PartID string

	Ports []Port
}

// Port is one port of an RDMA device.
type Port struct {
	// Number is the port number, from 1.
	Number int
	// ID is the configuration's port ID.
	ID         string
	Technology v1alpha1.NICTechnology
	Width      int32
	LaneSpeed  v1alpha1.LaneSpeed
	Planes     int32
	CapMask    string
	LMC        int32
	// GID is GID 0.
	GID string
	// Netdev is the name of the port's Ethernet netdev; empty for a port
	// without one.
	Netdev string
}

// Netdev is one Ethernet netdev of a NIC function.
type Netdev struct {
	Name string
	// Function is the address of the function the netdev is on.
	Function string
	Slot     string
	// Port is the configuration's port ID, and PhysPortName the name the
	// driver gives it, such as p0.
	Port         string
	PhysPortName string
	MAC          string
	SwitchID     string
	Width        int32
	LaneSpeed    v1alpha1.LaneSpeed
}
