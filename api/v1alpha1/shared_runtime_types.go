// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import "slices"

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

// WithOverride returns a copy of s in which every field set in override
// replaces the inherited value. A string is set when non-empty and a pointer
// when non-nil, so an explicit zero overrides. Neither input is modified or
// aliased, and either may be nil.
func (s *RuntimeState) WithOverride(override *RuntimeState) *RuntimeState {
	merged := s.DeepCopy()
	if merged == nil {
		merged = &RuntimeState{}
	}
	merged.overlay(override.DeepCopy())
	return merged
}

// FieldPaths returns the sorted JSON paths of the fields s sets, such as
// "telemetry.temperature.gpuCelsius". A percent range is one field.
func (s *RuntimeState) FieldPaths() []string {
	paths := s.appendFieldPaths(nil)
	slices.Sort(paths)
	return paths
}

// overlay applies the fields set in o. It adopts o's pointers, so o must be a
// private copy.
func (s *RuntimeState) overlay(o *RuntimeState) {
	if o == nil {
		return
	}
	overlayString(&s.DeviceState, o.DeviceState)
	s.Modes = overlayGroup(s.Modes, o.Modes, (*RuntimeModes).overlay)
	s.Telemetry = overlayGroup(s.Telemetry, o.Telemetry, (*RuntimeTelemetry).overlay)
}

