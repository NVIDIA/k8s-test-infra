// Copyright (c) 2026, NVIDIA CORPORATION. All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package imexredist

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func parseLock(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()

	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		require.True(t, ok, "lock line is not an assignment: %q", line)
		_, duplicate := values[key]
		require.False(t, duplicate, "lock contains duplicate key %q", key)
		values[key] = value
	}
	require.NoError(t, scanner.Err())
	return values
}

func TestIMEXLock(t *testing.T) {
	t.Parallel()
	values := parseLock(t, filepath.Join(repoRoot(t), "deployments/nvml-mock/build/imex.lock"))
	require.Equal(t, map[string]struct{}{
		"IMEX_VERSION":      {},
		"IMEX_AMD64_PATH":   {},
		"IMEX_AMD64_SHA256": {},
		"IMEX_ARM64_PATH":   {},
		"IMEX_ARM64_SHA256": {},
	}, keySet(values))

	version := values["IMEX_VERSION"]
	require.Regexp(t, `^\d+\.\d+\.\d+$`, version)
	for _, arch := range []string{"AMD64", "ARM64"} {
		path := values["IMEX_"+arch+"_PATH"]
		require.Contains(t, path, version)
		require.True(t, strings.HasSuffix(path, "-archive.tar.xz"))
		require.Regexp(t, `^[0-9a-f]{64}$`, values["IMEX_"+arch+"_SHA256"])
	}
	require.Contains(t, values["IMEX_AMD64_PATH"], "/linux-x86_64/")
	require.Contains(t, values["IMEX_ARM64_PATH"], "/linux-sbsa/")
}

func keySet(values map[string]string) map[string]struct{} {
	keys := make(map[string]struct{}, len(values))
	for key := range values {
		keys[key] = struct{}{}
	}
	return keys
}

func TestDownloadIMEXRejectsUnsupportedArchitecture(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	cmd := exec.Command("sh", filepath.Join(root, "deployments/nvml-mock/build/download-imex.sh"),
		filepath.Join(root, "deployments/nvml-mock/build/imex.lock"), "s390x", t.TempDir())
	out, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(out), "unsupported TARGETARCH: s390x")
}

func TestDownloadIMEXRejectsInvalidChecksumBeforeDownload(t *testing.T) {
	t.Parallel()
	lock := filepath.Join(t.TempDir(), "imex.lock")
	require.NoError(t, os.WriteFile(lock, []byte(strings.Join([]string{
		"IMEX_VERSION=1.2.3",
		"IMEX_AMD64_PATH=imex/linux-x86_64/imex-linux-x86_64-1.2.3-archive.tar.xz",
		"IMEX_AMD64_SHA256=not-a-checksum",
		"IMEX_ARM64_PATH=imex/linux-sbsa/imex-linux-sbsa-1.2.3-archive.tar.xz",
		"IMEX_ARM64_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, "\n")+"\n"), 0o600))

	cmd := exec.Command("sh", filepath.Join(repoRoot(t), "deployments/nvml-mock/build/download-imex.sh"),
		lock, "amd64", t.TempDir())
	out, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(out), "invalid SHA256 for TARGETARCH amd64")
}

func TestDownloadIMEXSelectsArtifactForArchitecture(t *testing.T) {
	t.Parallel()
	payload := []byte("selector fixture; deliberately not a tar archive")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		if _, err := w.Write(payload); err != nil {
			t.Errorf("serve fixture: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	lock := writeLock(t, digest, digest)
	tests := []struct {
		arch     string
		wantPath string
	}{
		{arch: "amd64", wantPath: "/imex/linux-x86_64/imex-linux-x86_64-1.2.3-archive.tar.xz"},
		{arch: "arm64", wantPath: "/imex/linux-sbsa/imex-linux-sbsa-1.2.3-archive.tar.xz"},
	}
	for _, tt := range tests {
		t.Run(tt.arch, func(t *testing.T) {
			cmd := downloadCommand(t, lock, tt.arch, filepath.Join(t.TempDir(), "out"), server.URL)
			out, err := cmd.CombinedOutput()
			require.Error(t, err, "fixture is intentionally not a tar archive")
			require.Contains(t, string(out), "tar")
			require.Equal(t, tt.wantPath, <-requests)
		})
	}
}

func TestDownloadIMEXStopsOnChecksumMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("downloaded archive")); err != nil {
			t.Errorf("serve fixture: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	lock := writeLock(t, strings.Repeat("0", 64), strings.Repeat("1", 64))
	output := filepath.Join(t.TempDir(), "output-must-not-exist")
	out, err := downloadCommand(t, lock, "amd64", output, server.URL).CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(out), "FAILED")
	require.NoDirExists(t, output, "extraction must not start after a checksum mismatch")
}

func writeLock(t *testing.T, amd64SHA, arm64SHA string) string {
	t.Helper()
	lock := filepath.Join(t.TempDir(), "imex.lock")
	contents := fmt.Sprintf(`IMEX_VERSION=1.2.3
IMEX_AMD64_PATH=imex/linux-x86_64/imex-linux-x86_64-1.2.3-archive.tar.xz
IMEX_AMD64_SHA256=%s
IMEX_ARM64_PATH=imex/linux-sbsa/imex-linux-sbsa-1.2.3-archive.tar.xz
IMEX_ARM64_SHA256=%s
`, amd64SHA, arm64SHA)
	require.NoError(t, os.WriteFile(lock, []byte(contents), 0o600))
	return lock
}

func downloadCommand(t *testing.T, lock, arch, output, baseURL string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(repoRoot(t), "deployments/nvml-mock/build/download-imex.sh"),
		lock, arch, output)
	cmd.Env = append(os.Environ(), "IMEX_REDIST_BASE_URL="+baseURL)
	return cmd
}
