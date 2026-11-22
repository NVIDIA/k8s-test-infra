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

// AssignmentVersion identifies the legacy format used by the projection writer.
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
	AssignmentRevision          int64            `json:"-"`
	RackDeliveryGenerationFloor int64            `json:"-"`
	Assigned                    bool             `json:"assigned,omitempty"`
	Version                     int              `json:"v"`
	Inventory                   ObjectReference  `json:"inventory"`
	Rack                        ObjectReference  `json:"rack"`
	Profile                     ProfileReference `json:"profile"`
	RackGroup                   string           `json:"rackGroup"`
	RackIndex                   int32            `json:"rackIndex"`
	NodeIndex                   int32            `json:"nodeIndex"`
	NodeUID                     types.UID        `json:"nodeUID"`
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
	if assignment.Version == publicationVersion {
		return decodePublication(value, assignment.Assigned)
	}
	if assignment.Version != AssignmentVersion {
		return Assignment{}, fmt.Errorf("unsupported Node assignment version %d", assignment.Version)
	}
	assignment.Assigned = true
	return assignment, nil
}

// PublishedBinding describes the exact rack slot and the earliest usable rack state.
type PublishedBinding struct {
	Rack                        ObjectReference `json:"rack"`
	RackGroup                   string          `json:"rackGroup"`
	RackIndex                   int32           `json:"rackIndex"`
	NodeIndex                   int32           `json:"nodeIndex"`
	RackDeliveryGenerationFloor int64           `json:"rackDeliveryGenerationFloor"`
}

const publicationVersion = 2

type publication struct {
	Version            int       `json:"v"`
	NodeUID            types.UID `json:"nodeUID"`
	AssignmentRevision int64     `json:"assignmentRevision"`
	Assigned           *bool     `json:"assigned"`
	*PublishedBinding
}

func (p publication) validate() error {
	if p.NodeUID == "" || p.AssignmentRevision < 1 || p.Assigned == nil {
		return errors.New("v2 assignment requires a node UID, positive revision and assigned flag")
	}
	if *p.Assigned != (p.PublishedBinding != nil) {
		return errors.New("only an assigned node can have rack fields")
	}
	if b := p.PublishedBinding; b != nil && !b.valid() {
		return errors.New("incomplete v2 rack binding")
	}
	return nil
}

func (b *PublishedBinding) valid() bool {
	return b.Rack.Name != "" && b.Rack.UID != "" && b.RackGroup != "" && b.RackIndex >= 0 && b.NodeIndex >= 0 && b.RackDeliveryGenerationFloor > 0
}

// EncodePublication encodes a v2 assignment. A nil binding encodes the release tombstone.
func EncodePublication(uid types.UID, revision int64, binding *PublishedBinding) (string, error) {
	assigned := binding != nil
	p := publication{Version: publicationVersion, NodeUID: uid, AssignmentRevision: revision, Assigned: &assigned, PublishedBinding: binding}
	if err := p.validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(p)
	return string(raw), err
}

func decodePublication(value string, assigned bool) (Assignment, error) {
	var p publication
	if assigned {
		// Zero is a valid coordinate. Reserve a negative value for a missing index.
		p.PublishedBinding = &PublishedBinding{RackIndex: -1, NodeIndex: -1}
	}
	if err := json.Unmarshal([]byte(value), &p); err != nil {
		return Assignment{}, err
	}
	if err := p.validate(); err != nil {
		return Assignment{}, err
	}
	a := Assignment{Version: p.Version, NodeUID: p.NodeUID, AssignmentRevision: p.AssignmentRevision, Assigned: *p.Assigned}
	if b := p.PublishedBinding; b != nil {
		a.Rack, a.RackGroup, a.RackIndex, a.NodeIndex = b.Rack, b.RackGroup, b.RackIndex, b.NodeIndex
		a.RackDeliveryGenerationFloor = b.RackDeliveryGenerationFloor
	}
	return a, nil
}
