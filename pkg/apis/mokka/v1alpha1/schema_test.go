// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

func TestGeneratedCRDs(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "deployments", "mokka-crds", "helm", "mokka-crds", "crds", "*.yaml"))
	require.NoError(t, err)
	require.Len(t, files, 3)

	tests := []struct {
		file      string
		plural    string
		kind      string
		hasStatus bool
	}{
		{file: "mokka.nvidia.com_sgpuprofiles.yaml", plural: "sgpuprofiles", kind: "SGPUProfile"},
		{file: "mokka.nvidia.com_sgpuinventories.yaml", plural: "sgpuinventories", kind: "SGPUInventory", hasStatus: true},
		{file: "mokka.nvidia.com_sgpuracks.yaml", plural: "sgpuracks", kind: "SGPURack", hasStatus: true},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			crd := loadCRD(t, tt.file)
			require.Equal(t, apiextensionsv1.ClusterScoped, crd.Spec.Scope)
			require.Equal(t, GroupName, crd.Spec.Group)
			require.Equal(t, tt.plural, crd.Spec.Names.Plural)
			require.Equal(t, tt.kind, crd.Spec.Names.Kind)
			require.Len(t, crd.Spec.Versions, 1)

			version := crd.Spec.Versions[0]
			require.Equal(t, SchemeGroupVersion.Version, version.Name)
			require.True(t, version.Served)
			require.True(t, version.Storage)
			if tt.hasStatus {
				require.NotNil(t, version.Subresources)
				require.NotNil(t, version.Subresources.Status)
			} else {
				require.Nil(t, version.Subresources)
			}
			requireStructural(t, version.Schema.OpenAPIV3Schema)
		})
	}
}

func TestProfileSchemaHasSlotMapAndCrossFieldCEL(t *testing.T) {
	t.Parallel()

	schema := versionSchema(loadCRD(t, "mokka.nvidia.com_sgpuprofiles.yaml"))
	spec := property(t, schema, "spec")
	require.NotContains(t, spec.Properties, "defaults")
	node := property(t, spec, "node")
	gpuSlots := property(t, property(t, node, "topology"), "gpuSlots")
	require.Equal(t, "map", *gpuSlots.XListType)
	require.Equal(t, []string{"index"}, gpuSlots.XListMapKeys)
	require.NotNil(t, gpuSlots.MaxItems)
	require.ElementsMatch(t, []string{
		"self.gpus.count == size(self.topology.gpuSlots)",
		"self.topology.gpuSlots.all(slot, slot.index < self.gpus.count)",
		"self.topology.gpuSlots.all(slot, self.topology.gpuSlots.exists_one(other, other.pciAddress == slot.pciAddress))",
	}, validationRules(node))

	address := property(t, gpuSlots.Items.Schema, "pciAddress")
	require.Equal(t, `^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`, address.Pattern)
	require.Equal(t, int64(12), *address.MaxLength)
	capacity := property(t, property(t, property(t, node, "gpus"), "memory"), "capacity")
	require.True(t, capacity.XIntOrString)
	require.NotEmpty(t, capacity.XValidations)

	attributes := property(t, property(t, property(t, node, "gpus"), "capabilities"), "attributes")
	require.NotNil(t, attributes.AdditionalProperties)
	strings := property(t, attributes.AdditionalProperties.Schema, "strings")
	require.Equal(t, "set", *strings.XListType)
}

func TestInventorySchemaHasBoundedRackGroupAndConditionMaps(t *testing.T) {
	t.Parallel()

	schema := versionSchema(loadCRD(t, "mokka.nvidia.com_sgpuinventories.yaml"))
	rackGroups := property(t, property(t, schema, "spec"), "rackGroups")
	require.Equal(t, "map", *rackGroups.XListType)
	require.Equal(t, []string{"id"}, rackGroups.XListMapKeys)
	require.Equal(t, int64(64), *rackGroups.MaxItems)
	require.Equal(t, float64(0), *property(t, rackGroups.Items.Schema, "count").Minimum)

	status := property(t, schema, "status")
	conditions := property(t, status, "conditions")
	require.Equal(t, "map", *conditions.XListType)
	require.Equal(t, []string{"type"}, conditions.XListMapKeys)
	groupStatuses := property(t, status, "rackGroups")
	require.Equal(t, "map", *groupStatuses.XListType)
	require.Equal(t, []string{"id"}, groupStatuses.XListMapKeys)

	usage := property(t, status, "usage")
	for _, name := range []string{"requestedNodes", "allocatedNodes", "availableNodes", "pendingNodes", "conflictingNodes", "projectedNodes"} {
		require.Contains(t, usage.Properties, name)
	}

	profileName := property(t, property(t, rackGroups.Items.Schema, "profileRef"), "name")
	require.JSONEq(t, `""`, string(profileName.Default.Raw))
	require.NotEmpty(t, property(t, rackGroups.Items.Schema, "profileRef").XValidations)
}

func TestRackSchemaHasMapListsAndReferences(t *testing.T) {
	t.Parallel()

	schema := versionSchema(loadCRD(t, "mokka.nvidia.com_sgpuracks.yaml"))
	spec := property(t, schema, "spec")
	for _, name := range []string{"inventoryRef", "profileRef", "identity", "slots"} {
		require.Contains(t, spec.Properties, name)
	}
	slots := property(t, spec, "slots")
	require.Equal(t, "map", *slots.XListType)
	require.Equal(t, []string{"index"}, slots.XListMapKeys)
	gpus := property(t, slots.Items.Schema, "gpus")
	require.Equal(t, "map", *gpus.XListType)
	require.Equal(t, []string{"index"}, gpus.XListMapKeys)

	conditions := property(t, property(t, schema, "status"), "conditions")
	require.Equal(t, "map", *conditions.XListType)
	require.Equal(t, []string{"type"}, conditions.XListMapKeys)
}

func loadCRD(t *testing.T, name string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()

	path := filepath.Join("..", "..", "..", "..", "deployments", "mokka-crds", "helm", "mokka-crds", "crds", name)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	jsonData, err := yaml.YAMLToJSON(data)
	require.NoError(t, err)

	crd := &apiextensionsv1.CustomResourceDefinition{}
	require.NoError(t, json.Unmarshal(jsonData, crd))
	return crd
}

func versionSchema(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.JSONSchemaProps {
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema
}

func property(t *testing.T, schema *apiextensionsv1.JSONSchemaProps, name string) *apiextensionsv1.JSONSchemaProps {
	t.Helper()

	value, found := schema.Properties[name]
	require.Truef(t, found, "property %q is missing", name)
	return &value
}

func validationRules(schema *apiextensionsv1.JSONSchemaProps) []string {
	rules := make([]string, 0, len(schema.XValidations))
	for _, validation := range schema.XValidations {
		rules = append(rules, validation.Rule)
	}
	return rules
}

func requireStructural(t *testing.T, schema *apiextensionsv1.JSONSchemaProps) {
	t.Helper()

	internal := &apiextensions.JSONSchemaProps{}
	require.NoError(t, apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(schema, internal, nil))
	structural, err := structuralschema.NewStructural(internal)
	require.NoError(t, err)
	require.Empty(t, structuralschema.ValidateStructural(field.NewPath("openAPIV3Schema"), structural))
}
