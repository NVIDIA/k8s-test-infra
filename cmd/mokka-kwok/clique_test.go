// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apixv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

func TestDaemonReportMatchesDriverSchema(t *testing.T) {
	t.Parallel()
	// The unmodified driver CRD comes from deployments/helm/dra-driver-nvidia-gpu/crds
	// in NVIDIA/k8s-dra-driver-gpu. Use the version pinned by the e2e suite.
	matrix, err := os.ReadFile("../../tests/e2e/VERSION-MATRIX.md")
	require.NoError(t, err)
	require.Contains(t, string(matrix), "| DRA Driver (GPU) | 0.5.0 |")
	raw, err := os.ReadFile("testdata/computedomaincliques-v0.5.0.yaml")
	require.NoError(t, err)
	var crd apixv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(raw, &crd))
	schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	require.Equal(t, []string{"nodeName"}, schema.Properties["daemons"].XListMapKeys)
	var internal apiextensions.JSONSchemaProps
	require.NoError(t, apixv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(schema, &internal, nil))
	validate, _, err := validation.NewSchemaValidator(&internal)
	require.NoError(t, err)

	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	d := newDaemonReports(client, kubefake.NewSimpleClientset().CoreV1(), "drivers")
	var applied *unstructured.Unstructured
	client.PrependReactor("patch", "computedomaincliques", func(a clienttesting.Action) (bool, runtime.Object, error) {
		patch := a.(clienttesting.PatchAction)
		require.Equal(t, types.ApplyPatchType, patch.GetPatchType())
		applied = &unstructured.Unstructured{}
		require.NoError(t, json.Unmarshal(patch.GetPatch(), applied))
		require.NoError(t, validation.ValidateCustomResource(nil, applied.Object, validate).ToAggregate())
		return true, applied, nil
	})
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "daemon", Namespace: "drivers", UID: "pod-uid", Labels: map[string]string{domainLabel: "domain"}}, Spec: corev1.PodSpec{NodeName: "worker"}, Status: corev1.PodStatus{PodIP: "10.1.2.3", Phase: corev1.PodRunning}}
	for _, want := range []string{"NotReady", "Ready"} {
		require.NoError(t, d.publish(t.Context(), pod, "domain.fabric.0", "fabric.0", 3))
		require.Equal(t, "drivers", applied.GetNamespace())
		require.Equal(t, "domain.fabric.0", applied.GetName())
		require.Equal(t, pod.UID, applied.GetOwnerReferences()[0].UID)
		require.Equal(t, []any{map[string]any{"nodeName": "worker", "ipAddress": "10.1.2.3", "index": int64(3), "cliqueID": "fabric.0", "status": want}}, applied.Object["daemons"])
		// An identical report from another manager must still be applied.
		require.NoError(t, d.watch.GetStore().Update(applied))
		client.ClearActions()
		require.NoError(t, d.publish(t.Context(), pod, "domain.fabric.0", "fabric.0", 3))
		require.Len(t, client.Actions(), 1)
		applied.SetManagedFields([]metav1.ManagedFieldsEntry{testCliqueOwnership(reportManager("worker"), "pod-uid")})
		require.NoError(t, d.watch.GetStore().Update(applied))
		client.ClearActions()
		require.NoError(t, d.publish(t.Context(), pod, "domain.fabric.0", "fabric.0", 3))
		require.Empty(t, client.Actions())
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	}
}

func TestDaemonIdentityAndReadinessChanges(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "drivers", Labels: map[string]string{domainLabel: "uid"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "computedomain-daemon-uid", Controller: ptr.To(true)}}}}
	require.Equal(t, "uid", daemonDomain(pod, "drivers"))
	require.Empty(t, daemonDomain(pod, "elsewhere"))
	updated := pod.DeepCopy()
	updated.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	require.True(t, daemonChanged(pod, updated))
	updated = pod.DeepCopy()
	updated.Status.PodIP = "10.1.2.3"
	require.True(t, daemonChanged(pod, updated))
	updated = pod.DeepCopy()
	updated.OwnerReferences = nil
	require.Empty(t, daemonDomain(updated, "drivers"))
}

func testCliqueOwnership(manager, uid string) metav1.ManagedFieldsEntry {
	return metav1.ManagedFieldsEntry{Manager: manager, Operation: metav1.ManagedFieldsOperationApply, FieldsV1: metav1.NewFieldsV1(fmt.Sprintf(`{"f:daemons":{"k:{\"nodeName\":\"worker\"}":{}},"f:metadata":{"f:ownerReferences":{"k:{\"uid\":\"%s\"}":{}}}}`, uid))}
}

func TestCliqueDeletionRequiresExclusiveOwnership(t *testing.T) {
	t.Parallel()
	manager := reportManager("worker")
	o := cliqueObject("drivers", "domain.fabric.0")
	mine := testCliqueOwnership(manager, "old")
	o.SetManagedFields([]metav1.ManagedFieldsEntry{mine})
	require.True(t, ownsCliqueEntry(o, "worker", "old", true))
	require.False(t, ownsCliqueEntry(o, "worker", "replacement", true))
	o.SetManagedFields([]metav1.ManagedFieldsEntry{mine, testCliqueOwnership("other", "old")})
	require.False(t, ownsCliqueEntry(o, "worker", "old", true))
	require.True(t, ownsCliqueEntry(o, "worker", "old", false))
}
