// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package materialize

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestRackNameVectors(t *testing.T) {
	require.Equal(t, "inventory-a-compute-2-152f2cf61b78", RackName("inventory-a", types.UID("inventory-uid-a"), "compute", 2))

	name := RackName(strings.Repeat("very-long-inventory-", 8), types.UID("inventory-uid-a"), strings.Repeat("group", 20), 12345)
	require.LessOrEqual(t, len(name), 63)
	require.Empty(t, validation.IsDNS1123Subdomain(name))
	require.Equal(t, "very-long-inventory-very-long-inventory-very-long-60142d58c4a7", name)
}

func TestRackNameHashAlwaysDisambiguatesCoordinates(t *testing.T) {
	base := RackName("same-readable-prefix", types.UID("uid-a"), "compute", 1)
	require.NotEqual(t, base, RackName("same-readable-prefix", types.UID("uid-b"), "compute", 1))
	require.NotEqual(t, base, RackName("same-readable-prefix", types.UID("uid-a"), "storage", 1))
	require.NotEqual(t, base, RackName("same-readable-prefix", types.UID("uid-a"), "compute", 2))

	parts := strings.Split(base, "-")
	require.Len(t, parts[len(parts)-1], rackNameHashLength)
}