func (s *RuntimeState) appendFieldPaths(paths []string) []string {
	if s == nil {
		return paths
	}
	paths = appendIfSet(paths, "deviceState", s.DeviceState != "")
	paths = s.Modes.appendFieldPaths(paths, "modes.")
	return s.Telemetry.appendFieldPaths(paths, "telemetry.")
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

func (m *RuntimeModes) overlay(o *RuntimeModes) {
	overlayString(&m.Persistence, o.Persistence)
	overlayString(&m.Compute, o.Compute)
	overlayString(&m.MIG, o.MIG)
	overlayString(&m.ECC, o.ECC)
	overlayString(&m.Accounting, o.Accounting)
}

func (m *RuntimeModes) appendFieldPaths(paths []string, prefix string) []string {
	if m == nil {
		return paths
	}
	paths = appendIfSet(paths, prefix+"persistence", m.Persistence != "")
	paths = appendIfSet(paths, prefix+"compute", m.Compute != "")
	paths = appendIfSet(paths, prefix+"mig", m.MIG != "")
	paths = appendIfSet(paths, prefix+"ecc", m.ECC != "")
	return appendIfSet(paths, prefix+"accounting", m.Accounting != "")
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

func (t *RuntimeTelemetry) overlay(o *RuntimeTelemetry) {
	overlayString(&t.PerformanceState, o.PerformanceState)
	t.Utilization = overlayGroup(t.Utilization, o.Utilization, (*UtilizationTelemetry).overlay)
	t.Power = overlayGroup(t.Power, o.Power, (*PowerTelemetry).overlay)
	t.Temperature = overlayGroup(t.Temperature, o.Temperature, (*TemperatureTelemetry).overlay)
	t.Clocks = overlayGroup(t.Clocks, o.Clocks, (*ClocksTelemetry).overlay)
}

func (t *RuntimeTelemetry) appendFieldPaths(paths []string, prefix string) []string {
	if t == nil {
		return paths
	}
	paths = appendIfSet(paths, prefix+"performanceState", t.PerformanceState != "")
	paths = t.Utilization.appendFieldPaths(paths, prefix+"utilization.")
	paths = t.Power.appendFieldPaths(paths, prefix+"power.")
	paths = t.Temperature.appendFieldPaths(paths, prefix+"temperature.")
	return t.Clocks.appendFieldPaths(paths, prefix+"clocks.")
}

// UtilizationTelemetry drives synthetic GPU/memory utilization.
type UtilizationTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Pattern;Fixed
	Mode string `json:"mode,omitempty"`

	// +optional
	Pattern *UtilizationPattern `json:"pattern,omitempty"`
}

func (u *UtilizationTelemetry) overlay(o *UtilizationTelemetry) {
	overlayString(&u.Mode, o.Mode)
	u.Pattern = overlayGroup(u.Pattern, o.Pattern, (*UtilizationPattern).overlay)
}

func (u *UtilizationTelemetry) appendFieldPaths(paths []string, prefix string) []string {
	if u == nil {
		return paths
	}
	paths = appendIfSet(paths, prefix+"mode", u.Mode != "")
	return u.Pattern.appendFieldPaths(paths, prefix+"pattern.")
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

func (p *UtilizationPattern) overlay(o *UtilizationPattern) {
	overlayString(&p.Type, o.Type)
	overlayPointer(&p.GPUPercent, o.GPUPercent)
	overlayPointer(&p.MemoryPercent, o.MemoryPercent)
}

func (p *UtilizationPattern) appendFieldPaths(paths []string, prefix string) []string {
	if p == nil {
		return paths
	}
	paths = appendIfSet(paths, prefix+"type", p.Type != "")
	paths = appendIfSet(paths, prefix+"gpuPercent", p.GPUPercent != nil)
	return appendIfSet(paths, prefix+"memoryPercent", p.MemoryPercent != nil)
}

// PercentRange is a min/max percentage bound. Both bounds are required, so an
// override replaces an inherited range whole.
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
	DrawMilliWatts *int64 `json:"drawMilliWatts,omitempty"`
}

func (p *PowerTelemetry) overlay(o *PowerTelemetry) {
	overlayString(&p.Mode, o.Mode)
	overlayPointer(&p.DrawMilliWatts, o.DrawMilliWatts)
}

func (p *PowerTelemetry) appendFieldPaths(paths []string, prefix string) []string {
	if p == nil {
		return paths
	}
	paths = appendIfSet(paths, prefix+"mode", p.Mode != "")
	return appendIfSet(paths, prefix+"drawMilliWatts", p.DrawMilliWatts != nil)
}

// TemperatureTelemetry drives synthetic GPU/memory temperature.
type TemperatureTelemetry struct {
	// +optional
	// +kubebuilder:validation:Enum=Fixed;Pattern
	Mode string `json:"mode,omitempty"`

	// +optional
	GPUCelsius *int32 `json:"gpuCelsius,omitempty"`

	// +optional
	MemoryCelsius *int32 `json:"memoryCelsius,omitempty"`
}

func (t *TemperatureTelemetry) overlay(o *TemperatureTelemetry) {
	overlayString(&t.Mode, o.Mode)
	overlayPointer(&t.GPUCelsius, o.GPUCelsius)
	overlayPointer(&t.MemoryCelsius, o.MemoryCelsius)
}

func (t *TemperatureTelemetry) appendFieldPaths(paths []string, prefix string) []string {
	if t == nil {
		return paths
	}
	paths = appendIfSet(paths, prefix+"mode", t.Mode != "")
	paths = appendIfSet(paths, prefix+"gpuCelsius", t.GPUCelsius != nil)
	return appendIfSet(paths, prefix+"memoryCelsius", t.MemoryCelsius != nil)
}

// ClocksTelemetry reports current clock rates.
type ClocksTelemetry struct {
	// +optional
	// +kubebuilder:validation:Minimum=0
	GraphicsMHz *int32 `json:"graphicsMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	SMMHz *int32 `json:"smMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	MemoryMHz *int32 `json:"memoryMHz,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	VideoMHz *int32 `json:"videoMHz,omitempty"`
}

func (c *ClocksTelemetry) overlay(o *ClocksTelemetry) {
	overlayPointer(&c.GraphicsMHz, o.GraphicsMHz)
	overlayPointer(&c.SMMHz, o.SMMHz)
	overlayPointer(&c.MemoryMHz, o.MemoryMHz)
	overlayPointer(&c.VideoMHz, o.VideoMHz)
}

func (c *ClocksTelemetry) appendFieldPaths(paths []string, prefix string) []string {
	if c == nil {
		return paths
	}
	paths = appendIfSet(paths, prefix+"graphicsMHz", c.GraphicsMHz != nil)
	paths = appendIfSet(paths, prefix+"smMHz", c.SMMHz != nil)
	paths = appendIfSet(paths, prefix+"memoryMHz", c.MemoryMHz != nil)
	return appendIfSet(paths, prefix+"videoMHz", c.VideoMHz != nil)
}

// overlayString keeps *field unless value is set; the empty string is never a
// valid runtime value, so it means "inherit".
func overlayString[T ~string](field *T, value T) {
	if value != "" {
		*field = value
	}
}

func overlayPointer[T any](field **T, value *T) {
	if value != nil {
		*field = value
	}
}

// overlayGroup applies a nested override group. Without a base group it
// adopts the override's, which is safe because overlay receives a private copy.
func overlayGroup[T any](base, override *T, overlay func(*T, *T)) *T {
	if override == nil {
		return base
	}
	if base == nil {
		return override
	}
	overlay(base, override)
	return base
}

func appendIfSet(paths []string, path string, set bool) []string {
	if set {
		return append(paths, path)
	}
	return paths
}
