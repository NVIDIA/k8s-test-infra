// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

// RuntimeState is the effective runtime settings of a simulated GPU.
// Sparse: omitted fields inherit; explicit zero is a set value.
type RuntimeState struct {
	// +optional
	// DeviceState is the simulated GPU health state.
	DeviceState DeviceState `json:"deviceState,omitempty"`

	// +optional
	// Modes contains persistent NVML/CUDA mode settings.
	Modes *RuntimeModes `json:"modes,omitempty"`

	// +optional
	// Telemetry contains synthetic NVML telemetry values.
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
	// Persistence controls persistence mode.
	Persistence string `json:"persistence,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Default;Exclusive;Prohibited
	// Compute controls compute mode.
	Compute string `json:"compute,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// MIG controls MIG mode.
	MIG string `json:"mig,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// ECC controls ECC mode.
	ECC string `json:"ecc,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// Accounting controls accounting mode.
	Accounting string `json:"accounting,omitempty"`
}

// RuntimeTelemetry is the synthetic NVML telemetry.
type RuntimeTelemetry struct {
	// NVML P-state, e.g. "P0".
	// +optional
	// +kubebuilder:validation:Pattern=`^P[0-9]+$`
	// PerformanceState is the NVML performance state, such as P0.
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
	// Mode selects fixed or patterned utilization.
	Mode string `json:"mode,omitempty"`

	// +optional
	// Pattern describes the utilization curve when Mode is Pattern.
	Pattern *UtilizationPattern `json:"pattern,omitempty"`
}

// UtilizationPattern shapes the generated utilization curve.
type UtilizationPattern struct {
	// +optional
	// +kubebuilder:validation:Enum=Steady;Bursty;Wave
	// Type selects the utilization curve shape.
	Type string `json:"type,omitempty"`

	// +optional
	// GPUPercent is the GPU utilization range.
	GPUPercent *PercentRange `json:"gpuPercent,omitempty"`

	// +optional
	// MemoryPercent is the memory utilization range.
	MemoryPercent *PercentRange `json:"memoryPercent,omitempty"`
}

// PercentRange is a min/max percentage bound.
type PercentRange struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// Minimum is the lower percentage bound.
	Minimum int32 `json:"minimum"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// Maximum is the upper percentage bound.
	Maximum int32 `json:"maximum"`
}

// PowerTelemetry drives synthetic power draw.
type PowerTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Fixed;Pattern
	// Mode selects fixed or patterned power draw.
	Mode string `json:"mode,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// DrawMilliWatts is the synthetic power draw in milliwatts.
	DrawMilliWatts int64 `json:"drawMilliWatts,omitempty"`
}

// TemperatureTelemetry drives synthetic GPU/memory temperature.
type TemperatureTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Fixed;Pattern
	// Mode selects fixed or patterned temperature.
	Mode string `json:"mode,omitempty"`

	// +optional
	// GPUCelsius is the GPU temperature in Celsius.
	GPUCelsius int32 `json:"gpuCelsius,omitempty"`

	// +optional
	// MemoryCelsius is the memory temperature in Celsius.
	MemoryCelsius int32 `json:"memoryCelsius,omitempty"`
}

// ClocksTelemetry reports current clock rates.
type ClocksTelemetry struct {
	// +optional
	// +kubebuilder:validation:Minimum=0
	// GraphicsMHz is the graphics clock in MHz.
	GraphicsMHz int32 `json:"graphicsMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// SMMHz is the SM clock in MHz.
	SMMHz int32 `json:"smMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// MemoryMHz is the memory clock in MHz.
	MemoryMHz int32 `json:"memoryMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// VideoMHz is the video clock in MHz.
	VideoMHz int32 `json:"videoMHz,omitempty"`
}
