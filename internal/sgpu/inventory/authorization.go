// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package inventory

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/allocate"
	rackrender "github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory/rack"
)

// ProjectionTargetAllowed verifies that the rack matches controller
// materialization and its binding satisfies allocation policy. Deployment
// authorization must still reserve SGPURack writes to the controller because
// Kubernetes field-manager names are caller-selected, not identities.
func ProjectionTargetAllowed(
	cache Cache,
	rack *mokkav1alpha1.SGPURack,
	slot *mokkav1alpha1.SGPURackNode,
	node *corev1.Node,
) (bool, error) {
	if !projectionBindingIdentityValid(rack, slot, node) {
		return false, nil
	}
	if !controllerOwnsRackSpec(rack) {
		return false, nil
	}
	inventory, valid, err := projectionInventory(cache, rack)
	if err != nil || !valid {
		return false, err
	}
	group, valid, err := projectionGroup(cache, inventory, rack.Spec.Identity.RackGroup)
	if err != nil || !valid {
		return false, err
	}
	return projectionTargetMatches(rack, slot, node, inventory, group)
}

func projectionBindingIdentityValid(
	rack *mokkav1alpha1.SGPURack,
	slot *mokkav1alpha1.SGPURackNode,
	node *corev1.Node,
) bool {
	if rack == nil || slot == nil || node == nil {
		return false
	}
	if rack.DeletionTimestamp != nil || node.DeletionTimestamp != nil ||
		node.Labels[allocate.EligibleNodeLabel] != "true" {
		return false
	}
	return slot.BoundTo(node.Name, node.UID)
}

func projectionInventory(
	cache Cache,
	rack *mokkav1alpha1.SGPURack,
) (*mokkav1alpha1.SGPUInventory, bool, error) {
	if !rack.OwnerMatchesInventoryRef() {
		return nil, false, nil
	}
	ref := rack.Spec.InventoryRef
	inventory, err := cache.Inventory(ref.Name)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get projection inventory %q: %w", ref.Name, err)
	}
	if !projectionInventoryValid(inventory, ref.UID) {
		return nil, false, nil
	}
	return inventory, true, nil
}

func projectionInventoryValid(inventory *mokkav1alpha1.SGPUInventory, uid types.UID) bool {
	if inventory.UID != uid || inventory.DeletionTimestamp != nil {
		return false
	}
	return validateInventory(inventory) == nil && validateInventoryRackCapacity(inventory) == nil
}

func projectionGroup(
	cache Cache,
	inventory *mokkav1alpha1.SGPUInventory,
	groupID string,
) (resolvedGroup, bool, error) {
	resolved, _, err := (&Reconciler{cache: cache}).resolveGroups(inventory)
	if err != nil {
		return resolvedGroup{}, false, err
	}
	if validateResolvedCapacity(resolved) != nil {
		return resolvedGroup{}, false, nil
	}
	resolved, _ = validateGroupMaterialization(inventory, resolved)
	for _, group := range resolved {
		if group.group.ID == groupID {
			return group, true, nil
		}
	}
	return resolvedGroup{}, false, nil
}

func projectionTargetMatches(
	rack *mokkav1alpha1.SGPURack,
	slot *mokkav1alpha1.SGPURackNode,
	node *corev1.Node,
	inventory *mokkav1alpha1.SGPUInventory,
	group resolvedGroup,
) (bool, error) {
	rendered, err := rackrender.RenderRackWithRevision(rackrender.RackInput{
		InventoryName: inventory.Name,
		InventoryUID:  inventory.UID,
		Group:         group.group,
		RackIndex:     rack.Spec.Identity.RackIndex,
		Profile:       group.profile,
	}, group.revision)
	if err != nil {
		return false, nil
	}
	if rack.Name != rendered.Name ||
		!equality.Semantic.DeepEqual(rack.Spec.WithoutBindings(), rendered.Spec.WithoutBindings()) {
		return false, nil
	}
	observed := rack.Spec.NodeByIndex(slot.Index)
	if observed == nil || !equality.Semantic.DeepEqual(observed, slot) {
		return false, nil
	}
	selector, err := allocate.CompilePlacementSelector(group.group.NodeSelector())
	if err != nil {
		return false, nil
	}
	return selector.Matches(labels.Set(node.Labels)), nil
}

func controllerOwnsRackSpec(rack *mokkav1alpha1.SGPURack) bool {
	owned := false
	for _, entry := range rack.ManagedFields {
		if !rackSpecManagedFieldsEntry(entry) {
			continue
		}
		ownsSpec, valid := fieldsV1OwnsTopLevel(entry.FieldsV1, "f:spec")
		if !valid {
			return false
		}
		if !ownsSpec {
			continue
		}
		if entry.Manager != RackFieldManager {
			return false
		}
		owned = owned || entry.Operation == metav1.ManagedFieldsOperationApply ||
			entry.Operation == metav1.ManagedFieldsOperationUpdate
	}
	return owned
}

func rackSpecManagedFieldsEntry(entry metav1.ManagedFieldsEntry) bool {
	return entry.Subresource == "" && entry.FieldsType == "FieldsV1" && entry.FieldsV1 != nil &&
		entry.APIVersion == mokkav1alpha1.SchemeGroupVersion.String()
}

func fieldsV1OwnsTopLevel(fields *metav1.FieldsV1, key string) (bool, bool) {
	decoder := json.NewDecoder(fields.GetRawReader())
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false, false
	}
	for decoder.More() {
		token, err = decoder.Token()
		name, stringKey := token.(string)
		if err != nil || !stringKey {
			return false, false
		}
		if name == key {
			return true, true
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return false, false
		}
	}
	token, err = decoder.Token()
	return false, err == nil && token == json.Delim('}')
}
