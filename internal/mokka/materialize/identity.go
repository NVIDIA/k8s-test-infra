// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package materialize

import (
	"crypto/sha1" // UUIDv5 is defined in terms of SHA-1.
	"encoding/binary"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
)

// identityNamespaceV1 is the fixed Mokka namespace for the first identity
// scheme. A new namespace preserves existing identities if the scheme evolves.
const identityNamespaceV1 = "\xd9\x54\xd6\x17\xe8\x1d\x58\xd0\x8f\x22\xf4\x5a\xfd\xa6\x22\x60"

// FabricUUID returns the rack-local fabric identity for one logical rack.
func FabricUUID(inventoryUID types.UID, rackGroup string, rackIndex int32) string {
	return uuidV5(identityName("fabric", inventoryUID, rackGroup, rackIndex))
}

// GPUUUID returns the NVML-compatible UUID for one logical GPU coordinate.
func GPUUUID(inventoryUID types.UID, rackGroup string, rackIndex, nodeIndex, gpuIndex int32) string {
	return "GPU-" + uuidV5(identityName(
		"gpu", inventoryUID, rackGroup, rackIndex, nodeIndex, gpuIndex,
	))
}

// GPUSerial returns the numeric serial for one logical GPU coordinate.
func GPUSerial(inventoryUID types.UID, rackGroup string, rackIndex, nodeIndex, gpuIndex int32) string {
	id := uuidV5Bytes(identityName(
		"serial", inventoryUID, rackGroup, rackIndex, nodeIndex, gpuIndex,
	))
	return fmt.Sprintf("%020d", binary.BigEndian.Uint64(id[:8]))
}

func identityName(kind string, inventoryUID types.UID, rackGroup string, coordinates ...int32) []byte {
	// Length-prefixing string components prevents distinct logical coordinates
	// from becoming equal through separator characters in names or UIDs.
	name := []byte("mokka-identity-v1")
	name = appendLengthPrefixed(name, kind)
	name = appendLengthPrefixed(name, string(inventoryUID))
	name = appendLengthPrefixed(name, rackGroup)
	for _, coordinate := range coordinates {
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], uint32(coordinate))
		name = append(name, encoded[:]...)
	}
	return name
}

func appendLengthPrefixed(dst []byte, value string) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

func uuidV5(name []byte) string {
	id := uuidV5Bytes(name)
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(id[0:4]),
		binary.BigEndian.Uint16(id[4:6]),
		binary.BigEndian.Uint16(id[6:8]),
		binary.BigEndian.Uint16(id[8:10]),
		id[10:16],
	)
}

func uuidV5Bytes(name []byte) [16]byte {
	hash := sha1.New()
	_, _ = hash.Write([]byte(identityNamespaceV1))
	_, _ = hash.Write(name)

	var id [16]byte
	copy(id[:], hash.Sum(nil))
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
