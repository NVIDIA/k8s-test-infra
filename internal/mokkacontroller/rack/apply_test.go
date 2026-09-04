// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	require.Empty(t, writer.updateCalls)
	require.Len(t, writer.createCalls, 1)
	require.Equal(t, RackFieldManager, writer.createCalls[0].options.FieldManager)
	require.Equal(t, desired, writer.createCalls[0].rack)
	require.Empty(t, writer.createCalls[0].rack.Status.Conditions)
}

func TestUpdateRackPreservesUnrelatedMetadataWithResourceVersion(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	existing := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	existing.UID = "rack-uid"
	existing.ResourceVersion = "17"
	setRackSpecManagedFields(existing, RackFieldManager, metav1.ManagedFieldsOperationUpdate)
	existing.Labels["example.com/user-label"] = "keep"
	existing.Annotations["example.com/user-annotation"] = "keep"
	existing.Finalizers = append(existing.Finalizers, "example.com/user-finalizer")
	existing.OwnerReferences = append(existing.OwnerReferences, metav1.OwnerReference{
		APIVersion: "example.com/v1", Kind: "Thing", Name: "keep", UID: "thing-uid",
	})
	desiredSpec := *existing.Spec.DeepCopy()
	desiredSpec.Identity.RackIndex = 4
	updated := existing.DeepCopy()
	updated.Spec = desiredSpec
	writer := &recordingRackWriter{updateResult: updated}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, existing, existing.Name, desiredSpec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.updateCalls, 1)

	call := writer.updateCalls[0]
	require.Equal(t, RackFieldManager, call.options.FieldManager)
	require.Equal(t, existing.ResourceVersion, call.rack.ResourceVersion)
	require.Equal(t, "keep", call.rack.Labels["example.com/user-label"])
	require.Equal(t, "keep", call.rack.Annotations["example.com/user-annotation"])
	require.Contains(t, call.rack.Finalizers, "example.com/user-finalizer")
	require.Contains(t, call.rack.OwnerReferences, metav1.OwnerReference{
		APIVersion: "example.com/v1", Kind: "Thing", Name: "keep", UID: "thing-uid",
	})
}

func TestRackUpdateClearsReleasedNodeBinding(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	existing := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
		Nodes: []mokkav1alpha1.SGPURackNode{{
			Index: 0,
			NodeRef: &mokkav1alpha1.SGPUNodeReference{
				Name: "node", UID: "old-node-uid",
			},
			GPUs: []mokkav1alpha1.SGPURackGPU{{Index: 0}},
		}},
	})
	existing.UID = "rack-uid"
	existing.ResourceVersion = "17"
	setRackSpecManagedFields(existing, RackFieldManager, metav1.ManagedFieldsOperationUpdate)
	desiredSpec := *existing.Spec.DeepCopy()
	desiredSpec.Nodes[0].NodeRef = nil
	updated := existing.DeepCopy()
	updated.Spec = desiredSpec
	writer := &recordingRackWriter{updateResult: updated}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, existing, existing.Name, desiredSpec,
	)

	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.updateCalls, 1)
	require.Nil(t, writer.updateCalls[0].rack.Spec.Nodes[0].NodeRef)
	require.Equal(t, RackFieldManager, writer.updateCalls[0].options.FieldManager)
}

func TestRackUpdateRejectsForeignSpecCoOwner(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	existing := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	existing.UID = "rack-uid"
	existing.ResourceVersion = "17"
	setRackSpecManagedFields(existing, RackFieldManager, metav1.ManagedFieldsOperationUpdate)
	foreign := existing.ManagedFields[0]
	foreign.Manager = "foreign-controller"
	existing.ManagedFields = append(existing.ManagedFields, foreign)
	desiredSpec := *existing.Spec.DeepCopy()
	desiredSpec.Identity.RackIndex = 4
	writer := &recordingRackWriter{}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, existing, existing.Name, desiredSpec,
	)

	require.Error(t, err)
	var ownershipErr *OwnershipConflictError
	require.ErrorAs(t, err, &ownershipErr)
	require.False(t, changed)
	require.Equal(t, &ownershipErr.Conflict, conflict)
	require.Empty(t, writer.updateCalls)
}

