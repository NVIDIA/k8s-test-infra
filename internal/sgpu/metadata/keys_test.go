// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package metadata

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabelsEqualIgnoringProjection(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"pool": "training"}
	projected := map[string]string{"pool": "training", AssignedLabel: "true", CliqueLabel: "fabric.0"}
	zoned := map[string]string{"pool": "training", "zone": "a"}

	require.True(t, LabelsEqualIgnoringProjection(labels, projected))
	require.True(t, LabelsEqualIgnoringProjection(projected, labels))
	require.False(t, LabelsEqualIgnoringProjection(labels, map[string]string{"pool": "inference"}))
	require.False(t, LabelsEqualIgnoringProjection(labels, zoned), "an added label is a change")
	require.False(t, LabelsEqualIgnoringProjection(zoned, labels), "a removed label is a change")
}
