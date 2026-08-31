// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

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

func TestCreateRackUsesFieldManagedCreateWithoutReadOrApply(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	desired := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: 3},
	})
	created := desired.DeepCopy()
	created.UID = "rack-uid"
	created.ResourceVersion = "1"
	created.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: RackFieldManager, Operation: metav1.ManagedFieldsOperationUpdate,
	}}
	writer := &recordingRackWriter{createResult: created}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, desired.Name, desired.Spec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Zero(t, writer.getCalls)
	require.Empty(t, writer.patchCalls)
	require.Len(t, writer.createCalls, 1)
	require.Equal(t, RackFieldManager, writer.createCalls[0].options.FieldManager)
	require.Equal(t, desired, writer.createCalls[0].rack)
	require.Empty(t, writer.createCalls[0].rack.Status.Conditions)
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
	writer.createErr = apierrors.NewAlreadyExists(mokkav1alpha1.Resource("sgpuracks"), existing.Name)
	changed, conflict, err = reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, existing.Name, existing.Spec,
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, &OwnershipConflict{
		RackName: existing.Name, RackGroup: existing.Spec.Identity.RackGroup, OwnerUID: foreignInventory.UID,
	}, conflict)
	require.Len(t, writer.createCalls, 1)
	require.Equal(t, 1, writer.getCalls)
	require.Empty(t, writer.patchCalls)
}

func TestCreateRackAlreadyExistsUsesLiveRecreatedUID(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	desired := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group", RackIndex: 3},
	})
	created := desired.DeepCopy()
	created.UID = "original-rack-uid"
	created.ResourceVersion = "1"
	live := desired.DeepCopy()
	live.UID = "recreated-rack-uid"
	live.ResourceVersion = "23"
	live.Spec.Identity.RackIndex = 2
	updated := live.DeepCopy()
	updated.Spec = *desired.Spec.DeepCopy()
	writer := &recordingRackWriter{createResult: created}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, desired.Name, desired.Spec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)

	writer.createResult = &mokkav1alpha1.SGPURack{}
	writer.createErr = apierrors.NewAlreadyExists(mokkav1alpha1.Resource("sgpuracks"), desired.Name)
	writer.getResult = live
	writer.patchResult = updated
	changed, conflict, err = reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, desired.Name, desired.Spec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.createCalls, 2)
	require.Equal(t, 1, writer.getCalls)
	require.Len(t, writer.patchCalls, 1)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(writer.patchCalls[0].data, &payload))
	require.Equal(t, live.ResourceVersion, payload["metadata"].(map[string]any)["resourceVersion"])
}

func TestCreateRackAlreadyExistsSameUIDCacheLagIsIdempotent(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	desired := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	live := desired.DeepCopy()
	live.UID = "rack-uid"
	live.ResourceVersion = "17"
	writer := &recordingRackWriter{createResult: live}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, desired.Name, desired.Spec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)

	writer.createResult = nil
	writer.createErr = apierrors.NewAlreadyExists(mokkav1alpha1.Resource("sgpuracks"), desired.Name)
	writer.getResult = live
	changed, conflict, err = reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, desired.Name, desired.Spec,
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.createCalls, 2)
	require.Equal(t, 1, writer.getCalls)
	require.Empty(t, writer.patchCalls)
}

func TestCreateRackClassifiesPermanentAndTransientErrors(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	desired := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})

	t.Run("invalid is permanent materialization issue", func(t *testing.T) {
		writer := &recordingRackWriter{createErr: apierrors.NewInvalid(
			mokkav1alpha1.SchemeGroupVersion.WithKind("SGPURack").GroupKind(), desired.Name, nil,
		)}
		reconciler := &Reconciler{racks: writer}
		_, _, err := reconciler.createOrUpdateRack(
			context.Background(), inventory, nil, desired.Name, desired.Spec,
		)
		var materializationErr *profileMaterializationError
		require.ErrorAs(t, err, &materializationErr)
		require.True(t, apierrors.IsInvalid(err))
		require.Zero(t, writer.getCalls)
		require.Empty(t, writer.patchCalls)
	})

	t.Run("service unavailable remains retryable", func(t *testing.T) {
		writer := &recordingRackWriter{createErr: apierrors.NewServiceUnavailable("apiserver unavailable")}
		reconciler := &Reconciler{racks: writer}
		_, _, err := reconciler.createOrUpdateRack(
			context.Background(), inventory, nil, desired.Name, desired.Spec,
		)
		require.Error(t, err)
		require.True(t, apierrors.IsServiceUnavailable(err))
		require.Zero(t, writer.getCalls)
		require.Empty(t, writer.patchCalls)
	})
}

func TestCreatedRackUsesSameManagerForLaterNonForcedApply(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	desired := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	created := desired.DeepCopy()
	created.UID = "rack-uid"
	created.ResourceVersion = "1"
	created.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: RackFieldManager, Operation: metav1.ManagedFieldsOperationUpdate,
	}}
	writer := &recordingRackWriter{createResult: created}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, desired.Name, desired.Spec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)

	updatedSpec := *desired.Spec.DeepCopy()
	updatedSpec.Identity.RackIndex = 1
	writer.patchResult = created.DeepCopy()
	writer.patchResult.Spec = updatedSpec
	changed, conflict, err = reconciler.createOrUpdateRack(
		context.Background(), inventory, created, desired.Name, updatedSpec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.createCalls, 1)
	require.Len(t, writer.patchCalls, 1)
	call := writer.patchCalls[0]
	require.Equal(t, types.ApplyPatchType, call.patchType)
	require.Equal(t, RackFieldManager, call.options.FieldManager)
	require.NotNil(t, call.options.Force)
	require.False(t, *call.options.Force)
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

type rackCreateCall struct {
	rack    *mokkav1alpha1.SGPURack
	options metav1.CreateOptions
}

type recordingRackWriter struct {
	createResult *mokkav1alpha1.SGPURack
	createErr    error
	createCalls  []rackCreateCall
	getResult    *mokkav1alpha1.SGPURack
	getErr       error
	getCalls     int
	patchResult  *mokkav1alpha1.SGPURack
	patchErr     error
	patchCalls   []rackPatchCall
}

func (w *recordingRackWriter) Create(
	_ context.Context,
	rack *mokkav1alpha1.SGPURack,
	options metav1.CreateOptions,
) (*mokkav1alpha1.SGPURack, error) {
	w.createCalls = append(w.createCalls, rackCreateCall{rack: rack.DeepCopy(), options: options})
	return w.createResult, w.createErr
}

func (w *recordingRackWriter) Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPURack, error) {
	w.getCalls++
	return w.getResult, w.getErr
}

func (*recordingRackWriter) List(context.Context, metav1.ListOptions) (*mokkav1alpha1.SGPURackList, error) {
	return &mokkav1alpha1.SGPURackList{}, nil
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
