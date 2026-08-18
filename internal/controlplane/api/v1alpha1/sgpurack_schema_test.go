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

const (
	rackProfileCRDFile = "mokka.nvidia.com_sgpurackprofiles.yaml"
	rackCRDFile        = "mokka.nvidia.com_sgpuracks.yaml"
)

func TestSGPURackProfileCRDIdentityAndRackDescription(t *testing.T) {
	t.Parallel()

	crd := loadCRD(t, rackProfileCRDFile)
	require.Equal(t, "sgpurackprofiles.mokka.nvidia.com", crd.Name)
	require.Equal(t, "SGPURackProfile", crd.Spec.Names.Kind)
	require.Equal(t, "SGPURackProfileList", crd.Spec.Names.ListKind)
	require.Equal(t, "sgpurackprofile", crd.Spec.Names.Singular)
	require.Equal(t, "sgpurackprofiles", crd.Spec.Names.Plural)
	require.Equal(t, []string{"srprof"}, crd.Spec.Names.ShortNames)

	schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	rack := schemaProperty(t, schemaProperty(t, schema, "spec"), "rack")
	require.Equal(t, "Rack is the logical rack shape described by this profile.", rack.Description)
}

func TestSGPURackCRDIdentityAndPresentation(t *testing.T) {
	t.Parallel()

	crd := loadSGPURackCRD(t)
	require.Equal(t, "sgpuracks.mokka.nvidia.com", crd.Name)
	require.Equal(t, GroupName, crd.Spec.Group)
	require.Equal(t, apixv1.ClusterScoped, crd.Spec.Scope)
	require.Equal(t, "SGPURack", crd.Spec.Names.Kind)
	require.Equal(t, "SGPURackList", crd.Spec.Names.ListKind)
	require.Equal(t, "sgpurack", crd.Spec.Names.Singular)
	require.Equal(t, "sgpuracks", crd.Spec.Names.Plural)
	require.Equal(t, []string{"sgpur"}, crd.Spec.Names.ShortNames)
	require.Contains(t, crd.Spec.Names.Categories, "mokka")

	require.Len(t, crd.Spec.Versions, 1)
	version := crd.Spec.Versions[0]
	require.Equal(t, GroupVersion.Version, version.Name)
	require.True(t, version.Served)
	require.True(t, version.Storage)
	require.NotNil(t, version.Subresources)
	require.NotNil(t, version.Subresources.Status)

	type column struct {
		name     string
		kind     string
		jsonPath string
	}
	wantColumns := []column{
		{name: "Inventory", kind: "string", jsonPath: ".spec.inventoryRef.name"},
		{name: "Rack Group", kind: "string", jsonPath: ".spec.identity.rackGroup"},
		{name: "Rack", kind: "integer", jsonPath: ".spec.identity.rackIndex"},
		{name: "Profile", kind: "string", jsonPath: ".spec.profileRef.name"},
		{name: "Assigned", kind: "integer", jsonPath: ".status.assignedNodes"},
		{name: "Age", kind: "date", jsonPath: ".metadata.creationTimestamp"},
	}
	require.Len(t, version.AdditionalPrinterColumns, len(wantColumns))
	for i, want := range wantColumns {
		got := version.AdditionalPrinterColumns[i]
		require.Equal(t, want.name, got.Name)
		require.Equal(t, want.kind, got.Type)
		require.Equal(t, want.jsonPath, got.JSONPath)
	}
}

func TestSGPURackCRDDurableNodes(t *testing.T) {
	t.Parallel()

	schema := loadSGPURackCRD(t).Spec.Versions[0].Schema.OpenAPIV3Schema
	require.ElementsMatch(t, []string{"spec"}, schema.Required)

	spec := schemaProperty(t, schema, "spec")
	require.ElementsMatch(t, []string{"identity", "inventoryRef", "nodes", "profileRef"}, spec.Required)
	require.NotContains(t, spec.Properties, "gpuFabric")
	require.NotContains(t, spec.Properties, "network")

	inventoryRef := schemaProperty(t, spec, "inventoryRef")
	require.ElementsMatch(t, []string{"name", "uid"}, inventoryRef.Required)
	require.NotEmpty(t, inventoryRef.XValidations)
	require.Equal(t, "size(self.uid) > 0", inventoryRef.XValidations[0].Rule)

	profileRef := schemaProperty(t, spec, "profileRef")
	require.ElementsMatch(t, []string{"generation", "name", "revision", "uid"}, profileRef.Required)
	require.Equal(t, "^[0-9a-f]{64}$", schemaProperty(t, profileRef, "revision").Pattern)
	require.NotEmpty(t, profileRef.XValidations)
	require.Equal(t, "size(self.uid) > 0", profileRef.XValidations[0].Rule)

	identity := schemaProperty(t, spec, "identity")
	require.ElementsMatch(t, []string{"cliqueID", "fabricUUID", "rackGroup", "rackIndex"}, identity.Required)
	require.Equal(t, "dns1123Label", schemaProperty(t, identity, "rackGroup").Format)
	require.Equal(t, "uuid", schemaProperty(t, identity, "fabricUUID").Format)

	nodes := schemaProperty(t, spec, "nodes")
	require.Equal(t, int64(1), *nodes.MinItems)
	require.Equal(t, int64(1024), *nodes.MaxItems)
	require.Equal(t, "map", *nodes.XListType)
	require.Equal(t, []string{"index"}, nodes.XListMapKeys)
	require.NotNil(t, nodes.Items)
	require.NotNil(t, nodes.Items.Schema)

	node := nodes.Items.Schema
	require.ElementsMatch(t, []string{"gpus", "index"}, node.Required)
	nodeRef := schemaProperty(t, node, "nodeRef")
	require.ElementsMatch(t, []string{"name", "uid"}, nodeRef.Required)
	require.NotEmpty(t, nodeRef.XValidations)
	require.Equal(t, "size(self.uid) > 0", nodeRef.XValidations[0].Rule)

	gpus := schemaProperty(t, node, "gpus")
	require.Equal(t, int64(1), *gpus.MinItems)
	require.Equal(t, int64(64), *gpus.MaxItems)
	require.Equal(t, "map", *gpus.XListType)
	require.Equal(t, []string{"index"}, gpus.XListMapKeys)
}

func TestSGPURackCRDObservedStatus(t *testing.T) {
	t.Parallel()

	schema := loadSGPURackCRD(t).Spec.Versions[0].Schema.OpenAPIV3Schema
	status := schemaProperty(t, schema, "status")
	require.Len(t, status.Properties, 3)
	require.Contains(t, status.Properties, "observedGeneration")
	require.Contains(t, status.Properties, "assignedNodes")

	conditions := schemaProperty(t, status, "conditions")
	require.Equal(t, "map", *conditions.XListType)
	require.Equal(t, []string{"type"}, conditions.XListMapKeys)
}

func loadSGPURackCRD(t *testing.T) *apixv1.CustomResourceDefinition {
	t.Helper()
	return loadCRD(t, rackCRDFile)
}

func loadCRD(t *testing.T, filename string) *apixv1.CustomResourceDefinition {
	t.Helper()

	path := filepath.Join("..", "..", "..", "..", "deployments", "mokka-crds", "helm", "mokka-crds", "templates", filename)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var crd apixv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))
	return &crd
}

func schemaProperty(t *testing.T, schema *apixv1.JSONSchemaProps, name string) *apixv1.JSONSchemaProps {
	t.Helper()

	property, ok := schema.Properties[name]
	require.Truef(t, ok, "schema property %q is missing", name)
	return &property
}
