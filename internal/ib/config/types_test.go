// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/config"
	"github.com/stretchr/testify/require"
)

// A minimal profile block has to render something a consumer accepts, and RDMA
// consumers resolve an HCA's interface before considering it, so an unset
// prefix would leave every HCA associated with nothing.
func TestDefaults_SuppliesANetdevPrefix(t *testing.T) {
	t.Parallel()

	require.Equal(t, "mockib", config.Infiniband{Enabled: true}.Defaults().NetdevPrefix)
}

// An operator who names the interfaces keeps that name: it has to match the
// selector their own configuration uses.
func TestDefaults_KeepsAConfiguredNetdevPrefix(t *testing.T) {
	t.Parallel()

	ib := config.Infiniband{Enabled: true, NetdevPrefix: "ibs"}
	require.Equal(t, "ibs", ib.Defaults().NetdevPrefix)
}
