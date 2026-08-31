// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package materialize

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"k8s.io/apimachinery/pkg/types"
)

const (
	maxRackNameLength  = 63
	rackNameHashLength = 12
)

// RackName returns a readable DNS label with a coordinate hash that survives
// truncation.
func RackName(inventoryName string, inventoryUID types.UID, rackGroup string, rackIndex int32) string {
	hashInput := appendLengthPrefixed(nil, string(inventoryUID))
	hashInput = appendLengthPrefixed(hashInput, rackGroup)
	var encodedRackIndex [4]byte
	binary.BigEndian.PutUint32(encodedRackIndex[:], uint32(rackIndex))
	hashInput = append(hashInput, encodedRackIndex[:]...)
	sum := sha256.Sum256(hashInput)
	suffix := hex.EncodeToString(sum[:])[:rackNameHashLength]
	prefix := dnsLabel(fmt.Sprintf("%s-%s-%d", inventoryName, rackGroup, rackIndex))
	maxPrefix := maxRackNameLength - 1 - len(suffix)
	if len(prefix) > maxPrefix {
		prefix = strings.Trim(prefix[:maxPrefix], "-")
	}
	if prefix == "" {
		return suffix
	}
	return prefix + "-" + suffix
}

func dnsLabel(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	builder.Grow(len(value))
	lastHyphen := false
	for _, r := range value {
		valid := unicode.IsLower(r) || unicode.IsDigit(r)
		if valid {
			builder.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			builder.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
