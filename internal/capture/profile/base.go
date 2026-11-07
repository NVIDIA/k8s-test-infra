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

package profile

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// baseProfile is an existing profile that supplies what a capture cannot:
// load readings, real-looking identifiers, and blocks that describe the mock
// rather than the board. Every lookup on a nil baseProfile finds nothing.
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
