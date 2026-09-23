// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package assignment owns the versioned Node assignment annotation value.
package assignment

import (
	"encoding/json"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// AssignmentVersion is the current assignment annotation schema version.
const AssignmentVersion = 1

// ObjectReference identifies an exact inventory or rack instance.
type ObjectReference struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}

// ProfileReference identifies the rendered profile revision.
type ProfileReference struct {
	Name     string    `json:"name"`
	UID      types.UID `json:"uid"`
	Revision string    `json:"revision"`
}

// Assignment is the versioned Node annotation derived from one exact logical rack Node.
type Assignment struct {
	Version   int              `json:"v"`
	Inventory ObjectReference  `json:"inventory"`
	Rack      ObjectReference  `json:"rack"`
	Profile   ProfileReference `json:"profile"`
	RackGroup string           `json:"rackGroup"`
	RackIndex int32            `json:"rackIndex"`
	NodeIndex int32            `json:"nodeIndex"`
	NodeUID   types.UID        `json:"nodeUID"`
}

// EncodeAssignment serializes the compact annotation without whitespace.
func EncodeAssignment(rack *mokkav1alpha1.SGPURack, slot *mokkav1alpha1.SGPURackNode) (string, error) {
	if rack == nil || rack.Name == "" || rack.UID == "" || slot == nil || slot.NodeRef == nil || slot.NodeRef.UID == "" {
		return "", errors.New("assignment requires exact rack, logical Node, and Kubernetes Node identities")
	}
	encoded, err := json.Marshal(Assignment{
		Version:   AssignmentVersion,
		Inventory: ObjectReference{Name: rack.Spec.InventoryRef.Name, UID: rack.Spec.InventoryRef.UID},
		Rack:      ObjectReference{Name: rack.Name, UID: rack.UID},
		Profile:   ProfileReference{Name: rack.Spec.ProfileRef.Name, UID: rack.Spec.ProfileRef.UID, Revision: rack.Spec.ProfileRef.Revision},
		RackGroup: rack.Spec.Identity.RackGroup,
		RackIndex: rack.Spec.Identity.RackIndex,
		NodeIndex: slot.Index,
		NodeUID:   slot.NodeRef.UID,
	})
	if err != nil {
		return "", fmt.Errorf("encode Node assignment: %w", err)
	}
	return string(encoded), nil
}

// DecodeAssignment validates the complete compact Node assignment document.
func DecodeAssignment(value string) (Assignment, error) {
	var assignment Assignment
	if err := json.Unmarshal([]byte(value), &assignment); err != nil {
		return Assignment{}, fmt.Errorf("decode Node assignment: %w", err)
	}
	if assignment.Version != AssignmentVersion {
		return Assignment{}, fmt.Errorf("unsupported Node assignment version %d", assignment.Version)
	}
	return assignment, nil
}
