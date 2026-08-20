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

const profileCRDFile = "mokka.nvidia.com_sgpurackprofiles.yaml"

func TestSGPURackProfileCRDDimensionBoundsMatchRenderedRack(t *testing.T) {
	t.Parallel()

	schema := loadSGPURackProfileCRD(t).Spec.Versions[0].Schema.OpenAPIV3Schema
	spec := schemaProperty(t, schema, "spec")
	nodesPerRack := schemaProperty(t, schemaProperty(t, spec, "rack"), "nodesPerRack")
	require.NotNil(t, nodesPerRack.Minimum)
	require.NotNil(t, nodesPerRack.Maximum)
	require.InDelta(t, 1, *nodesPerRack.Minimum, 0)
	require.InDelta(t, 1024, *nodesPerRack.Maximum, 0)

	node := schemaProperty(t, spec, "node")
	count := schemaProperty(t, schemaProperty(t, node, "gpus"), "count")
	require.NotNil(t, count.Minimum)
	require.NotNil(t, count.Maximum)
	require.InDelta(t, 1, *count.Minimum, 0)
	require.InDelta(t, 64, *count.Maximum, 0)

	gpuSlots := schemaProperty(t, schemaProperty(t, node, "topology"), "gpuSlots")
	require.NotNil(t, gpuSlots.MinItems)
	require.NotNil(t, gpuSlots.MaxItems)
	require.Equal(t, int64(1), *gpuSlots.MinItems)
	require.Equal(t, int64(64), *gpuSlots.MaxItems)
}

func loadSGPURackProfileCRD(t *testing.T) *apixv1.CustomResourceDefinition {
	t.Helper()

	path := filepath.Join("..", "..", "..", "..", "deployments", "mokka-crds", "helm", "mokka-crds", "templates", profileCRDFile)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var crd apixv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))
	return &crd
}
