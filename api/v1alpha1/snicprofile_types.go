// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SNICProfile is one NIC model as packaged on a card, and every firmware
// configuration it can run, each declared as its result. It holds model data
// only: no addresses, identifiers or counts of a particular node. NIC slots in
// SGPURackProfile place instances of it on a node.
//
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:metadata:annotations=helm.sh/resource-policy=keep
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=mokka,shortName=snicprof
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.identity.model`
// +kubebuilder:printcolumn:name="Default",type=string,JSONPath=`.spec.defaultConfiguration`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SNICProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec describes the NIC model and its configurations.
	Spec SNICProfileSpec `json:"spec"`
}

// SNICProfileList is the list wrapper for SNICProfile.
// +kubebuilder:object:root=true
type SNICProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the profiles in this list.
	Items []SNICProfile `json:"items"`
}

// SNICProfileSpec is the model data of an SNICProfile.
type SNICProfileSpec struct {
	// Identity is what the NIC reports about itself, the same in every
	// configuration.
	Identity NICIdentity `json:"identity"`

	// +optional
	// +listType=atomic
	// Sources cites the captures or documents the values come from.
	Sources []string `json:"sources,omitempty"`

	// +optional
	// SwitchPort identifies the ports of the NIC's own PCIe switch, which slots
	// place as bridges.
	SwitchPort *PCIID `json:"switchPort,omitempty"`

	// +optional
	// VPD is the vital product data of the functions whose type has vpd set.
	VPD *NICVPD `json:"vpd,omitempty"`

	// +kubebuilder:validation:MinProperties=1
	// FunctionTypes are the kinds of PCI function the NIC can expose, keyed by
	// the name configurations use.
	FunctionTypes map[string]NICFunctionType `json:"functionTypes"`

	// +kubebuilder:validation:MinLength=1
	// DefaultConfiguration names the configuration used when neither the
	// cluster nor the slot selects one.
	DefaultConfiguration string `json:"defaultConfiguration"`

	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	// Configurations are the firmware configurations the card can run, each
	// declared as its result.
	Configurations []NICConfiguration `json:"configurations"`
}

// Configuration returns the named configuration, or the default one when name
// is empty.
func (s *SNICProfileSpec) Configuration(name string) (*NICConfiguration, bool) {
	if name == "" {
		name = s.DefaultConfiguration
	}
	for i := range s.Configurations {
		if s.Configurations[i].Name == name {
			return &s.Configurations[i], true
		}
	}
	return nil, false
}

// NICIdentity is the model-level identity a NIC reports.
type NICIdentity struct {
	// +kubebuilder:validation:MinLength=1
	// Model is the model name, such as ConnectX-8.
	Model string `json:"model"`

	// +optional
	// HCAType is the RDMA hca_type, such as MT4131.
	HCAType string `json:"hcaType,omitempty"`

	// +optional
	// BoardID is the RDMA board_id, such as MT_0000001513.
	BoardID string `json:"boardID,omitempty"`

	// +optional
	// FirmwareVersion is the RDMA fw_ver; a slot may override it.
	FirmwareVersion string `json:"firmwareVersion,omitempty"`

	// +optional
	// PartNumber is the board part number and the default for VPD PN.
	PartNumber string `json:"partNumber,omitempty"`

	// +optional
	// +kubebuilder:validation:Pattern=`^0x[0-9a-fA-F]+$`
	// HWRev is the RDMA hw_rev and the ibv_devinfo hw_ver; empty means 0x0.
	HWRev string `json:"hwRev,omitempty"`

	// +optional
	// +kubebuilder:validation:Pattern=`^0x[0-9a-f]+$`
	// VendorOUI is the IEEE OUI verbs report as vendor_id, such as "0x02c9".
	VendorOUI string `json:"vendorOUI,omitempty"`
}

// VPDScope names which NIC functions share one generated VPD value: board,
// asic or boardPosition, or another identity keyword whose generated value
// this one reuses before its function suffix, as a BlueField's VU starts with
// its SN.
// +kubebuilder:validation:Pattern=`^(board|asic|boardPosition|[A-Z0-9]{2})$`
type VPDScope string

const (
	// VPDScopeBoard shares one value across every slot on the same board.
	VPDScopeBoard VPDScope = "board"
	// VPDScopeASIC shares one value across the functions of one slot.
	VPDScopeASIC VPDScope = "asic"
	// VPDScopeBoardPosition shares one value across the devices on one board
	// whose placement gives the same VPD position.
	VPDScopeBoardPosition VPDScope = "boardPosition"
)

// NICVPD is the vital product data of a NIC model.
type NICVPD struct {
	// +optional
	// Keywords are model values by VPD keyword, such as PN, EC or V2. "name"
	// is the product name, to which a device's vpdPosition is appended. PN
	// defaults to identity.partNumber.
	Keywords map[string]string `json:"keywords,omitempty"`

	// +optional
	// Identities are the keywords whose value is a generated identifier,
	// mapped to the scope that shares one value.
	Identities map[string]VPDScope `json:"identities,omitempty"`
}

// PCIID is a PCI vendor and device ID pair.
type PCIID struct {
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	// VendorID is four lowercase hex digits, without 0x.
	VendorID string `json:"vendorID"`

	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	// DeviceID is four lowercase hex digits, without 0x.
	DeviceID string `json:"deviceID"`
}

// RDMAAttribute is an RDMA device attribute a driver may leave out or leave
// empty.
// +kubebuilder:validation:Enum=fw_ver;hca_type;board_id;hw_rev
type RDMAAttribute string

// NICFunctionType is the hardware identity and driver binding of one kind of
// PCI function. The class isn't here: the configuration decides it.
type NICFunctionType struct {
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	// VendorID is four lowercase hex digits, without 0x.
	VendorID string `json:"vendorID"`

	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	// DeviceID is four lowercase hex digits, without 0x.
	DeviceID string `json:"deviceID"`

	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	// SubsystemVendorID is four lowercase hex digits; empty means 0000.
	SubsystemVendorID string `json:"subsystemVendorID,omitempty"`

	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	// SubsystemDeviceID is four lowercase hex digits; empty means 0000.
	SubsystemDeviceID string `json:"subsystemDeviceID,omitempty"`

	// +kubebuilder:validation:MinLength=1
	// Driver is the bound kernel driver, such as mlx5_core.
	Driver string `json:"driver"`

	// +optional
	// RDMANamePrefix is the kernel's RDMA device name prefix, such as mlx5_.
	RDMANamePrefix string `json:"rdmaNamePrefix,omitempty"`

	// +optional
	// RDMANodeType is the node_type the driver reports; empty means "1: CA".
	RDMANodeType string `json:"rdmaNodeType,omitempty"`

	// +optional
	// +listType=set
	// RDMAOmit lists RDMA attributes the driver doesn't create: the file is
	// absent.
	RDMAOmit []RDMAAttribute `json:"rdmaOmit,omitempty"`

	// +optional
	// +listType=set
	// RDMAEmpty lists RDMA attributes the driver creates with an empty value.
	RDMAEmpty []RDMAAttribute `json:"rdmaEmpty,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// TotalVFs is sriov_totalvfs; 0 means no SR-IOV. Enabling VFs is cluster
	// software's job, so the declaration never says how many are enabled.
	TotalVFs int32 `json:"totalVFs,omitempty"`

	// +optional
	// VFType names the function type of the VFs this function enables, a key
	// of functionTypes. It needs totalVFs.
	VFType string `json:"vfType,omitempty"`

	// +optional
	// +kubebuilder:validation:Pattern=`^0x[0-9a-f]{2}$`
	// Revision is the PCI revision ID, such as "0x00".
	Revision string `json:"revision,omitempty"`

	// +optional
	// PCIeLink is the link the function trains to. It is omitted when no
	// capture shows it, and then no link attributes are generated.
	PCIeLink *NICPCIeLink `json:"pcieLink,omitempty"`

	// +optional
	// VPD reports whether the function exposes the profile's VPD.
	VPD bool `json:"vpd,omitempty"`

	// +optional
	// VPDSuffixes are appended to an identity keyword's value on this kind of
	// function. "{function}" stands for the PCI function number.
	VPDSuffixes map[string]string `json:"vpdSuffixes,omitempty"`

	// +optional
	// +listType=set
	// VPDOmit lists the identity keywords this kind of function doesn't
	// report.
	VPDOmit []string `json:"vpdOmit,omitempty"`
}

