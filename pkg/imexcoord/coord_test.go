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
