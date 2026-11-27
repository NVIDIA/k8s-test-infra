// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package profile

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// baseProfile is an existing profile that supplies what a capture cannot:
// real-looking identifiers, and blocks that describe the mock rather than the
// board. Every lookup on a nil baseProfile finds nothing.
type baseProfile struct {
	name string
	root *yaml.Node
}

func loadBase(path string) (*baseProfile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s is not a profile", path)
	}
	return &baseProfile{name: path, root: doc.Content[0]}, nil
}

// pciDeviceID is the GPU's PCI device ID, which says what GPU a profile is of.
var pciDeviceID = getter("GetPciInfo", "PciDeviceId").as(hex32)

// checkBase refuses a -base written for another GPU: the blocks it supplies,
// such as features and NVSwitches, would describe that GPU. The PCI device
// ID tells; a base without one cannot be checked, so it is refused too.
func (g *generator) checkBase() error {
	if g.base == nil {
		return nil
	}
	const key = "device_defaults.pci.device_id"
	n := g.base.find(key)
	if n == nil || n.Kind != yaml.ScalarNode {
		return fmt.Errorf("-base %s has no %s to show it is a profile of the captured GPU", g.base.name, key)
	}
	want, err := strconv.ParseUint(n.Value, 0, 32)
	if err != nil {
		return fmt.Errorf("-base %s: %s = %q is not a PCI device ID", g.base.name, key, n.Value)
	}
	for i, a := range g.answers(pciDeviceID) {
		if a.missing != "" {
			return fmt.Errorf("GPU %d: %s = %s, so -base cannot be checked against it", i, pciDeviceID.from, a.missing)
		}
		if got, err := strconv.ParseUint(a.yaml, 0, 32); err != nil || got != want {
			return fmt.Errorf("-base %s is a profile of PCI device %s, but GPU %d of the capture is %s: pass a profile of the same GPU, or no -base",
				g.base.name, n.Value, i, a.yaml)
		}
	}
	return nil
}

func lookup(n *yaml.Node, path []string) *yaml.Node {
	for _, key := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	return n
}

func (b *baseProfile) find(dotted string) *yaml.Node {
	if b == nil {
		return nil
	}
	return lookup(b.root, strings.Split(dotted, "."))
}

func (b *baseProfile) scalar(dotted string) (scalar, bool) {
	n := b.find(dotted)
	if n == nil || n.Kind != yaml.ScalarNode {
		return scalar{}, false
	}
	return toScalar(n), true
}

func (b *baseProfile) node(dotted string) (node, bool) {
	n := b.find(dotted)
	if n == nil {
		return nil, false
	}
	return convert(n), true
}

// deviceScalar finds a key of the base's devices[] entry with the given index.
func (b *baseProfile) deviceScalar(index int, dotted string) (scalar, bool) {
	devices := b.find("devices")
	if devices == nil || devices.Kind != yaml.SequenceNode {
		return scalar{}, false
	}
	for _, d := range devices.Content {
		if idx := lookup(d, []string{"index"}); idx == nil || idx.Value != strconv.Itoa(index) {
			continue
		}
		n := lookup(d, strings.Split(dotted, "."))
		if n == nil || n.Kind != yaml.ScalarNode {
			return scalar{}, false
		}
		return toScalar(n), true
	}
	return scalar{}, false
}

func toScalar(n *yaml.Node) scalar {
	if n.Tag == "!!str" {
		return quote(n.Value)
	}
	return scalar{n.Value}
}

func convert(n *yaml.Node) node {
	switch n.Kind {
	case yaml.MappingNode:
		m := &mapping{flow: n.Style&yaml.FlowStyle != 0}
		for i := 0; i+1 < len(n.Content); i += 2 {
			m.set(n.Content[i].Value, convert(n.Content[i+1]), "")
		}
		return m
	case yaml.SequenceNode:
		s := &sequence{}
		for _, item := range n.Content {
			s.items = append(s.items, convert(item))
		}
		return s
	case yaml.AliasNode:
		return convert(n.Alias)
	default:
		return toScalar(n)
	}
}
