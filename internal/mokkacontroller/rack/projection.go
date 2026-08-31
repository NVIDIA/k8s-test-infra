// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rack

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/internal/controlplane/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/allocate"
	"github.com/NVIDIA/k8s-test-infra/internal/mokka/materialize"
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
	if rack == nil || slot == nil || slot.NodeRef == nil || node == nil {
		return false
	}
	if rack.DeletionTimestamp != nil || node.DeletionTimestamp != nil ||
		node.Labels[allocate.EligibleNodeLabel] != "true" {
		return false
	}
	return slot.NodeRef.Name == node.Name && slot.NodeRef.UID == node.UID
}

func projectionInventory(
	cache Cache,
	rack *mokkav1alpha1.SGPURack,
) (*mokkav1alpha1.SGPUInventory, bool, error) {
	owner, valid := projectionOwner(rack)
	if !valid {
		return nil, false, nil
	}
	inventory, err := cache.Inventory(owner.Name)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get projection inventory %q: %w", owner.Name, err)
	}
	if !projectionInventoryValid(inventory, owner.UID) {
		return nil, false, nil
	}
	return inventory, true, nil
}

func projectionOwner(rack *mokkav1alpha1.SGPURack) (*metav1.OwnerReference, bool) {
	owner := controllerInventoryOwner(rack)
	if owner == nil || owner.Name == "" || owner.UID == "" {
		return nil, false
	}
	if rack.Spec.InventoryRef.Name != owner.Name || rack.Spec.InventoryRef.UID != owner.UID {
		return nil, false
	}
	return owner, true
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
	rendered, err := materialize.RenderRackWithRevision(materialize.RackInput{
		InventoryName: inventory.Name,
		InventoryUID:  inventory.UID,
		Group:         group.group,
		RackIndex:     rack.Spec.Identity.RackIndex,
		Profile:       group.profile,
	}, group.revision)
	if err != nil {
		return false, nil
	}
	if rack.Name != rendered.Name || !rackTemplateMatches(rack.Spec, rendered.Spec) {
		return false, nil
	}
	observed := boundSlot(rack, slot.Index)
	if observed == nil || !equality.Semantic.DeepEqual(observed, slot) {
		return false, nil
	}
	var placement *metav1.LabelSelector
	if group.group.Placement != nil {
		placement = group.group.Placement.NodeSelector
	}
	selector, err := allocate.CompilePlacementSelector(placement)
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

func rackTemplateMatches(observed, desired mokkav1alpha1.SGPURackSpec) bool {
	observedCopy := observed.DeepCopy()
	desiredCopy := desired.DeepCopy()
	for index := range observedCopy.Nodes {
		observedCopy.Nodes[index].NodeRef = nil
	}
	for index := range desiredCopy.Nodes {
		desiredCopy.Nodes[index].NodeRef = nil
	}
	return equality.Semantic.DeepEqual(observedCopy, desiredCopy)
}

func boundSlot(rack *mokkav1alpha1.SGPURack, index int32) *mokkav1alpha1.SGPURackNode {
	for slotIndex := range rack.Spec.Nodes {
		if rack.Spec.Nodes[slotIndex].Index == index {
			return &rack.Spec.Nodes[slotIndex]
		}
	}
	return nil
}
