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

func TestSGPUInventoryCRDRackGroupProvisioner(t *testing.T) {
	t.Parallel()

	rackGroups := rackGroupsSchema(t)
	group := rackGroups.Items.Schema

	provisioner := schemaProperty(t, group, "provisioner")
	require.NotNil(t, provisioner.Default)
	require.JSONEq(t, `"CPU"`, string(provisioner.Default.Raw))
	require.Equal(t, []string{`"CPU"`, `"KWOK"`}, enumValues(provisioner))

	require.ElementsMatch(t, []string{
		"self.provisioner == oldSelf.provisioner",
		"!has(self.placement) || self.provisioner == 'CPU'",
		"!has(self.kwok) || self.provisioner == 'KWOK'",
	}, validationRules(group))

	// The immutability rule is a transition rule, which Kubernetes allows only
	// where it can correlate old and new items. Demoting rackGroups to a plain
	// list would make the CRD itself invalid.
	require.NotNil(t, rackGroups.XListType)
	require.Equal(t, "map", *rackGroups.XListType)
	require.Equal(t, []string{"id"}, rackGroups.XListMapKeys)
}

func TestSGPUInventoryCRDKWOKSettings(t *testing.T) {
	t.Parallel()

	kwok := schemaProperty(t, rackGroupsSchema(t).Items.Schema, "kwok")

	maxPods := schemaProperty(t, schemaProperty(t, kwok, "kubelet"), "maxPods")
	require.NotNil(t, maxPods.Default)
	require.JSONEq(t, "110", string(maxPods.Default.Raw))
	require.NotNil(t, maxPods.Minimum)
	require.InDelta(t, 1, *maxPods.Minimum, 0)

	labels := schemaProperty(t, schemaProperty(t, kwok, "nodeTemplate"), "labels")
	require.Equal(t, []string{
		"!('mokka.nvidia.com/sgpu-node' in self) && !('mokka.nvidia.com/provisioner' in self)",
	}, validationRules(labels))
}

func rackGroupsSchema(t *testing.T) *apixv1.JSONSchemaProps {
	t.Helper()

	schema := loadSGPUInventoryCRD(t).Spec.Versions[0].Schema.OpenAPIV3Schema
	rackGroups := schemaProperty(t, schemaProperty(t, schema, "spec"), "rackGroups")
	require.NotNil(t, rackGroups.Items)
	require.NotNil(t, rackGroups.Items.Schema)

	return rackGroups
}

func loadSGPUInventoryCRD(t *testing.T) *apixv1.CustomResourceDefinition {
	t.Helper()

	path := filepath.Join("..", "..", "deployments", "mokka-crds", "helm", "mokka-crds", "templates", inventoryCRDFile)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var crd apixv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))
	return &crd
}
