// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/imex"
)

func TestRunLaysOutTheBuildContextPerArchitecture(t *testing.T) {
	t.Parallel()

	archives := map[string][]byte{
		"/redist/imex/linux-x86_64/imex-linux-x86_64-test-archive.tar.xz": []byte("amd64 archive"),
		"/redist/imex/linux-sbsa/imex-linux-sbsa-test-archive.tar.xz":     []byte("arm64 archive"),
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := archives[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	lock := imex.Lock{Version: "test-version", BaseURL: srv.URL + "/redist", Artifacts: map[string]imex.Artifact{}}
	for path, body := range archives {
		arch := "amd64"
		if strings.Contains(path, "sbsa") {
			arch = "arm64"
		}
		sum := sha256.Sum256(body)
		lock.Artifacts[arch] = imex.Artifact{Path: strings.TrimPrefix(path, "/redist/"), SHA256: hex.EncodeToString(sum[:])}
	}
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	err := run(t.Context(), srv.Client(), lock, dir, []string{"amd64", " arm64"}, &stdout, &stderr)

	require.NoError(t, err)
	buildContext := filepath.Join(dir, "test-version")
	require.Equal(t, buildContext+"\n", stdout.String())
	for file, body := range map[string]string{
		"amd64/imex-linux-x86_64-test-archive.tar.xz": "amd64 archive",
		"arm64/imex-linux-sbsa-test-archive.tar.xz":   "arm64 archive",
	} {
		content, err := os.ReadFile(filepath.Join(buildContext, file))
		require.NoError(t, err)
		require.Equal(t, body, string(content))
	}
}

func TestRunRejectsAnEmptyArchitecture(t *testing.T) {
	t.Parallel()

	err := run(t.Context(), http.DefaultClient, imex.DefaultLock(), t.TempDir(), []string{""}, &bytes.Buffer{}, &bytes.Buffer{})

	require.ErrorContains(t, err, "names no architecture")
}