func TestRackUpdateIsIdempotentAndNeverAdoptsForeignOwner(t *testing.T) {
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
	require.Empty(t, writer.updateCalls)

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
	require.Empty(t, writer.updateCalls)
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
	setRackSpecManagedFields(live, RackFieldManager, metav1.ManagedFieldsOperationUpdate)
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
	writer.updateResult = updated
	changed, conflict, err = reconciler.createOrUpdateRack(
		context.Background(), inventory, nil, desired.Name, desired.Spec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.createCalls, 2)
	require.Equal(t, 1, writer.getCalls)
	require.Len(t, writer.updateCalls, 1)
	require.Equal(t, live.ResourceVersion, writer.updateCalls[0].rack.ResourceVersion)
	require.Equal(t, RackFieldManager, writer.updateCalls[0].options.FieldManager)
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
	require.Empty(t, writer.updateCalls)
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
		require.Empty(t, writer.updateCalls)
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
		require.Empty(t, writer.updateCalls)
	})
}

func TestCreatedRackUsesSameManagerForLaterUpdate(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	desired := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	created := desired.DeepCopy()
	created.UID = "rack-uid"
	created.ResourceVersion = "1"
	setRackSpecManagedFields(created, RackFieldManager, metav1.ManagedFieldsOperationUpdate)
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
	writer.updateResult = created.DeepCopy()
	writer.updateResult.Spec = updatedSpec
	changed, conflict, err = reconciler.createOrUpdateRack(
		context.Background(), inventory, created, desired.Name, updatedSpec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Len(t, writer.createCalls, 1)
	require.Len(t, writer.updateCalls, 1)
	require.Equal(t, RackFieldManager, writer.updateCalls[0].options.FieldManager)
}

func TestRackUpdateRetriesResourceVersionConflict(t *testing.T) {
	inventory := testInventory("inventory", "inventory-uid", "profile", 1)
	existing := newRack(inventory, "rack", mokkav1alpha1.SGPURackSpec{
		InventoryRef: mokkav1alpha1.SGPURackInventoryReference{Name: inventory.Name, UID: inventory.UID},
		Identity:     mokkav1alpha1.SGPURackIdentity{RackGroup: "group"},
	})
	existing.UID = "rack-uid"
	existing.ResourceVersion = "17"
	setRackSpecManagedFields(existing, RackFieldManager, metav1.ManagedFieldsOperationUpdate)
	latest := existing.DeepCopy()
	latest.ResourceVersion = "18"
	desiredSpec := *existing.Spec.DeepCopy()
	desiredSpec.Identity.RackIndex = 4
	updated := latest.DeepCopy()
	updated.Spec = desiredSpec
	writer := &recordingRackWriter{
		getResult: latest, updateResult: updated,
		updateErrors: []error{apierrors.NewConflict(
			mokkav1alpha1.Resource("sgpuracks"), existing.Name, errors.New("resource version changed"),
		)},
	}
	reconciler := &Reconciler{racks: writer}

	changed, conflict, err := reconciler.createOrUpdateRack(
		context.Background(), inventory, existing, existing.Name, desiredSpec,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, conflict)
	require.Equal(t, 2, writer.getCalls)
	require.Len(t, writer.updateCalls, 2)
	require.Equal(t, "17", writer.updateCalls[0].rack.ResourceVersion)
	require.Equal(t, "18", writer.updateCalls[1].rack.ResourceVersion)
}

type rackUpdateCall struct {
	rack    *mokkav1alpha1.SGPURack
	options metav1.UpdateOptions
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
	updateResult *mokkav1alpha1.SGPURack
	updateErrors []error
	updateCalls  []rackUpdateCall
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

func (w *recordingRackWriter) Update(
	_ context.Context,
	rack *mokkav1alpha1.SGPURack,
	options metav1.UpdateOptions,
) (*mokkav1alpha1.SGPURack, error) {
	w.updateCalls = append(w.updateCalls, rackUpdateCall{rack: rack.DeepCopy(), options: options})
	if len(w.updateErrors) > 0 {
		err := w.updateErrors[0]
		w.updateErrors = w.updateErrors[1:]
		return nil, err
	}
	return w.updateResult, nil
}

func (*recordingRackWriter) Delete(context.Context, string, metav1.DeleteOptions) error { return nil }

func setRackSpecManagedFields(
	rack *mokkav1alpha1.SGPURack,
	manager string,
	operation metav1.ManagedFieldsOperationType,
) {
	rack.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: manager, Operation: operation,
		APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), FieldsType: "FieldsV1",
		FieldsV1: metav1.NewFieldsV1(`{"f:spec":{}}`),
	}}
}

func TestOwnershipConflictErrorUnwrapsCause(t *testing.T) {
	cause := apierrors.NewConflict(mokkav1alpha1.Resource("sgpuracks"), "rack", errors.New("conflict"))
	err := &OwnershipConflictError{Conflict: OwnershipConflict{RackName: "rack"}, Cause: cause}
	require.ErrorIs(t, err, cause)
}
