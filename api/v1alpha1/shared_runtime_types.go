// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

// RuntimeState is the effective runtime settings of a simulated GPU.
// Sparse: omitted fields inherit; explicit zero is a set value.
type RuntimeState struct {
	// +optional
	// DeviceState is the simulated health state of the GPU.
	DeviceState DeviceState `json:"deviceState,omitempty"`

	// +optional
	// Modes contains persistent NVML/CUDA mode settings.
	Modes *RuntimeModes `json:"modes,omitempty"`

	// +optional
	// Telemetry contains synthetic utilization, power, temperature, and clock values.
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
	// Persistence controls the simulated persistence mode.
	Persistence string `json:"persistence,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Default;Exclusive;Prohibited
	// Compute selects the simulated compute mode.
	Compute string `json:"compute,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// MIG controls whether Multi-Instance GPU mode is enabled.
	MIG string `json:"mig,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// ECC controls whether error-correcting code is enabled.
	ECC string `json:"ecc,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// Accounting controls whether GPU accounting mode is enabled.
	Accounting string `json:"accounting,omitempty"`
}

// RuntimeTelemetry is the synthetic NVML telemetry.
type RuntimeTelemetry struct {
	// NVML P-state, e.g. "P0".
	// +optional
	// +kubebuilder:validation:Pattern=`^P[0-9]+$`
	PerformanceState string `json:"performanceState,omitempty"`

	// +optional
	// Utilization defines synthetic GPU and memory utilization values.
	Utilization *UtilizationTelemetry `json:"utilization,omitempty"`

	// +optional
	// Power defines synthetic power draw values.
	Power *PowerTelemetry `json:"power,omitempty"`

	// +optional
	// Temperature defines synthetic GPU and memory temperature values.
	Temperature *TemperatureTelemetry `json:"temperature,omitempty"`

	// +optional
	// Clocks defines the current simulated clock rates.
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

// PercentRange is a min/max percentage bound.
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
	DrawMilliWatts int64 `json:"drawMilliWatts,omitempty"`
}

// TemperatureTelemetry drives synthetic GPU/memory temperature.
type TemperatureTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Fixed;Pattern
	// Mode selects constant or generated temperature values.
	Mode string `json:"mode,omitempty"`

	// +optional
	// GPUCelsius is the simulated GPU temperature in degrees Celsius.
	GPUCelsius int32 `json:"gpuCelsius,omitempty"`

	// +optional
	// MemoryCelsius is the simulated memory temperature in degrees Celsius.
	MemoryCelsius int32 `json:"memoryCelsius,omitempty"`
}

// ClocksTelemetry reports current clock rates.
type ClocksTelemetry struct {
	// +optional
	// +kubebuilder:validation:Minimum=0
	// GraphicsMHz is the simulated graphics clock rate in MHz.
	GraphicsMHz int32 `json:"graphicsMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// SMMHz is the simulated streaming-multiprocessor clock rate in MHz.
	SMMHz int32 `json:"smMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// MemoryMHz is the simulated memory clock rate in MHz.
	MemoryMHz int32 `json:"memoryMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// VideoMHz is the simulated video clock rate in MHz.
	VideoMHz int32 `json:"videoMHz,omitempty"`
}
