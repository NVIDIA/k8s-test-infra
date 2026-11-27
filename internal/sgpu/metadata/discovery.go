// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package metadata

import (
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

// Hardware discovery labels published by the KWOK data plane.
const (
	GPUProductLabel  = "nvidia.com/gpu.product"
	GPUCountLabel    = "nvidia.com/gpu.count"
	GPUMemoryLabel   = "nvidia.com/gpu.memory"
	MIGStrategyLabel = "nvidia.com/mig.strategy"
	NvidiaPCILabel   = "feature.node.kubernetes.io/pci-10de.present"
)

// Includes optional keys so ownership is dropped if a later profile omits them.
var discoveryKeys = []string{
	GPUProductLabel, GPUCountLabel, GPUMemoryLabel, MIGStrategyLabel, NvidiaPCILabel,
	"nvidia.com/gpu.family", "nvidia.com/gpu.compute.major", "nvidia.com/gpu.compute.minor",
	"nvidia.com/gpu.machine", "nvidia.com/gpu.mode", "nvidia.com/gpu.replicas", "nvidia.com/gpu.sharing-strategy",
	"nvidia.com/mps.capable", "nvidia.com/mig.capable", "nvidia.com/vgpu.present",
	"nvidia.com/cuda.driver.major", "nvidia.com/cuda.driver.minor", "nvidia.com/cuda.driver.rev",
	"nvidia.com/cuda.driver-version.major", "nvidia.com/cuda.driver-version.minor", "nvidia.com/cuda.driver-version.revision", "nvidia.com/cuda.driver-version.full",
	"nvidia.com/cuda.runtime.major", "nvidia.com/cuda.runtime.minor", "nvidia.com/cuda.runtime-version.major", "nvidia.com/cuda.runtime-version.minor", "nvidia.com/cuda.runtime-version.full",
}

// DiscoveryLabelKeys returns the read-only set managed by the data plane.
func DiscoveryLabelKeys() []string { return discoveryKeys }

var invalidProductChars = regexp.MustCompile(`[^A-Za-z0-9-_. ]`)

// DiscoveryLabels renders the GFD and NFD values for a homogeneous whole-GPU node.
func DiscoveryLabels(count int, hardware state.Hardware) (map[string]string, error) {
	model := hardware.GPUModel
	memory, err := resource.ParseQuantity(model.Memory)
	if err != nil {
		return nil, err
	}
	const bytesPerMiB = 1 << 20
	result := map[string]string{
		GPUCountLabel:                     strconv.Itoa(count),
		GPUMemoryLabel:                    strconv.FormatInt(memory.Value()/bytesPerMiB, 10),
		MIGStrategyLabel:                  "none",
		"nvidia.com/gpu.mode":             "compute", // Mokka serves GPUs as PCI 3D controllers.
		"nvidia.com/gpu.replicas":         "1",
		"nvidia.com/gpu.sharing-strategy": "none",
		"nvidia.com/mps.capable":          "false",
		"nvidia.com/mig.capable":          strconv.FormatBool(model.MIGCapable),
		"nvidia.com/vgpu.present":         "false",
	}
	if name := model.ProductName; name != "" {
		result[GPUProductLabel] = strings.Join(strings.Fields(invalidProductChars.ReplaceAllString(name, "")), "-")
		// Rack profiles have no separate DMI product name. The CPU node agent
		// uses the device name for its machine-type file in that case.
		result["nvidia.com/gpu.machine"] = strings.ReplaceAll(name, " ", "-")
	}
	if major, minor, ok := strings.Cut(model.ComputeCapability, "."); ok && major != "0" {
		result["nvidia.com/gpu.compute.major"] = major
		result["nvidia.com/gpu.compute.minor"] = minor
		result["nvidia.com/gpu.family"] = gpuFamily(model.Architecture, major, minor)
	}
	addSoftwareLabels(result, hardware.Software)
	vendorID := strings.TrimPrefix(strings.ToLower(model.PCI.VendorID), "0x")
	if vendorID == "10de" || vendorID == "" && strings.EqualFold(model.Vendor, "NVIDIA") {
		result[NvidiaPCILabel] = "true"
	}
	return result, nil
}

var familyByComputeMajor = map[string]string{
	"1": "tesla", "2": "fermi", "3": "kepler", "5": "maxwell",
	"6": "pascal", "7": "volta", "8": "ampere", "9": "hopper",
	"10": "blackwell", "12": "blackwell", "13": "rubin",
}

// GFD uses the NVML architecture name when available. For an unidentified
// architecture it derives the family from the CUDA compute capability.
func gpuFamily(name, major, minor string) string {
	if arch, ok := gpuarch.Parse(name); ok && arch.Known() {
		if arch == gpuarch.Ada {
			return "ada-lovelace"
		}
		return arch.String()
	}
	m, _ := strconv.Atoi(minor) // Compiled rack state uses decimal integers.
	if major == "7" && m >= 5 {
		return "turing"
	}
	if major == "8" && m >= 9 {
		return "ada-lovelace"
	}
	if family, ok := familyByComputeMajor[major]; ok {
		return family
	}
	return "undefined"
}

// GFD publishes both the legacy and versioned keys. Keep leading zeros in
// driver components and the empty revision of a two-component driver version.
func addSoftwareLabels(labels map[string]string, software *api.SGPUSoftware) {
	if software == nil {
		return
	}
	parts := strings.Split(software.DriverVersion, ".")
	if len(parts) == 2 || len(parts) == 3 {
		revision := ""
		if len(parts) == 3 {
			revision = parts[2]
		}
		labels["nvidia.com/cuda.driver.major"] = parts[0]
		labels["nvidia.com/cuda.driver-version.major"] = parts[0]
		labels["nvidia.com/cuda.driver.minor"] = parts[1]
		labels["nvidia.com/cuda.driver-version.minor"] = parts[1]
		labels["nvidia.com/cuda.driver.rev"] = revision
		labels["nvidia.com/cuda.driver-version.revision"] = revision
		labels["nvidia.com/cuda.driver-version.full"] = software.DriverVersion
	}
	if major, minor, ok := strings.Cut(software.CUDAVersion, "."); ok {
		labels["nvidia.com/cuda.runtime.major"] = major
		labels["nvidia.com/cuda.runtime-version.major"] = major
		labels["nvidia.com/cuda.runtime.minor"] = minor
		labels["nvidia.com/cuda.runtime-version.minor"] = minor
		labels["nvidia.com/cuda.runtime-version.full"] = software.CUDAVersion
	}
}
