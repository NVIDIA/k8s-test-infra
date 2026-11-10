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

package engine

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// FabricClique is one (type, id) clique assignment of nvmlGpuFabricInfo_v4_t.
// Type is an NVML_GPU_FABRIC_CLIQUE_TYPE_* value.
type FabricClique struct {
	Type uint8
	ID   uint32
}

// fabricCliqueTypes maps the names a profile uses onto
// NVML_GPU_FABRIC_CLIQUE_TYPE_*.
var fabricCliqueTypes = map[string]uint8{
	"unicast_pointer":            nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_POINTER,
	"multicast_pointer":          nvml.GPU_FABRIC_CLIQUE_TYPE_MULTICAST_POINTER,
	"unicast_logical_endpoint":   nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_LOGICAL_ENDPOINT,
	"multicast_logical_endpoint": nvml.GPU_FABRIC_CLIQUE_TYPE_MULTICAST_LOGICAL_ENDPOINT,
}

func parseFabricCliqueType(name string) (uint8, bool) {
	t, ok := fabricCliqueTypes[strings.ToLower(strings.TrimSpace(name))]
	return t, ok
}

// fabricCliques builds the v4 clique list, sorted by type and then id as the
// NVML header promises.
//
// The driver reports one clique per type, and more types with each generation:
// unicast pointer always, multicast pointer from Hopper, both logical-endpoint
// types from Blackwell. Each follows clique_id unless the profile lists
// cliques of that type, which then stand in for it. The unicast-pointer clique
// is never taken from the profile's list: it is the clique v1 to v3 report as
// cliqueId, and the versions must agree.
func fabricCliques(cfg *FabricConfig, arch gpuarch.Arch) []FabricClique {
	reported := []uint8{nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_POINTER}
	if arch.AtLeast(gpuarch.Hopper) {
		reported = append(reported, nvml.GPU_FABRIC_CLIQUE_TYPE_MULTICAST_POINTER)
	}
	if arch.AtLeast(gpuarch.Blackwell) {
		reported = append(reported,
			nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_LOGICAL_ENDPOINT,
			nvml.GPU_FABRIC_CLIQUE_TYPE_MULTICAST_LOGICAL_ENDPOINT)
	}

	var cliques []FabricClique
	for _, c := range cfg.Cliques {
		// Validation rejects both cases at load; a runtime override reaches
		// here unvalidated, and dropping the entry keeps the list well-formed.
		t, ok := parseFabricCliqueType(c.Type)
		if !ok || t == nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_POINTER {
			continue
		}
		cliques = append(cliques, FabricClique{Type: t, ID: c.ID})
		reported = slices.DeleteFunc(reported, func(r uint8) bool { return r == t })
	}
	for _, t := range reported {
		cliques = append(cliques, FabricClique{Type: t, ID: cfg.CliqueID})
	}

	slices.SortFunc(cliques, func(a, b FabricClique) int {
		return cmp.Or(cmp.Compare(a.Type, b.Type), cmp.Compare(a.ID, b.ID))
	})
	cliques = slices.Compact(cliques)
	return cliques[:min(len(cliques), nvml.GPU_FABRIC_CLIQUE_MAX)]
}

// validateFabricCliques checks the cliques list of every fabric block.
func validateFabricCliques(config *YAMLConfig) error {
	if err := validateFabricCliqueList(config.DeviceDefaults.Fabric); err != nil {
		return fmt.Errorf("device_defaults.%w", err)
	}
	for i := range config.Devices {
		if err := validateFabricCliqueList(config.Devices[i].Fabric); err != nil {
			return fmt.Errorf("devices[%d].%w", i, err)
		}
	}
	return nil
}

func validateFabricCliqueList(fabric *FabricConfig) error {
	if fabric == nil {
		return nil
	}
	// One slot of the struct always holds the unicast-pointer clique.
	if limit := nvml.GPU_FABRIC_CLIQUE_MAX - 1; len(fabric.Cliques) > limit {
		return fmt.Errorf("fabric.cliques lists %d entries, at most %d fit", len(fabric.Cliques), limit)
	}
	for i, c := range fabric.Cliques {
		t, ok := parseFabricCliqueType(c.Type)
		if !ok {
			return fmt.Errorf("fabric.cliques[%d].type %q is not a clique type", i, c.Type)
		}
		if t == nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_POINTER {
			return fmt.Errorf("fabric.cliques[%d].type %q: set the unicast-pointer clique with fabric.clique_id", i, c.Type)
		}
	}
	return nil
}
