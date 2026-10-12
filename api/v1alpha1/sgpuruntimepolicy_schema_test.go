// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const runtimePolicyCRDFile = "mokka.nvidia.com_sgpuruntimepolicies.yaml"

func TestSGPURuntimePolicyCRDReportsAcceptance(t *testing.T) {
	t.Parallel()

	version := loadCRD(t, runtimePolicyCRDFile).Spec.Versions[0]
	type column struct {
		name     string
		kind     string
		jsonPath string
	}
	wantColumns := []column{
		{name: "Inventory", kind: "string", jsonPath: ".spec.targetRef.name"},
		{name: "RackGroups", kind: "string", jsonPath: ".status.rackGroupsSummary"},
		{name: "Racks", kind: "string", jsonPath: ".status.rackIndexesSummary"},
		{name: "Nodes", kind: "string", jsonPath: ".status.nodeIndexesSummary"},
		{name: "GPUs", kind: "string", jsonPath: ".status.gpuIndexesSummary"},
		{name: "Accepted", kind: "string", jsonPath: `.status.conditions[?(@.type=="Accepted")].status`},
		{name: "Age", kind: "date", jsonPath: ".metadata.creationTimestamp"},
	}
	require.Len(t, version.AdditionalPrinterColumns, len(wantColumns))
	for i, want := range wantColumns {
		got := version.AdditionalPrinterColumns[i]
		require.Equal(t, want.name, got.Name)
		require.Equal(t, want.kind, got.Type)
		require.Equal(t, want.jsonPath, got.JSONPath)
	}

	conditions := schemaProperty(t, schemaProperty(t, version.Schema.OpenAPIV3Schema, "status"), "conditions")
	require.Equal(t, "map", *conditions.XListType)
	require.Equal(t, []string{"type"}, conditions.XListMapKeys)
}

func TestSGPURuntimePolicyCRDRejectsNegativeIndexes(t *testing.T) {
	t.Parallel()

	schema := loadCRD(t, runtimePolicyCRDFile).Spec.Versions[0].Schema.OpenAPIV3Schema
	targetRef := schemaProperty(t, schemaProperty(t, schema, "spec"), "targetRef")
	for _, axis := range []string{"rackIndexes", "nodeIndexes", "gpuIndexes"} {
		items := schemaProperty(t, targetRef, axis).Items.Schema
		require.NotNil(t, items.Minimum, axis)
		require.InDelta(t, 0, *items.Minimum, 0, axis)
	}
}
