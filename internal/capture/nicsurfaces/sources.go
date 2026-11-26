// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nicsurfaces

import "strings"

// SourcesOf are the capture files a record of obj and attr comes from on a
// captured node, for a listing built from elsewhere, such as a declared
// node's state, so diff nic can still tell a surface one side didn't capture
// from a difference: the file it is read from, then any it is also derived
// from. It is empty for a record no capture file holds.
func SourcesOf(obj, attr string) []string {
	switch {
	case Kind(obj) == "port" && attr == "link_local_gid_from_mac":
		return []string{rdmaSysfs, netSysfs}
	case Kind(obj) == "net" && attr == "address_has_node_guid":
		return []string{netSysfs, rdmaSysfs}
	}
	if src := SourceOf(obj, attr); src != "" {
		return []string{src}
	}
	return nil
}

// SourceOf is the capture file a record of obj and attr is read from on a
// captured node; see SourcesOf.
func SourceOf(obj, attr string) string {
	switch kind := Kind(obj); kind {
	case "pci", "bridge":
		return pciSource(attr)
	case "net":
		return netSource(attr)
	case "devlink":
		return devlinkSource(attr)
	case "node":
		if attr == "rdma_system" {
			return rdmaSystem
		}
		return rdmaChar
	case "fw":
		if strings.HasPrefix(attr, "flint.") {
			return fwFlint
		}
		return fwMLXConfig
	default:
		return kindSources[kind]
	}
}

// kindSources are the kinds of object whose records all come from one file.
var kindSources = map[string]string{
	"gpu": pciAll, "rdma": rdmaSysfs, "port": rdmaSysfs, "uverbs": rdmaChar, "mad": rdmaChar, "devnode": rdmaChar,
	"module": sysModules, "rdmalink": rdmaLink, "ibv": ibvDevinfo, "ibstat": ibstat, "topo": smiTopo,
	"cable": cableMLXLink,
}

func pciSource(attr string) string {
	switch {
	case strings.HasPrefix(attr, "vpd."):
		return pciVPD
	case strings.HasPrefix(attr, "lspci."):
		return lspciNIC
	}
	return pciNICSysfs
}

func netSource(attr string) string {
	switch {
	case attr == "ecn", attr == "qos", strings.HasPrefix(attr, "ecn."), strings.HasPrefix(attr, "qos."):
		return netECNQoS
	case strings.HasPrefix(attr, "ethtool.module."):
		return netEthtoolM
	case attr == "ethtool.driver", attr == "ethtool.firmware-version", attr == "ethtool.bus-info":
		return netEthtoolI
	case strings.HasPrefix(attr, "ethtool."):
		return netEthtool
	}
	return netSysfs
}

func devlinkSource(attr string) string {
	switch {
	case strings.HasPrefix(attr, "ports."):
		return devlinkPort
	case strings.HasPrefix(attr, "eswitch."):
		return devlinkESW
	case strings.HasPrefix(attr, "health."):
		return devlinkHealth
	}
	return devlinkInfo
}
