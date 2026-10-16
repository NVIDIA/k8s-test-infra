// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"bytes"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// passthroughConfig returns the node's profile with every GPU in passthrough
// virtualization mode, which is what NVML reports inside a VFIO guest. The
// guest's overrides.yaml mirrors the host's, so the mode lives in the profile
// instead. The edit is on the YAML tree, so the rest of the profile is kept.
func passthroughConfig(profile []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(profile, &doc); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("profile is not a YAML mapping")
	}

	virtualization, err := mappingAt(doc.Content[0], "device_defaults", "virtualization")
	if err != nil {
		return nil, err
	}
	setScalar(virtualization, "mode", "passthrough")

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode profile: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode profile: %w", err)
	}

	return out.Bytes(), nil
}

// mappingAt walks keys down from m, adding an empty mapping for each key that
// is missing.
func mappingAt(m *yaml.Node, keys ...string) (*yaml.Node, error) {
	for _, key := range keys {
		value := valueOf(m, key)
		if value == nil {
			value = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
		}
		if value.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("profile %s is not a mapping", key)
		}
		m = value
	}

	return m, nil
}

func setScalar(m *yaml.Node, key, value string) {
	if node := valueOf(m, key); node != nil {
		*node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func valueOf(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}

	return nil
}
