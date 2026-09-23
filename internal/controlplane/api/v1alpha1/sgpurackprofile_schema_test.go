// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

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

func TestSGPURackProfileCRDRequiresDeterministicGPUTopology(t *testing.T) {
	t.Parallel()

	schema := loadSGPURackProfileCRD(t).Spec.Versions[0].Schema.OpenAPIV3Schema
	spec := schemaProperty(t, schema, "spec")
	node := schemaProperty(t, spec, "node")
	require.Contains(t, node.Required, "topology")
	require.Len(t, node.XValidations, 1)
	require.Equal(
		t,
		"size(self.topology.gpuSlots) == self.gpus.count && self.topology.gpuSlots.all(slot, slot.index < self.gpus.count)",
		node.XValidations[0].Rule,
	)

	topology := schemaProperty(t, node, "topology")
	require.Contains(t, topology.Required, "gpuSlots")
	gpuSlots := schemaProperty(t, topology, "gpuSlots")
	require.NotNil(t, gpuSlots.Items)
	require.NotNil(t, gpuSlots.Items.Schema)
	slot := gpuSlots.Items.Schema
	require.ElementsMatch(t, []string{"index", "pciAddress", "rootComplex"}, slot.Required)

	index := schemaProperty(t, slot, "index")
	require.NotNil(t, index.Minimum)
	require.NotNil(t, index.Maximum)
	require.InDelta(t, 0, *index.Minimum, 0)
	require.InDelta(t, 63, *index.Maximum, 0)
	require.Equal(t, `^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`, schemaProperty(t, slot, "pciAddress").Pattern)
	require.Equal(t, `^pci[0-9a-f]{4}:[0-9a-f]{2}$`, schemaProperty(t, slot, "rootComplex").Pattern)
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
