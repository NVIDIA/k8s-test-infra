// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"fmt"
	"maps"

	"github.com/Masterminds/semver/v3"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

// NVIDIA's GPU plugin uses unqualified names for driver attributes and
// Kubernetes-qualified names for topology. Selectors depend on both the
// spelling and the type (in particular, a version is not a string).
func sliceDevices(slot state.NodePayload) ([]resourcev1.Device, error) {
	model := slot.Hardware.GPUModel
	memory, err := resource.ParseQuantity(model.Memory)
	if err != nil {
		return nil, fmt.Errorf("GPU memory %q: %w", model.Memory, err)
	}
	brand := model.Brand
	if brand == "" {
		brand = "Nvidia"
	}
	common := map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
		"type": {StringValue: ptr.To("gpu")}, "brand": {StringValue: &brand},
	}
	for name, value := range map[resourcev1.QualifiedName]string{
		"productName": model.ProductName, "architecture": cases.Title(language.Und).String(model.Architecture),
		"addressingMode": model.AddressingMode,
	} {
		if value != "" {
			common[name] = resourcev1.DeviceAttribute{StringValue: &value}
		}
	}
	versions := map[resourcev1.QualifiedName]string{"cudaComputeCapability": model.ComputeCapability}
	if sw := slot.Hardware.Software; sw != nil {
		versions["driverVersion"], versions["cudaDriverVersion"] = sw.DriverVersion, sw.CUDAVersion
	}
	for name, value := range versions {
		if value == "" {
			continue
		}
		v, err := semver.NewVersion(value)
		if err != nil {
			return nil, fmt.Errorf("GPU %s %q: %w", name, value, err)
		}
		common[name] = resourcev1.DeviceAttribute{VersionValue: ptr.To(v.String())}
	}
	devices := make([]resourcev1.Device, 0, len(slot.GPUs))
	for _, gpu := range slot.GPUs {
		attrs := maps.Clone(common)
		attrs["uuid"] = resourcev1.DeviceAttribute{StringValue: &gpu.UUID}
		attrs["resource.kubernetes.io/pciBusID"] = resourcev1.DeviceAttribute{StringValue: &gpu.PCIAddress}
		attrs["resource.kubernetes.io/pcieRoot"] = resourcev1.DeviceAttribute{StringValue: &gpu.RootComplex}
		attrs["resource.kubernetes.io/numaNode"] = resourcev1.DeviceAttribute{IntValue: ptr.To(int64(gpu.NUMANode))}
		devices = append(devices, resourcev1.Device{Name: fmt.Sprintf("gpu-%d", gpu.MinorNumber), Attributes: attrs,
			Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{"memory": {Value: memory}},
		})
	}
	return devices, nil
}
