// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	yaml3 "go.yaml.in/yaml/v3"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// Document is the NIC part of a file-mode Node's config document: the GPU
// profile's software.rdma and nics block, and the nic_profiles and
// nic_configurations the chart appends. The rest of the document is the NVML
// engine's, including where the GPUs sit.
type Document struct {
	RDMA *v1alpha1.RDMASoftware
	// RootPorts are the Node's PCIe root ports.
	RootPorts []v1alpha1.RootPort
	// GPUParents are the parents of the GPUs in the PCIe tree, by GPU index.
	GPUParents map[int32]string
	Slots      []v1alpha1.NICSlot
	// Configurations select the configuration of slots, as an SGPUInventory
	// rack group's nicConfigurations do.
	Configurations []v1alpha1.NICConfigurationChoice
	Profiles       Profiles
}

// GPUSlots sets the parent of each GPU slot from nics.gpuParents. A parent for
// a GPU the profile doesn't declare is an error.
func (d *Document) GPUSlots(slots []v1alpha1.GPUSlot) ([]v1alpha1.GPUSlot, error) {
	out := slices.Clone(slots)
	var errs []error
	for _, index := range slices.Sorted(maps.Keys(d.GPUParents)) {
		i := slices.IndexFunc(out, func(g v1alpha1.GPUSlot) bool { return g.Index == index })
		if i < 0 {
			errs = append(errs, fmt.Errorf("nics.gpuParents names GPU %d, which the profile doesn't declare", index))
			continue
		}
		out[i].Parent = d.GPUParents[index]
	}
	return out, errors.Join(errs...)
}

// Declares reports whether the document declares any NICs.
func (d *Document) Declares() bool {
	return len(d.Slots) > 0 || len(d.Configurations) > 0 || len(d.Profiles) > 0
}

// DecodeDocument reads the NIC part of a config document. It decodes that
// part strictly, so a misspelled field is an error rather than a value
// silently dropped, and validates each NIC profile. The slots need the
// Node's GPU slots to validate, which ValidateNode does.
func DecodeDocument(data []byte) (*Document, error) {
	if err := repeatedNICKeys(data); err != nil {
		return nil, err
	}
	j, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("parse config document: %w", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(j, &top); err != nil {
		return nil, fmt.Errorf("parse config document: %w", err)
	}
	if err := checkRawDocument(j); err != nil {
		return nil, err
	}

	out := &Document{Profiles: Profiles{}}
	var software map[string]json.RawMessage
	var nics struct {
		RootPorts  []v1alpha1.RootPort `json:"rootPorts"`
		GPUParents map[int32]string    `json:"gpuParents"`
		Slots      []v1alpha1.NICSlot  `json:"slots"`
	}
	var profiles []json.RawMessage
	errs := []error{
		decodeKey(top, "software", "software", &software),
		decodeKey(top, "nics", "nics", &nics),
		decodeKey(top, "nic_configurations", "nic_configurations", &out.Configurations),
		decodeKey(top, "nic_profiles", "nic_profiles", &profiles),
	}
	// software may come to describe more of the image than RDMA, so only its
	// rdma key is held to the NIC schema.
	errs = append(errs, decodeKey(software, "rdma", "software.rdma", &out.RDMA))
	out.RootPorts, out.GPUParents, out.Slots = nics.RootPorts, nics.GPUParents, nics.Slots
	for _, raw := range profiles {
		p, err := DecodeProfile(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("nic_profiles: %w", err))
			continue
		}
		if _, dup := out.Profiles[p.Name]; dup {
			errs = append(errs, fmt.Errorf("nic_profiles: %s is listed twice", p.Name))
		}
		out.Profiles[p.Name] = p
	}
	return out, errors.Join(errs...)
}

// decodeKey strictly decodes key of top, if present, naming it field in an
// error.
func decodeKey(top map[string]json.RawMessage, key, field string, into any) error {
	raw, ok := top[key]
	if !ok {
		return nil
	}
	if err := yaml.UnmarshalStrict(raw, into); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

// nicKeys are the config document's top-level keys that hold NICs; the rest
// is the NVML engine's, which reads it its own way.
var nicKeys = []string{"nics", "software", "nic_configurations", "nic_profiles"}

// repeatedNICKeys rejects a key that appears twice in one mapping of the NIC
// part of a config document, or a NIC key that appears twice at the top.
// Converting YAML to JSON keeps the last silently, so a pasted slot field or
// a second nics block would quietly replace another.
func repeatedNICKeys(data []byte) error {
	var root yaml3.Node
	if err := yaml3.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse config document: %w", err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml3.MappingNode {
		return nil
	}
	top := root.Content[0]
	seen := map[string]bool{}
	var errs []error
	for i := 0; i+1 < len(top.Content); i += 2 {
		key := top.Content[i].Value
		if !slices.Contains(nicKeys, key) {
			continue
		}
		if seen[key] {
			errs = append(errs, fmt.Errorf("line %d: key %q is repeated", top.Content[i].Line, key))
		}
		seen[key] = true
		errs = append(errs, repeatedKeys(top.Content[i+1], key)...)
	}
	return errors.Join(errs...)
}

// repeatedKeys finds the keys repeated within any mapping below n.
func repeatedKeys(n *yaml3.Node, path string) []error {
	var errs []error
	switch n.Kind {
	case yaml3.MappingNode:
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if seen[key] {
				errs = append(errs, fmt.Errorf("%s: line %d: key %q is repeated", path, n.Content[i].Line, key))
			}
			seen[key] = true
			errs = append(errs, repeatedKeys(n.Content[i+1], path+"."+key)...)
		}
	case yaml3.SequenceNode:
		for i, item := range n.Content {
			errs = append(errs, repeatedKeys(item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	case yaml3.DocumentNode, yaml3.ScalarNode, yaml3.AliasNode:
		// A scalar holds no keys, and an alias repeats a node checked where
		// its anchor is.
	}
	return errs
}

// checkRawDocument checks the NIC parts of a config document, as JSON, against
// the CRD schemas they share: slots and root ports with a rack profile's
// topology, the RDMA software with its software, and the configuration
// choices with an inventory's nicConfigurations. NIC profiles are checked as they are decoded.
func checkRawDocument(j []byte) error {
	var raw any
	if err := json.Unmarshal(j, &raw); err != nil {
		return fmt.Errorf("parse config document: %w", err)
	}
	var pr problems
	for _, part := range []struct{ path, kind, schemaPath string }{
		{"nics.slots", kindRackProfile, "spec.node.topology.nics"},
		{"nics.rootPorts", kindRackProfile, "spec.node.topology.rootPorts"},
		{"software.rdma", kindRackProfile, "spec.software.rdma"},
		{"nic_configurations", "SGPUInventory", "spec.rackGroups.[].nicConfigurations"},
	} {
		checkRaw(&pr, lookup(raw, part.path), schemas[part.kind].at(part.schemaPath), part.path)
	}
	return pr.err()
}
