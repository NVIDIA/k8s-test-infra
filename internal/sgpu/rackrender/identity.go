// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package rackrender

import (
	"encoding/binary"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	"github.com/NVIDIA/k8s-test-infra/internal/identity"
)

// FabricUUID returns the rack-local fabric identity for one logical rack.
func FabricUUID(inventoryUID types.UID, rackGroup string, rackIndex int32) string {
	return identityName("fabric", inventoryUID, rackGroup, rackIndex).UUID()
}

// GPUUUID returns the NVML-compatible UUID for one logical GPU coordinate.
func GPUUUID(inventoryUID types.UID, rackGroup string, rackIndex, nodeIndex, gpuIndex int32) string {
	return "GPU-" + identityName("gpu", inventoryUID, rackGroup, rackIndex, nodeIndex, gpuIndex).UUID()
}

// GPUSerial returns the numeric serial for one logical GPU coordinate.
func GPUSerial(inventoryUID types.UID, rackGroup string, rackIndex, nodeIndex, gpuIndex int32) string {
	id := identityName("serial", inventoryUID, rackGroup, rackIndex, nodeIndex, gpuIndex).Sum()
	return fmt.Sprintf("%020d", binary.BigEndian.Uint64(id[:8]))
}

func identityName(kind string, inventoryUID types.UID, rackGroup string, coordinates ...int32) identity.Name {
	return identity.New(kind).String(string(inventoryUID)).String(rackGroup).Int(coordinates...)
}
