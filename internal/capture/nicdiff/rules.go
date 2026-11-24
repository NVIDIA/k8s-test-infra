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

package nicdiff

import (
	"regexp"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicsurfaces"
)

// mode says how an attribute's two values are compared.
type mode int

const (
	// exact: the values must be equal.
	exact mode = iota
	// asSet: comma-separated lists, compared as sets; a difference names the
	// members each side lacks.
	asSet
	// count: comma-separated lists whose members are identifiers; only how many
	// there are is compared.
	count
	// devNumber: "major:minor[ mode]"; the minor follows probe order, so only
	// the major and the file mode are compared.
	devNumber
	// auxNames: comma-separated file names, compared as a set with the probe
	// numbers of auxiliary devices (mlx5_core.rdma.0) dropped.
	auxNames
	// nameKinds: comma-separated device file names; compared as the set of
	// their kinds (uverbs, umad, issm), since the numbers follow probe order.
	nameKinds
	// vuMarker: a VPD VU field; its identifier is compared through relations,
	// its MLNX marker (network or Data Direct function) as a value.
	vuMarker
	// skip: not compared, by design; why says why.
	skip
)

type rule struct {
	mode mode
	why  string // for skip: why the value is not compared
}

const (
	whyIdentifier = "an identifier; compared through relations"
	whyName       = "a name cluster software or probe order chooses"
	whyHost       = "set by the host the node runs on"
)

// rules maps kind.attribute to its rule. An attribute without one is compared
// exactly. Tool records carry a port number in the attribute
// (port.1.link_layer); it reads as port.N here.
var rules = map[string]rule{
	"node.dev_infiniband":              {mode: nameKinds},
	"pci.local_cpulist":                {skip, whyHost},
	"pci.irqs":                         {skip, "follows the host's CPU count"},
	"pci.files":                        {mode: auxNames},
	"pci.aux":                          {mode: asSet},
	"pci.vpd.SN":                       {skip, whyIdentifier},
	"pci.vpd.V3":                       {skip, whyIdentifier},
	"pci.vpd.VU":                       {mode: vuMarker},
	"pci.lspci.capabilities":           {mode: asSet},
	"rdma.name":                        {skip, whyName},
	"rdma.node_guid":                   {skip, whyIdentifier},
	"rdma.sys_image_guid":              {skip, whyIdentifier},
	"rdma.node_desc":                   {skip, "set by rdma-ndd from the host name"},
	"rdma.files":                       {mode: asSet},
	"port.lid":                         {skip, "assigned by the subnet manager; compared through relations"},
	"port.sm_lid":                      {skip, "assigned by the subnet manager"},
	"port.gids":                        {mode: count},
	"port.pkeys":                       {mode: count},
	"port.gid_types":                   {mode: asSet},
	"port.counters":                    {mode: asSet},
	"port.hw_counters":                 {mode: asSet},
	"port.files":                       {mode: asSet},
	"net.name":                         {skip, whyName},
	"net.address":                      {skip, whyIdentifier},
	"net.phys_switch_id":               {skip, whyIdentifier},
	"net.ethtool.bus-info":             {skip, "repeats the PCI address the key holds"},
	"net.ethtool.supported_link_modes": {mode: asSet},
	"net.ecn":                          {mode: asSet},
	"net.qos":                          {mode: asSet},
	"net.files":                        {mode: asSet},
	"uverbs.name":                      {skip, whyName},
	"uverbs.dev":                       {mode: devNumber},
	"uverbs.devnode":                   {mode: devNumber},
	"mad.name":                         {skip, whyName},
	"mad.dev":                          {mode: devNumber},
	"mad.devnode":                      {mode: devNumber},
	"devnode.devnode":                  {mode: devNumber},
	"devlink.health.reporters":         {mode: asSet},
}

var portNumber = regexp.MustCompile(`\bport\.[0-9]+\.`)

// ruleName is kind.attribute with tool port numbers generalised, the name a
// check reports and a causes file matches.
func ruleName(obj, attr string) string {
	return objectRule(obj) + "." + portNumber.ReplaceAllString(attr, "port.N.")
}

// objectRule is the first part of an object's rule names: its kind, or for a
// relation the relation's name (rel/smi_lid/... reads rel.smi_lid).
func objectRule(obj string) string {
	if nicsurfaces.Kind(obj) == "rel" {
		return strings.Join(strings.SplitN(obj, "/", 3)[:2], ".")
	}
	return nicsurfaces.Kind(obj)
}

func ruleFor(name string) rule {
	if r, ok := rules[name]; ok {
		return r
	}
	return rule{mode: exact}
}

// needsDeviceNodes says whether a record reads /dev/infiniband, which a pod
// that cannot see the host's device files does not show.
func needsDeviceNodes(obj, attr string) bool {
	return attr == "devnode" || strings.HasPrefix(obj, "rel/devnode/")
}
