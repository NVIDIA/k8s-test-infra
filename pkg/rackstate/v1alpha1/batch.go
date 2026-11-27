// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package v1alpha1

import (
	"encoding/json"
	"fmt"
)

// RackRequest selects an exact rack instance and its minimum usable delivery.
type RackRequest struct {
	Object
	MinRackDeliveryGeneration int64 `json:"minRackDeliveryGeneration"`
}

// RackStatus describes why one rack could not be served.
type RackStatus string

// Per-rack retry statuses apply independently to each requested rack.
const (
	NotReady RackStatus = "NotReady"
	NotFound RackStatus = "NotFound"
)

// RackResult carries either the complete state or a per-rack retry result.
type RackResult struct {
	*Rack
	Reference Object
	Status    RackStatus
}

// MarshalJSON keeps successful entries identical to the compiled rack and
// sends only the requested identity and status when its state is unavailable.
func (r RackResult) MarshalJSON() ([]byte, error) {
	if r.Rack != nil {
		return json.Marshal(r.Rack)
	}
	return json.Marshal(struct {
		Rack   Object     `json:"rack"`
		Status RackStatus `json:"status"`
	}{r.Reference, r.Status})
}

// UnmarshalJSON restores the same result variant and identity that MarshalJSON sends.
func (r *RackResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		Rack
		Status RackStatus `json:"status"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	next := RackResult{Reference: wire.Hardware.Rack, Status: wire.Status}
	switch wire.Status {
	case "":
		next.Rack = &wire.Rack
	case NotFound, NotReady:
	default:
		return fmt.Errorf("unknown rack result status %q", wire.Status)
	}
	*r = next
	return nil
}

// BatchRequest lists the racks to read in client order.
type BatchRequest struct {
	Racks []RackRequest `json:"racks"`
}

// BatchResponse contains one result for every requested rack.
type BatchResponse struct {
	APIVersion string       `json:"apiVersion"`
	Racks      []RackResult `json:"racks"`
}
