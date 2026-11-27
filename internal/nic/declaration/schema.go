// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// crdSchemas holds the parts of the generated CRD schemas that decoding
// needs and Go's types can't express: which keys are required and which
// lists are sets or maps. TestEmbeddedSchemasMatchTheCRDs keeps it in step
// with the CRDs.
//
//go:embed crdschema.json
var crdSchemas []byte

// schemaNode is a trimmed OpenAPI schema.
type schemaNode struct {
	Type                 string                 `json:"type,omitempty"`
	Properties           map[string]*schemaNode `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	Items                *schemaNode            `json:"items,omitempty"`
	AdditionalProperties *schemaNode            `json:"additionalProperties,omitempty"`
	ListType             string                 `json:"x-kubernetes-list-type,omitempty"`
	ListMapKeys          []string               `json:"x-kubernetes-list-map-keys,omitempty"`
}

// schemas are the trimmed schemas by kind.
var schemas = func() map[string]*schemaNode {
	var out map[string]*schemaNode
	if err := json.Unmarshal(crdSchemas, &out); err != nil {
		panic(fmt.Sprintf("crdschema.json: %v", err))
	}
	return out
}()

// at is the schema of the field at a dotted path below n, or nil.
func (n *schemaNode) at(path string) *schemaNode {
	for _, f := range strings.Split(path, ".") {
		if n == nil {
			return nil
		}
		if f == "[]" {
			n = n.Items
			continue
		}
		n = n.Properties[f]
	}
	return n
}

// checkRaw checks a raw document, as JSON decodes it, the way the API server
// does before Go sees it: a required key must be present, even with a zero
// value, and a set or map list holds no item twice. Go fills an omitted key
// with its zero value, so without this file mode would accept what the API
// server rejects.
func checkRaw(pr *problems, raw any, s *schemaNode, path string) {
	if s == nil {
		return
	}
	switch v := raw.(type) {
	case map[string]any:
		for _, key := range s.Required {
			_, ok := v[key]
			pr.check(ok, "%s: missing required field %q", where(path), key)
		}
		for _, key := range slices.Sorted(maps.Keys(v)) {
			value := v[key]
			child := s.Properties[key]
			if child == nil {
				child = s.AdditionalProperties
			}
			checkRaw(pr, value, child, join(path, key))
		}
	case []any:
		seen := map[string]int{}
		for i, item := range v {
			checkRaw(pr, item, s.Items, fmt.Sprintf("%s[%d]", path, i))
			key, ok := listKey(s, item)
			if !ok {
				continue
			}
			first, dup := seen[key]
			pr.check(!dup, "%s[%d]: repeats %s[%d]", path, i, path, first)
			if !dup {
				seen[key] = i
			}
		}
	}
}

// listKey is what makes a list item unique: the whole item in a set, its
// key fields in a map list. ok is false for a list whose items may repeat.
func listKey(s *schemaNode, item any) (string, bool) {
	switch s.ListType {
	case "set":
		b, _ := json.Marshal(item)
		return string(b), true
	case "map":
		m, _ := item.(map[string]any)
		key := make([]any, 0, len(s.ListMapKeys))
		for _, k := range slices.Sorted(slices.Values(s.ListMapKeys)) {
			key = append(key, m[k])
		}
		b, _ := json.Marshal(key)
		return string(b), true
	}
	return "", false
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func where(path string) string {
	if path == "" {
		return "the document"
	}
	return path
}

// checkRawAt checks the parts of a raw document at paths, each against the
// schema of kind at the same path.
func checkRawAt(pr *problems, raw any, kind string, paths ...string) {
	for _, path := range paths {
		checkRaw(pr, lookup(raw, path), schemas[kind].at(path), path)
	}
}

// lookup is the value at a dotted path of map keys, or nil.
func lookup(raw any, path string) any {
	for _, f := range strings.Split(path, ".") {
		m, _ := raw.(map[string]any)
		raw = m[f]
	}
	return raw
}
