// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const chartSchemaPath = "../../deployments/nvml-mock/helm/nvml-mock/values.schema.json"

// The chart schema lists every gate the services accept, so Helm rejects a
// stale gate or a locked override before any pod rolls. This keeps that list
// in step with the gates registered here.
func TestChartSchemaListsRegisteredGates(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(chartSchemaPath)
	require.NoError(t, err)

	var schema struct {
		Properties struct {
			FeatureGates struct {
				AdditionalProperties *bool                     `json:"additionalProperties"`
				Properties           map[string]map[string]any `json:"properties"`
			} `json:"featureGates"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	featureGates := schema.Properties.FeatureGates

	require.NotNil(t, featureGates.AdditionalProperties, "featureGates must set additionalProperties")
	require.False(t, *featureGates.AdditionalProperties, "featureGates must reject unregistered gates")

	// component-base registers AllAlpha and AllBeta in every gate.
	want := map[string]map[string]any{
		"AllAlpha": {"type": "boolean"},
		"AllBeta":  {"type": "boolean"},
	}
	for f, specs := range versionedSpecs {
		latest := specs[len(specs)-1]
		if latest.LockToDefault {
			want[string(f)] = map[string]any{"const": latest.Default}
			continue
		}
		want[string(f)] = map[string]any{"type": "boolean"}
	}
	require.Equal(t, want, featureGates.Properties)
}
