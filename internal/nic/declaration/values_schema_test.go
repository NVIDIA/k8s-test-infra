// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const valuesSchema = "../../../deployments/nvml-mock/helm/nvml-mock/values.schema.json"

// valuesSchemaOf turns a trimmed CRD schema into the JSON Schema the chart
// checks values with: the same types, required fields and list items, and no
// keys the CRD doesn't declare, so a misspelled field fails helm template as
// it fails the node agent.
func valuesSchemaOf(n *schemaNode) map[string]any {
	out := map[string]any{}
	if n.Type != "" {
		out["type"] = n.Type
	}
	if len(n.Properties) > 0 {
		props := map[string]any{}
		for k, p := range n.Properties {
			props[k] = valuesSchemaOf(p)
		}
		out["properties"] = props
		out["additionalProperties"] = false
	}
	if n.AdditionalProperties != nil {
		out["additionalProperties"] = valuesSchemaOf(n.AdditionalProperties)
	}
	if len(n.Required) > 0 {
		out["required"] = n.Required
	}
	if n.Items != nil {
		out["items"] = valuesSchemaOf(n.Items)
	}
	return out
}

// TestValuesSchemaFollowsTheCRDs keeps the chart's schemas for nics.slots,
// nics.rootPorts and a custom profile's spec equal to the CRD schemas the
// node agent checks them against. UPDATE=1 rewrites them.
func TestValuesSchemaFollowsTheCRDs(t *testing.T) {
	t.Parallel()
	want := map[string]any{
		"$comment": "Generated from the NIC CRDs by TestValuesSchemaFollowsTheCRDs in internal/nic/declaration; rerun it with UPDATE=1.",
		"$defs": map[string]any{
			"slot":        valuesSchemaOf(schemas[kindRackProfile].at("spec.node.topology.nics.[]")),
			"rootPort":    valuesSchemaOf(schemas[kindRackProfile].at("spec.node.topology.rootPorts.[]")),
			"profileSpec": valuesSchemaOf(schemas["SNICProfile"].at("spec")),
		},
	}
	data, err := os.ReadFile(valuesSchema)
	require.NoError(t, err)
	const marker = "\n    \"nic\": "
	i := bytes.Index(data, []byte(marker))
	require.Positive(t, i, "values.schema.json has no $defs.nic")

	if os.Getenv("UPDATE") != "" {
		gen, err := json.MarshalIndent(want, "    ", "  ")
		require.NoError(t, err)
		out := append(data[:i:i], []byte(marker)...)
		out = append(out, gen...)
		out = append(out, "\n  }\n}\n"...)
		require.NoError(t, os.WriteFile(valuesSchema, out, 0o600))
		return
	}

	var doc struct {
		Defs       map[string]json.RawMessage `json:"$defs"`
		Properties struct {
			NICs struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"nics"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	var got any
	require.NoError(t, json.Unmarshal(doc.Defs["nic"], &got))
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	var wantAny any
	require.NoError(t, json.Unmarshal(wantJSON, &wantAny))
	require.Equal(t, wantAny, got, "values.schema.json's $defs.nic is stale; rerun with UPDATE=1")

	// The values reference them.
	for key, ref := range map[string]string{"slots": "slot", "rootPorts": "rootPort", "customProfiles": "profileSpec"} {
		require.Contains(t, string(doc.Properties.NICs.Properties[key]), `"$ref": "#/$defs/nic/$defs/`+ref+`"`,
			"nics.%s doesn't use $defs.nic.%s", key, ref)
	}
}
