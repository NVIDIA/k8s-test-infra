// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package rack

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
)

func TestCreateRackUsesNonForcedServerSideApply(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	desired := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: 3},
	})
	writer := &recordingRackWriter{getErr: apierrors.NewNotFound(mokkav1alpha1.Resource("sgpuracks"), desired.Name)}
	writer.patchResult = desired.DeepCopy()
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, desired.Name, desired.Spec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.patchCalls, 1)
	call := writer.patchCalls[0]
	require.Equal(t, types.ApplyPatchType, call.patchType)
	require.Equal(t, RackFieldManager, call.options.FieldManager)
	require.NotNil(t, call.options.Force)
	require.False(t, *call.options.Force)
	require.Empty(t, call.subresources)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(call.data, &payload))
	require.Equal(t, mokkav1alpha1.SchemeGroupVersion.String(), payload["apiVersion"])
	require.Equal(t, "SGPURack", payload["kind"])
	require.Equal(t, map[string]any{
		"name": desired.Name,
		"labels": map[string]any{
			InventoryNameLabel: inventory.Name,
			RackGroupLabel:     "group",
			RackIndexLabel:     "3",
		},
		"annotations": map[string]any{InventoryUIDAnnotation: string(inventory.UID)},
		"finalizers":  []any{RackFinalizer},
		"ownerReferences": []any{map[string]any{
			"apiVersion":         mokkav1alpha1.SchemeGroupVersion.String(),
			"kind":               "SGPUInventory",
			"name":               inventory.Name,
			"uid":                string(inventory.UID),
			"controller":         true,
			"blockOwnerDeletion": true,
		}},
	}, payload["metadata"])
	require.Equal(t, map[string]any{
		"inventoryRef": map[string]any{"name": inventory.Name, "uid": string(inventory.UID)},
		"profileRef":   map[string]any{"name": "", "uid": "", "generation": float64(0), "revision": ""},
		"identity": map[string]any{
			"rackGroup": "group", "rackIndex": float64(3), "fabricUUID": "", "cliqueID": float64(0),
		},
		"slots": nil,
	}, payload["spec"])
}

func TestUpdateRackAppliesOnlyControllerFieldsWithResourceVersion(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	existing := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	existing.UID = "rack-uid"
	existing.ResourceVersion = "17"
	existing.Labels["example.com/user-label"] = "keep"
	existing.Annotations["example.com/user-annotation"] = "keep"
	existing.Finalizers = append(existing.Finalizers, "example.com/user-finalizer")
	existing.OwnerReferences = append(existing.OwnerReferences, metav1.OwnerReference{
		APIVersion: "example.com/v1", Kind: "Thing", Name: "keep", UID: "thing-uid",
	})
	desiredSpec := *existing.Spec.DeepCopy()
	desiredSpec.Identity.RackIndex = 4
	writer := &recordingRackWriter{getResult: existing.DeepCopy()}
	writer.patchResult = existing.DeepCopy()
	writer.patchResult.Spec = desiredSpec
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, existing, existing.Name, desiredSpec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.patchCalls, 1)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(writer.patchCalls[0].data, &payload))
	metadata := payload["metadata"].(map[string]any)
	require.Equal(t, existing.ResourceVersion, metadata["resourceVersion"])
	require.NotContains(t, metadata["labels"], "example.com/user-label")
	require.NotContains(t, metadata["annotations"], "example.com/user-annotation")
	require.NotContains(t, metadata["finalizers"], "example.com/user-finalizer")
	require.NotContains(t, metadata["ownerReferences"], map[string]any{
		"apiVersion": "example.com/v1", "kind": "Thing", "name": "keep", "uid": "thing-uid",
	})
}

func TestRackApplyIsIdempotentAndNeverAdoptsForeignOwner(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	existing := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	existing.Labels["example.com/user-label"] = "keep"
	writer := &recordingRackWriter{}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, existing, existing.Name, existing.Spec,
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, conflict)
	require.Empty(t, writer.patchCalls)

	foreignInventory := testInventory("foreign", "foreign-inventory-uid", "profile", 1)
	foreign := newRack(foreignInventory, existing.Name, existing.Spec)
	writer.getResult = foreign
	changed, conflict, err = reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, existing.Name, existing.Spec,
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, &OwnershipConflict{
		RackName: existing.Name, RackGroup: existing.Spec.Identity.RackGroup, OwnerUID: foreignInventory.UID,
	}, conflict)
	require.Empty(t, writer.patchCalls)
}

func TestRackFieldManagerConflictIsVisibleAndRetryable(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	existing := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	existing.UID = "rack-uid"
	existing.ResourceVersion = "17"
	desiredSpec := *existing.Spec.DeepCopy()
	desiredSpec.Identity.RackIndex = 4
	writer := &recordingRackWriter{
		getResult: existing,
		patchErr: apierrors.NewApplyConflict([]metav1.StatusCause{{
			Type: metav1.CauseTypeFieldManagerConflict, Field: ".spec.identity.rackIndex",
		}}, "owned by another manager"),
	}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, existing, existing.Name, desiredSpec,
	)
	require.Error(t, err)
	var ownershipErr *OwnershipConflictError
	require.ErrorAs(t, err, &ownershipErr)
	require.True(t, apierrors.IsConflict(err))
	require.False(t, changed)
	require.Equal(t, &ownershipErr.Conflict, conflict)
	require.Equal(t, existing.Name, conflict.RackName)
	require.Equal(t, inventory.UID, conflict.OwnerUID)
	require.Len(t, writer.patchCalls, 1, "field ownership cannot be resolved by immediate retries")
}

type rackPatchCall struct {
	name         string
	patchType    types.PatchType
	data         []byte
	options      metav1.PatchOptions
	subresources []string
}

type recordingRackWriter struct {
	getResult   *mokkav1alpha1.SGPURack
	getErr      error
	patchResult *mokkav1alpha1.SGPURack
	patchErr    error
	patchCalls  []rackPatchCall
}

func (w *recordingRackWriter) Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPURack, error) {
	return w.getResult, w.getErr
}

func (w *recordingRackWriter) Patch(
	_ context.Context,
	name string,
	patchType types.PatchType,
	data []byte,
	options metav1.PatchOptions,
	subresources ...string,
) (*mokkav1alpha1.SGPURack, error) {
	w.patchCalls = append(w.patchCalls, rackPatchCall{
		name: name, patchType: patchType, data: append([]byte(nil), data...), options: options,
		subresources: append([]string(nil), subresources...),
	})
	return w.patchResult, w.patchErr
}

func (*recordingRackWriter) Delete(context.Context, string, metav1.DeleteOptions) error { return nil }

func TestOwnershipConflictErrorUnwrapsCause(t *testing.T) {
	cause := apierrors.NewConflict(mokkav1alpha1.Resource("sgpuracks"), "rack", errors.New("conflict"))
	err := &OwnershipConflictError{Conflict: OwnershipConflict{RackName: "rack"}, Cause: cause}
	require.ErrorIs(t, err, cause)
}
