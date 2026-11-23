// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package v1alpha1

// RackRequest selects an exact rack instance and its minimum usable delivery.
type RackRequest struct {
	Object
	MinRackDeliveryGeneration int64 `json:"minRackDeliveryGeneration"`
}

type RackStatus string

const (
	NotReady RackStatus = "NotReady"
	NotFound RackStatus = "NotFound"
)

// RackResult carries either the complete state or a per-rack retry result.
type RackResult struct {
	*Rack
	Status RackStatus `json:"status,omitempty"`
}
