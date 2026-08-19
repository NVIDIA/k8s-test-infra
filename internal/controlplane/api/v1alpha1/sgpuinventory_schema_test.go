// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apixv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

const inventoryCRDFile = "mokka.nvidia.com_sgpuinventories.yaml"

func TestSGPUInventoryCRDRackGroupCountBound(t *testing.T) {
	t.Parallel()

	schema := loadSGPUInventoryCRD(t).Spec.Versions[0].Schema.OpenAPIV3Schema
	spec := schemaProperty(t, schema, "spec")
	rackGroups := schemaProperty(t, spec, "rackGroups")
	require.NotNil(t, rackGroups.Items)
	require.NotNil(t, rackGroups.Items.Schema)
	count := schemaProperty(t, rackGroups.Items.Schema, "count")
	require.NotNil(t, count.Minimum)
	require.NotNil(t, count.Maximum)
	require.InDelta(t, 1, *count.Minimum, 0)
	require.InDelta(t, 100_000, *count.Maximum, 0)
}

func loadSGPUInventoryCRD(t *testing.T) *apixv1.CustomResourceDefinition {
	t.Helper()

	path := filepath.Join("..", "..", "..", "..", "deployments", "mokka-crds", "helm", "mokka-crds", "templates", inventoryCRDFile)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var crd apixv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))
	return &crd
}