// NICPCIeLink is a PCIe link as sysfs reports it.
type NICPCIeLink struct {
	// +kubebuilder:validation:MinLength=1
	// Speed is the link speed as sysfs reports it, such as "64.0 GT/s PCIe".
	Speed string `json:"speed"`

	// +kubebuilder:validation:Minimum=0
	// Width is the link width in lanes.
	Width int32 `json:"width"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// MaxWidth is set only when it differs from width, as on a VF reporting 0
	// of 255.
	MaxWidth int32 `json:"maxWidth,omitempty"`
}

// NICConfiguration is one firmware configuration, declared as its result: its
// ports, the PCI functions it produces and the RDMA devices the driver
// creates on them.
type NICConfiguration struct {
	// +kubebuilder:validation:MinLength=1
	// Name is what slots and overrides select.
	Name string `json:"name"`

	// +optional
	// +listType=atomic
	// Ports are the configuration's logical network ports, in physical order:
	// a port's position is its number, p0 first, and its identifiers and
	// names follow from that number.
	Ports []NICPort `json:"ports,omitempty"`

	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=id
	// Functions are the PCI functions the configuration produces.
	Functions []NICFunction `json:"functions"`

	// +optional
	// +listType=map
	// +listMapKey=id
	// RDMADevices are the RDMA devices the driver creates.
	RDMADevices []NICRDMADevice `json:"rdmaDevices,omitempty"`
}

// NICTechnology is the link technology of a port.
// +kubebuilder:validation:Enum=ib;roce;efa
type NICTechnology string

const (
	// NICTechnologyIB is InfiniBand.
	NICTechnologyIB NICTechnology = "ib"
	// NICTechnologyRoCE is RDMA over Converged Ethernet.
	NICTechnologyRoCE NICTechnology = "roce"
	// NICTechnologyEFA is AWS Elastic Fabric Adapter.
	NICTechnologyEFA NICTechnology = "efa"
)

// LaneSpeed is the per-lane signalling generation of a port.
// +kubebuilder:validation:Enum=SDR;DDR;QDR;FDR10;FDR;EDR;HDR;NDR;XDR
type LaneSpeed string

// NICPort is one logical network port: what a fabric attaches to, not a
// physical connector.
type NICPort struct {
	// +kubebuilder:validation:MinLength=1
	// ID names the port within the configuration.
	ID string `json:"id"`

	// Technology is the port's link technology.
	Technology NICTechnology `json:"technology"`

	// +kubebuilder:validation:Minimum=1
	// Width is the lane count in the rate text, such as 4 in
	// "800 Gb/sec (4X XDR)".
	Width int32 `json:"width"`

	// LaneSpeed is the per-lane signalling generation.
	LaneSpeed LaneSpeed `json:"laneSpeed"`

	// +kubebuilder:validation:Minimum=0
	// Planes is the number of fabric planes the port carries; 0 is a single
	// endpoint.
	Planes int32 `json:"planes"`

	// +optional
	// +kubebuilder:validation:Pattern=`^0x[0-9a-f]{8}$`
	// CapMask is the capability mask an ib or efa port reports, which its
	// firmware sets. A roce port follows a rule instead.
	CapMask string `json:"capMask,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	// LMC is the LID mask count; empty means 0.
	LMC int32 `json:"lmc,omitempty"`
}

