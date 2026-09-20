// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

func testArchive(t *testing.T, omit string) []byte {
	t.Helper()

	files := map[string]string{
		"usr/bin/nvidia-imex":        "real-imex",
		"usr/bin/nvidia-imex-ctl":    "imex-ctl",
		"etc/nvidia-imex/config.cfg": "IMEX_CMD_ENABLED=1\n",
	}
	delete(files, omit)

	var compressed bytes.Buffer
	xzw, err := xz.NewWriter(&compressed)
	require.NoError(t, err)
	tw := tar.NewWriter(xzw)
	for name, contents := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "imex-test-archive/" + name,
			Mode:     0o755,
			Size:     int64(len(contents)),
			Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(contents))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, xzw.Close())
	return compressed.Bytes()
}

func testLock(baseURL string, archive []byte) Lock {
	sum := sha256.Sum256(archive)
	artifact := Artifact{Path: "imex/test.tar.xz", SHA256: hex.EncodeToString(sum[:])}
	return Lock{
		Version: "test-version",
		BaseURL: baseURL,
		Artifacts: map[string]Artifact{
			"amd64": artifact,
			"arm64": artifact,
		},
	}
}

func testUserspaceSimulator(t *testing.T, lock Lock, client *http.Client) (*Simulator, *host.Host) {
	t.Helper()

	h := host.New(t.TempDir())
	shim := filepath.Join(t.TempDir(), "nvidia-imex-shim")
	require.NoError(t, os.WriteFile(shim, []byte("mokka-shim"), 0o755))
	return New(h, Options{
		Architecture: "amd64",
		ShimPath:     shim,
		Lock:         lock,
		HTTPClient:   client,
	}), h
}

func userspaceState() *agent.State {
	return &agent.State{IMEX: agent.IMEXState{NodeSoftwareEnabled: true}}
}

func TestUserspaceStageDownloadsCachesAndPublishes(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	sim, h := testUserspaceSimulator(t, testLock(server.URL, archive), server.Client())
	require.NoError(t, os.MkdirAll(h.Root, 0o755))
	rootBefore, err := os.Stat(h.Root)
	require.NoError(t, err)

	require.NoError(t, sim.Stage(t.Context(), userspaceState()))
	require.True(t, sim.Ready())
	require.Equal(t, int32(1), requests.Load())
	require.FileExists(t, h.RootPath("cache/imex/test-version/amd64/archive.tar.xz"))
	requireFileContent(t, h.RootPath("driver/usr/bin/nvidia-imex"), "mokka-shim")
	requireFileContent(t, h.RootPath("driver/usr/bin/nvidia-imex.real"), "real-imex")
	requireFileContent(t, h.RootPath("driver/usr/bin/nvidia-imex-ctl"), "imex-ctl")
	requireFileContent(t, h.RootPath("driver/etc/nvidia-imex/config.cfg"), "IMEX_CMD_ENABLED=1\n")

	rootAfter, err := os.Stat(h.Root)
	require.NoError(t, err)
	require.True(t, os.SameFile(rootBefore, rootAfter), "staging must not replace the stable driver-tree root")

	server.Close()
	require.NoError(t, sim.Stage(t.Context(), userspaceState()), "the verified node cache must make restaging network-independent")
	require.Equal(t, int32(1), requests.Load())

	require.NoError(t, sim.Discard(t.Context()))
	require.NoFileExists(t, h.RootPath("driver/usr/bin/nvidia-imex"))
	require.NoFileExists(t, h.RootPath("driver/usr/bin/nvidia-imex.real"))
	require.NoFileExists(t, h.RootPath("driver/usr/bin/nvidia-imex-ctl"))
	require.FileExists(t, h.RootPath("cache/imex/test-version/amd64/archive.tar.xz"), "teardown keeps the verified restart cache")
}

func TestUserspaceStageRejectsChecksumMismatchWithoutPublishing(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	lock := testLock(server.URL, archive)
	artifact := lock.Artifacts["amd64"]
	artifact.SHA256 = strings.Repeat("0", 64)
	lock.Artifacts["amd64"] = artifact
	sim, h := testUserspaceSimulator(t, lock, server.Client())
	require.NoError(t, os.MkdirAll(filepath.Dir(h.RootPath("driver/usr/bin/nvidia-imex")), 0o755))
	require.NoError(t, os.WriteFile(h.RootPath("driver/usr/bin/nvidia-imex"), []byte("stale"), 0o755))

	err := sim.Stage(t.Context(), userspaceState())
	require.ErrorContains(t, err, "SHA-256 mismatch")
	require.False(t, sim.Ready())
	for _, rel := range publishedUserspacePaths {
		require.NoFileExists(t, h.RootPath(rel))
	}
}

func TestUserspaceStageRejectsIncompleteArchiveWithoutPublishing(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "usr/bin/nvidia-imex-ctl")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	sim, h := testUserspaceSimulator(t, testLock(server.URL, archive), server.Client())
	err := sim.Stage(t.Context(), userspaceState())
	require.ErrorContains(t, err, "archive is missing usr/bin/nvidia-imex-ctl")
	for _, rel := range publishedUserspacePaths {
		require.NoFileExists(t, h.RootPath(rel))
	}
}

func TestUserspaceDisabledDoesNotDownload(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	sim, h := testUserspaceSimulator(t, testLock(server.URL, archive), server.Client())
	require.NoError(t, sim.Stage(t.Context(), &agent.State{}))
	require.Zero(t, requests.Load())
	_, err := os.Stat(h.Root)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestUserspaceDisableRemovesPreviouslyPublishedFiles(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	sim, h := testUserspaceSimulator(t, testLock(server.URL, archive), server.Client())
	require.NoError(t, sim.Stage(t.Context(), userspaceState()))
	require.FileExists(t, h.RootPath("driver/usr/bin/nvidia-imex"))

	require.NoError(t, sim.Stage(t.Context(), &agent.State{}))
	for _, rel := range publishedUserspacePaths {
		require.NoFileExists(t, h.RootPath(rel))
	}
}

func TestPinnedOfficialArchives(t *testing.T) {
	if os.Getenv("MOKKA_IMEX_DOWNLOAD_TEST") != "1" {
		t.Skip("set MOKKA_IMEX_DOWNLOAD_TEST=1 to verify the pinned NVIDIA archives")
	}

	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			h := host.New(t.TempDir())
			shim := filepath.Join(t.TempDir(), "nvidia-imex-shim")
			require.NoError(t, os.WriteFile(shim, []byte("shim"), 0o755))
			sim := New(h, Options{
				Architecture: arch,
				ShimPath:     shim,
				HTTPClient:   &http.Client{Timeout: 2 * time.Minute},
			})

			require.NoError(t, sim.Stage(t.Context(), userspaceState()))
			require.FileExists(t, h.RootPath("driver/usr/bin/nvidia-imex.real"))
			require.FileExists(t, h.RootPath("driver/usr/bin/nvidia-imex-ctl"))
			require.FileExists(t, h.RootPath("driver/etc/nvidia-imex/config.cfg"))
		})
	}
}

func requireFileContent(t *testing.T, filename, expected string) {
	t.Helper()
	content, err := os.ReadFile(filename)
	require.NoError(t, err)
	require.Equal(t, expected, string(content))
}
