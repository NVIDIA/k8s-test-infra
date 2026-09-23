// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedLock(t *testing.T) {
	t.Parallel()

	lock, err := parseLock(embeddedLock)
	require.NoError(t, err)
	require.Equal(t, "595.91.07", lock.Version)

	amd64, err := lock.artifact("amd64")
	require.NoError(t, err)
	require.Contains(t, amd64.Path, "linux-x86_64")
	require.Len(t, amd64.SHA256, 64)

	arm64, err := lock.artifact("arm64")
	require.NoError(t, err)
	require.Contains(t, arm64.Path, "linux-sbsa")
	require.Len(t, arm64.SHA256, 64)
}

func TestLockRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	valid := `{
  "version":"1",
  "baseURL":"https://example.test/redist",
  "artifacts":{
    "amd64":{"path":"imex/amd64.tar.xz","sha256":"` + strings.Repeat("a", 64) + `"},
    "arm64":{"path":"imex/arm64.tar.xz","sha256":"` + strings.Repeat("b", 64) + `"}
  }
}`

	tests := []struct {
		name string
		data string
	}{
		{name: "missing version", data: strings.Replace(valid, `"version":"1"`, `"version":""`, 1)},
		{name: "traversing version", data: strings.Replace(valid, `"version":"1"`, `"version":"../1"`, 1)},
		{name: "relative base URL", data: strings.Replace(valid, "https://example.test/redist", "/redist", 1)},
		{name: "traversing path", data: strings.Replace(valid, "imex/amd64.tar.xz", "../amd64.tar.xz", 1)},
		{name: "uppercase digest", data: strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("A", 64), 1)},
		{name: "missing arm64", data: strings.Replace(valid, `,
    "arm64":{"path":"imex/arm64.tar.xz","sha256":"`+strings.Repeat("b", 64)+`"}`, "", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseLock([]byte(tt.data))
			require.Error(t, err)
		})
	}
}

func TestLockRejectsUnsupportedArchitecture(t *testing.T) {
	t.Parallel()

	_, err := mustDefaultLock().artifact("ppc64le")
	require.EqualError(t, err, `unsupported architecture "ppc64le"`)
}