// NICFunction is one PCI function a configuration produces.
type NICFunction struct {
	// +kubebuilder:validation:MinLength=1
	// ID names the function within the configuration.
	ID string `json:"id"`

	// +kubebuilder:validation:MinLength=1
	// Type is a key of functionTypes.
	Type string `json:"type"`

	// +kubebuilder:validation:MinLength=1
	// PCIDevice is the PCI device the function belongs to, which a slot
	// places, such as network or hostA.
	PCIDevice string `json:"pciDevice"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	// Function is the PCI function number within the device.
	Function int32 `json:"function"`

	// +kubebuilder:validation:Pattern=`^0x[0-9a-f]{6}$`
	// Class is the PCI class code the configuration gives the function, such
	// as "0x020700".
	Class string `json:"class"`

	// +optional
	// +listType=set
	// Ports are the ports the function owns. A port has one owner per PCI
	// device, so a NIC with several host links shows it on one function per
	// link.
	Ports []string `json:"ports,omitempty"`
}

// NICRDMADevice is an RDMA device the driver creates on a function.
type NICRDMADevice struct {
	// +kubebuilder:validation:MinLength=1
	// ID names the RDMA device within the configuration.
	ID string `json:"id"`

	// +kubebuilder:validation:MinLength=1
	// Function is the ID of the function the device is on.
	Function string `json:"function"`

	// +kubebuilder:validation:MinItems=1
	// +listType=atomic
	// Ports are the device's ports in order: port k of the device is the k-th
	// entry.
	Ports []string `json:"ports"`
}
