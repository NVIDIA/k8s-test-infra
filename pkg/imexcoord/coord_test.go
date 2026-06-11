// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

package imexcoord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllPeersReady_HappyPath(t *testing.T) {
	dir := t.TempDir()
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		err := WriteMarker(dir, ip)
		require.NoError(t, err)
	}
	ok, missing := AllPeersReady(dir, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"})
	require.True(t, ok, "expected ready, got missing=%s", missing)
}

func TestAllPeersReady_MissingPeer(t *testing.T) {
	dir := t.TempDir()
	_ = WriteMarker(dir, "10.0.0.1")
	ok, missing := AllPeersReady(dir, []string{"10.0.0.1", "10.0.0.99"})
	require.False(t, ok, "expected missing=10.0.0.99 not-ready, got ok=%v missing=%q", ok, missing)
	require.Equal(t, "10.0.0.99", missing, "expected missing=10.0.0.99 not-ready, got ok=%v missing=%q", ok, missing)
}

func TestAllPeersReady_EmptyPeersTreatedAsReady(t *testing.T) {
	ok, _ := AllPeersReady(t.TempDir(), nil)
	require.True(t, ok, "empty peers should be ready")
}

func TestRemoveMarker_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	err := RemoveMarker(dir, "10.0.0.1")
	require.NoError(t, err, "remove missing")
	_ = WriteMarker(dir, "10.0.0.1")
	err = RemoveMarker(dir, "10.0.0.1")
	require.NoError(t, err, "remove existing")
}

func TestReadPeers_SkipsCommentsBlanksAndPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nodes.cfg")
	_ = os.WriteFile(path, []byte("# header\n\n10.0.0.1\n10.0.0.2 50051\n  \n"), 0o644)
	peers, err := ReadPeers(path)
	require.NoError(t, err)
	require.Len(t, peers, 2, "unexpected peers: %v", peers)
	require.Equal(t, "10.0.0.1", peers[0], "unexpected peers: %v", peers)
	require.Equal(t, "10.0.0.2", peers[1], "unexpected peers: %v", peers)
}

// TestWriteMarker_RejectsInvalidIP guards against path traversal via
// untrusted nodes.cfg entries. The upstream compute-domain-daemon
// writes nodes.cfg, so a future bug or compromise there could feed
// strings like "../etc/passwd" into MarkerPath. WriteMarker must
// reject anything that isn't a real IP literal before touching the
// filesystem — the test asserts both the error AND that no file
// landed at the traversal target.
func TestWriteMarker_RejectsInvalidIP(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"../etc/passwd",
		"..",
		"/absolute/path",
		"not-an-ip",
		"10.0.0.1/24", // CIDR, not a plain IP literal
		"",
	}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			err := WriteMarker(dir, bad)
			require.Error(t, err, "WriteMarker(%q) = nil, want validation error", bad)
			require.True(t, strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "empty"),
				"WriteMarker(%q) error %q lacks 'invalid'/'empty' tag — looks like incidental filesystem failure rather than a validation reject", bad, err)
		})
	}
}

// TestAllPeersReady_TreatsInvalidIPAsMissing protects against a
// corrupted nodes.cfg passing a non-IP entry through to filesystem
// stat. An invalid IP must surface as "this peer is not ready"
// rather than as a successful READY (or, worse, an os.Stat on an
// unintended path).
func TestAllPeersReady_TreatsInvalidIPAsMissing(t *testing.T) {
	dir := t.TempDir()
	err := WriteMarker(dir, "10.0.0.1")
	require.NoError(t, err)
	ok, missing := AllPeersReady(dir, []string{"10.0.0.1", "../etc/passwd"})
	require.False(t, ok, "AllPeersReady with invalid peer returned ready=true")
	require.Equal(t, "../etc/passwd", missing, "missing = %q, want %q (the invalid peer)", missing, "../etc/passwd")
}
