// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

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
	Mode string `json:"mode,omitempty"`

	// +optional
	Pattern *UtilizationPattern `json:"pattern,omitempty"`
}

// UtilizationPattern shapes the generated utilization curve.
type UtilizationPattern struct {
	// +optional
	// +kubebuilder:validation:Enum=Steady;Bursty;Wave
	Type string `json:"type,omitempty"`

	// +optional
	GPUPercent *PercentRange `json:"gpuPercent,omitempty"`

	// +optional
	MemoryPercent *PercentRange `json:"memoryPercent,omitempty"`
}

// PercentRange is a min/max percentage bound.
type PercentRange struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Minimum int32 `json:"minimum"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Maximum int32 `json:"maximum"`
}

// PowerTelemetry drives synthetic power draw.
type PowerTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Fixed;Pattern
	Mode string `json:"mode,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	DrawMilliWatts int64 `json:"drawMilliWatts,omitempty"`
}

// TemperatureTelemetry drives synthetic GPU/memory temperature.
type TemperatureTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Fixed;Pattern
	Mode string `json:"mode,omitempty"`

	// +optional
	GPUCelsius int32 `json:"gpuCelsius,omitempty"`

	// +optional
	MemoryCelsius int32 `json:"memoryCelsius,omitempty"`
}

// ClocksTelemetry reports current clock rates.
type ClocksTelemetry struct {
	// +optional
	// +kubebuilder:validation:Minimum=0
	GraphicsMHz int32 `json:"graphicsMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	SMMHz int32 `json:"smMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	MemoryMHz int32 `json:"memoryMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	VideoMHz int32 `json:"videoMHz,omitempty"`
}
