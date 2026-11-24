// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apixv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// TestEmbeddedSchemasMatchTheCRDs keeps crdschema.json, what decoding checks a
// raw document against, in step with the generated CRDs. UPDATE=1 rewrites it.
func TestEmbeddedSchemasMatchTheCRDs(t *testing.T) {
	t.Parallel()
	out := map[string]*schemaNode{}
	for kind, file := range map[string]string{
		kindNICProfile:  "mokka.nvidia.com_snicprofiles.yaml",
		kindRackProfile: "mokka.nvidia.com_sgpurackprofiles.yaml",
		"SGPUInventory": "mokka.nvidia.com_sgpuinventories.yaml",
	} {
		data, err := os.ReadFile(filepath.Join(crdDir, file))
		require.NoError(t, err)
		var crd apixv1.CustomResourceDefinition
		require.NoError(t, yaml.Unmarshal(data, &crd))
		out[kind] = trim(crd.Spec.Versions[0].Schema.OpenAPIV3Schema)
	}
	want, err := json.MarshalIndent(out, "", " ")
	require.NoError(t, err)
	want = append(want, '\n')
	if os.Getenv("UPDATE") != "" {
		require.NoError(t, os.WriteFile("crdschema.json", want, 0o600))
		return
	}
	require.Equal(t, string(want), string(crdSchemas), "crdschema.json is stale; rerun with UPDATE=1")
}

// trim keeps what checkRaw reads of a CRD schema.
func trim(s *apixv1.JSONSchemaProps) *schemaNode {
	if s == nil {
		return nil
	}
	n := &schemaNode{Type: s.Type, Required: s.Required, ListMapKeys: s.XListMapKeys}
	if s.XListType != nil {
		n.ListType = *s.XListType
	}
	for name, p := range s.Properties {
		if n.Properties == nil {
			n.Properties = map[string]*schemaNode{}
		}
		n.Properties[name] = trim(&p)
	}
	if s.Items != nil {
		n.Items = trim(s.Items.Schema)
	}
	if s.AdditionalProperties != nil {
		n.AdditionalProperties = trim(s.AdditionalProperties.Schema)
	}
	return n
}
