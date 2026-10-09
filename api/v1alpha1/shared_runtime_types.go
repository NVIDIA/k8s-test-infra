// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// RuntimeState is the effective runtime settings of a simulated GPU.
// Sparse: omitted fields inherit; explicit zero is a set value.
type RuntimeState struct {
	// +optional
	DeviceState DeviceState `json:"deviceState,omitempty"`

	// +optional
	Modes *RuntimeModes `json:"modes,omitempty"`

	// +optional
	Telemetry *RuntimeTelemetry `json:"telemetry,omitempty"`
}

// WithOverride returns a copy of s with each override applied in order as a
// JSON merge patch (RFC 7386): a field an override sets replaces the inherited
// value, an explicit zero included, and an omitted field inherits. Neither
// input is modified, and any may be nil.
func (s *RuntimeState) WithOverride(overrides ...*RuntimeState) (*RuntimeState, error) {
	merged, err := s.document()
	if err != nil {
		return nil, err
	}

	for _, override := range overrides {
		patch, err := override.document()
		if err != nil {
			return nil, err
		}

		mergePatch(merged, patch)
	}

	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode merged runtime state: %w", err)
	}

	var result RuntimeState

	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("decode merged runtime state: %w", err)
	}

	return &result, nil
}

// FieldPaths returns the sorted JSON paths of the values s replaces when
// applied as an override, such as "telemetry.temperature.gpuCelsius".
func (s *RuntimeState) FieldPaths() ([]string, error) {
	document, err := s.document()
	if err != nil {
		return nil, err
	}

	paths := leafJSONPaths(document)
	slices.Sort(paths)

	return paths, nil
}

// document returns s as a JSON object, empty for nil. Numbers decode as
// json.Number so that integers beyond float64 precision keep their value.
func (s *RuntimeState) document() (map[string]any, error) {
	document := map[string]any{}

	if s == nil {
		return document, nil
	}

	encoded, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode runtime state: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()

	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode runtime state: %w", err)
	}

	return document, nil
}

// DeviceState is a simulated GPU health state.
// +kubebuilder:validation:Enum=Healthy;Degraded;Failed
type DeviceState string

// DeviceState values.
const (
	DeviceStateHealthy  DeviceState = "Healthy"
	DeviceStateDegraded DeviceState = "Degraded"
	DeviceStateFailed   DeviceState = "Failed"
)

// RuntimeModes is the persistent NVML/CUDA mode settings.
type RuntimeModes struct {
	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	Persistence string `json:"persistence,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Default;Exclusive;Prohibited
	Compute string `json:"compute,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	MIG string `json:"mig,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	ECC string `json:"ecc,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	Accounting string `json:"accounting,omitempty"`
}

// RuntimeTelemetry is the synthetic NVML telemetry.
type RuntimeTelemetry struct {
	// NVML P-state, e.g. "P0".
	// +optional
	// +kubebuilder:validation:Pattern=`^P[0-9]+$`
	PerformanceState string `json:"performanceState,omitempty"`

	// +optional
	Utilization *UtilizationTelemetry `json:"utilization,omitempty"`

	// +optional
	Power *PowerTelemetry `json:"power,omitempty"`

	// +optional
	Temperature *TemperatureTelemetry `json:"temperature,omitempty"`

	// +optional
	Clocks *ClocksTelemetry `json:"clocks,omitempty"`
}

// UtilizationTelemetry drives synthetic GPU/memory utilization.
type UtilizationTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Pattern;Fixed
	// Mode selects pattern-generated or fixed utilization values.
	Mode string `json:"mode,omitempty"`

	// +optional
	// Pattern defines the utilization values generated over time.
	Pattern *UtilizationPattern `json:"pattern,omitempty"`
}

// UtilizationPattern shapes the generated utilization curve.
type UtilizationPattern struct {
	// +optional
	// +kubebuilder:validation:Enum=Steady;Bursty;Wave
	// Type selects the shape of the generated utilization curve.
	Type string `json:"type,omitempty"`

	// +optional
	// GPUPercent sets the minimum and maximum GPU utilization percentages.
	GPUPercent *PercentRange `json:"gpuPercent,omitempty"`

	// +optional
	// MemoryPercent sets the minimum and maximum memory utilization percentages.
	MemoryPercent *PercentRange `json:"memoryPercent,omitempty"`
}

// PercentRange is a min/max percentage bound. Both bounds are required, so an
// override replaces an inherited range whole.
type PercentRange struct {
	// Minimum is the lower utilization percentage bound.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Minimum int32 `json:"minimum"`

	// Maximum is the upper utilization percentage bound.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Maximum int32 `json:"maximum"`
}

// PowerTelemetry drives synthetic power draw.
type PowerTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Fixed;Pattern
	// Mode selects a constant or generated power draw.
	Mode string `json:"mode,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// DrawMilliWatts is the simulated GPU power draw in milliwatts.
	DrawMilliWatts *int64 `json:"drawMilliWatts,omitempty"`
}

// TemperatureTelemetry drives synthetic GPU/memory temperature.
type TemperatureTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Fixed;Pattern
	// Mode selects constant or generated temperature values.
	Mode string `json:"mode,omitempty"`

	// +optional
	// GPUCelsius is the simulated GPU temperature in degrees Celsius.
	GPUCelsius *int32 `json:"gpuCelsius,omitempty"`

	// +optional
	// MemoryCelsius is the simulated memory temperature in degrees Celsius.
	MemoryCelsius *int32 `json:"memoryCelsius,omitempty"`
}

// ClocksTelemetry reports current clock rates.
type ClocksTelemetry struct {
	// +optional
	// +kubebuilder:validation:Minimum=0
	// GraphicsMHz is the simulated graphics clock rate in MHz.
	GraphicsMHz *int32 `json:"graphicsMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// SMMHz is the simulated streaming-multiprocessor clock rate in MHz.
	SMMHz *int32 `json:"smMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// MemoryMHz is the simulated memory clock rate in MHz.
	MemoryMHz *int32 `json:"memoryMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// VideoMHz is the simulated video clock rate in MHz.
	VideoMHz *int32 `json:"videoMHz,omitempty"`
}

// mergePatch applies patch to document as RFC 7386 defines it: objects merge
// key by key and any other value replaces the target's. Every RuntimeState
// field is omitempty, so a patch never holds the null the RFC uses to delete a
// key, and adopting a group the document lacks equals merging it into an empty
// one.
func mergePatch(document, patch map[string]any) {
	for name, value := range patch {
		group, isGroup := value.(map[string]any)
		target, hasTarget := document[name].(map[string]any)

		if isGroup && hasTarget {
			mergePatch(target, group)
			continue
		}

		document[name] = value
	}
}

// leafJSONPaths returns the dotted path of every value in document that is not
// itself an object.
func leafJSONPaths(document map[string]any) []string {
	var paths []string

	for name, value := range document {
		group, isGroup := value.(map[string]any)
		if !isGroup {
			paths = append(paths, name)
			continue
		}

		for _, path := range leafJSONPaths(group) {
			paths = append(paths, name+"."+path)
		}
	}

	return paths
}
