// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProvisionerEffectiveTreatsUnsetAsCPU(t *testing.T) {
	t.Parallel()

	// Admission fills the field, so only objects built in process, such as a
	// rack group in a test fixture, reach Effective unset.
	require.Equal(t, ProvisionerCPU, Provisioner("").Effective())
	require.Equal(t, ProvisionerCPU, ProvisionerCPU.Effective())
	require.Equal(t, ProvisionerKWOK, ProvisionerKWOK.Effective())
}
