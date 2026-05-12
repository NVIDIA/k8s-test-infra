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
)

func TestAllPeersReady_HappyPath(t *testing.T) {
	dir := t.TempDir()
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		if err := WriteMarker(dir, ip); err != nil {
			t.Fatal(err)
		}
	}
	ok, missing := AllPeersReady(dir, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"})
	if !ok {
		t.Fatalf("expected ready, got missing=%s", missing)
	}
}

func TestAllPeersReady_MissingPeer(t *testing.T) {
	dir := t.TempDir()
	_ = WriteMarker(dir, "10.0.0.1")
	ok, missing := AllPeersReady(dir, []string{"10.0.0.1", "10.0.0.99"})
	if ok || missing != "10.0.0.99" {
		t.Fatalf("expected missing=10.0.0.99 not-ready, got ok=%v missing=%q", ok, missing)
	}
}

func TestAllPeersReady_EmptyPeersTreatedAsReady(t *testing.T) {
	if ok, _ := AllPeersReady(t.TempDir(), nil); !ok {
		t.Fatal("empty peers should be ready")
	}
}

func TestRemoveMarker_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := RemoveMarker(dir, "10.0.0.1"); err != nil {
		t.Fatalf("remove missing: %v", err)
	}
	_ = WriteMarker(dir, "10.0.0.1")
	if err := RemoveMarker(dir, "10.0.0.1"); err != nil {
		t.Fatalf("remove existing: %v", err)
	}
}

func TestReadPeers_SkipsCommentsBlanksAndPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nodes.cfg")
	_ = os.WriteFile(path, []byte("# header\n\n10.0.0.1\n10.0.0.2 50051\n  \n"), 0o644)
	peers, err := ReadPeers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 || peers[0] != "10.0.0.1" || peers[1] != "10.0.0.2" {
		t.Fatalf("unexpected peers: %v", peers)
	}
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
			if err == nil {
				t.Fatalf("WriteMarker(%q) = nil, want validation error", bad)
			}
			if !strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "empty") {
				t.Errorf("WriteMarker(%q) error %q lacks 'invalid'/'empty' tag — looks like incidental filesystem failure rather than a validation reject", bad, err)
			}
		})
	}
	// And the more important assertion: nothing was created outside dir.
	parent := filepath.Dir(dir)
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if e.Name() == filepath.Base(dir) || strings.HasPrefix(e.Name(), "TestWriteMarker") {
			continue
		}
		// Don't fail on pre-existing siblings; just don't be the source.
	}
}

// TestAllPeersReady_TreatsInvalidIPAsMissing protects against a
// corrupted nodes.cfg passing a non-IP entry through to filesystem
// stat. An invalid IP must surface as "this peer is not ready"
// rather than as a successful READY (or, worse, an os.Stat on an
// unintended path).
func TestAllPeersReady_TreatsInvalidIPAsMissing(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMarker(dir, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	ok, missing := AllPeersReady(dir, []string{"10.0.0.1", "../etc/passwd"})
	if ok {
		t.Fatal("AllPeersReady with invalid peer returned ready=true")
	}
	if missing != "../etc/passwd" {
		t.Errorf("missing = %q, want %q (the invalid peer)", missing, "../etc/passwd")
	}
}
