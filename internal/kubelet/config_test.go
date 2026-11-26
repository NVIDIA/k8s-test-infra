// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kubelet

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigUpdatesOwnedSettingsAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()
	const original = `apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
featureGates:
  FutureFeature: true
cpuManagerPolicy: none
reservedSystemCPUs: "7"
systemReserved:
  cpu: 100m
  memory: 128Mi
  ephemeral-storage: 1Gi
reservedMemory:
  - numaNode: 0
    limits:
      memory: 1Mi
    futureField: keep
`
	document, err := DecodeConfig([]byte(original))
	require.NoError(t, err)
	document.Settings.CPUManagerPolicy = "static"
	document.Settings.ReservedSystemCPUs = ""
	document.Settings.SystemReserved.Memory = "256Mi"
	data, err := document.Bytes()
	require.NoError(t, err)
	require.YAMLEq(t, `apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
featureGates:
  FutureFeature: true
cpuManagerPolicy: static
systemReserved:
  cpu: 100m
  memory: 256Mi
  ephemeral-storage: 1Gi
reservedMemory:
  - numaNode: 0
    limits:
      memory: 1Mi
    futureField: keep
`, string(data))
}
