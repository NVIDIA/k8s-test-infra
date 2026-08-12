// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package mokkacontrollerchart_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func TestChartDefaultsAndSchema(t *testing.T) {
	valuesData, err := os.ReadFile("values.yaml")
	require.NoError(t, err)
	var values map[string]any
	require.NoError(t, yaml.Unmarshal(valuesData, &values))
	require.Equal(t, float64(2), values["replicaCount"])
	require.NotEmpty(t, values["resources"])

	schemaData, err := os.ReadFile("values.schema.json")
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(schemaData, &schema))
	require.Equal(t, "https://json-schema.org/draft-07/schema#", schema["$schema"])
	require.Equal(t, false, schema["additionalProperties"])
}

func TestHelmRenderIncludesOperationalResourcesAndExactRBAC(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	command := exec.Command(
		helm, "template", "mokka-controller", ".",
		"--namespace", "mokka-system",
		"--set", "image.repository=registry.example/mokka-controller",
		"--set", "image.tag=test",
		"--set", "controller.workers=7",
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))

	objects := decodeObjects(t, output)
	for _, kind := range []string{
		"ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding", "Service", "Deployment",
	} {
		require.Contains(t, objects, kind)
	}

	deployment := objects["Deployment"]
	replicas, found, err := unstructured.NestedInt64(deployment.Object, "spec", "replicas")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(2), replicas)
	containers, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, containers, 1)
	container := containers[0].(map[string]any)
	require.Equal(t, "registry.example/mokka-controller:test", container["image"])
	args := stringSlice(t, container["args"])
	require.Contains(t, args, "--workers=7")
	require.Contains(t, args, "--leader-election-namespace=mokka-system")
	require.Contains(t, container, "livenessProbe")
	require.Contains(t, container, "readinessProbe")
	require.Contains(t, container, "resources")

	clusterRole := &rbacv1.ClusterRole{}
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(objects["ClusterRole"].Object, clusterRole))
	require.ElementsMatch(t, []rbacv1.PolicyRule{
		{APIGroups: []string{"mokka.nvidia.com"}, Resources: []string{"sgpuprofiles"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"mokka.nvidia.com"}, Resources: []string{"sgpuinventories"}, Verbs: []string{"get", "list", "watch", "update"}},
		{APIGroups: []string{"mokka.nvidia.com"}, Resources: []string{"sgpuinventories/status"}, Verbs: []string{"update"}},
		{APIGroups: []string{"mokka.nvidia.com"}, Resources: []string{"sgpuracks"}, Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
		{APIGroups: []string{"mokka.nvidia.com"}, Resources: []string{"sgpuracks/status"}, Verbs: []string{"update"}},
		{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get", "list", "watch", "patch"}},
	}, clusterRole.Rules)

	role := &rbacv1.Role{}
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(objects["Role"].Object, role))
	require.Equal(t, "mokka-system", role.Namespace)
	require.Equal(t, []rbacv1.PolicyRule{{
		APIGroups: []string{"coordination.k8s.io"}, Resources: []string{"leases"}, Verbs: []string{"get", "create", "update"},
	}}, role.Rules)
	for _, rule := range append(slices.Clone(clusterRole.Rules), role.Rules...) {
		require.NotContains(t, rule.Resources, "events")
	}
}

func decodeObjects(t *testing.T, data []byte) map[string]*unstructured.Unstructured {
	t.Helper()
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	objects := make(map[string]*unstructured.Unstructured)
	for {
		object := &unstructured.Unstructured{}
		err := decoder.Decode(object)
		if err == io.EOF {
			break
		}
		if err != nil {
			require.NoError(t, err)
		}
		if object.GetKind() != "" {
			objects[object.GetKind()] = object
		}
	}
	return objects
}

func stringSlice(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	require.True(t, ok)
	result := make([]string, len(items))
	for i := range items {
		result[i], ok = items[i].(string)
		require.True(t, ok)
	}
	return result
}
