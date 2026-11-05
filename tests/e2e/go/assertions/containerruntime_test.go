// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// kindCRIInfo reads `crictl info` on a Kind node (containerd 2.2) with the
// node daemon's handler loaded, trimmed to what the checks read.
func kindCRIInfo(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "crictl-info-kind.json"))
	require.NoError(t, err)

	return data
}

func TestParseCRIInfo(t *testing.T) {
	got, err := ParseCRIInfo(kindCRIInfo(t))

	require.NoError(t, err)
	require.Equal(t, ContainerRuntime{
		EnableCDI:      true,
		CDISpecDirs:    []string{"/etc/cdi", "/var/run/cdi"},
		DefaultRuntime: "nvidia",
		Handlers: map[string]string{
			"runc":   "",
			"nvidia": "/usr/bin/nvidia-container-runtime",
		},
	}, got)
	require.NoError(t, CheckNvidiaHandler(got))
}

func TestParseCRIInfoWithoutConfig(t *testing.T) {
	_, err := ParseCRIInfo([]byte(`{"status": {}}`))
	require.Error(t, err)

	_, err = ParseCRIInfo([]byte(`not json`))
	require.Error(t, err)
}

func TestCheckNvidiaHandler(t *testing.T) {
	base := func() ContainerRuntime {
		rt, err := ParseCRIInfo(kindCRIInfo(t))
		require.NoError(t, err)

		return rt
	}

	missing := base()
	delete(missing.Handlers, "nvidia")
	require.ErrorContains(t, CheckNvidiaHandler(missing), "no nvidia handler")

	elsewhere := base()
	elsewhere.Handlers["nvidia"] = "/usr/local/nvidia/toolkit/nvidia-container-runtime"
	require.ErrorContains(t, CheckNvidiaHandler(elsewhere), "/usr/local/nvidia/toolkit")

	notDefault := base()
	notDefault.DefaultRuntime = "runc"
	require.ErrorContains(t, CheckNvidiaHandler(notDefault), "default", "the handler must be the default")

	noCDI := base()
	noCDI.EnableCDI = false
	require.ErrorContains(t, CheckNvidiaHandler(noCDI), "CDI")
}
