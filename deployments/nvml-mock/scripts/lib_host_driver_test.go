// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package scripts holds Go unit tests for the POSIX-sh helper libraries under
// deployments/nvml-mock/scripts. The libraries themselves are shell (sourced
// at runtime inside the nvml-mock / mock-driver containers), but the
// host-mutation ownership logic in lib-host-driver.sh is safety-critical -- a
// stale or tampered manifest must NEVER cause a delete of a path nvml-mock
// does not own -- so its refuse-on-mismatch branches are exercised here by
// shelling out to `sh`. This keeps the test in the repo's standard `go test`
// path (CI runs `go test $(go list ./...)`) without adding a bespoke shell
// test runner. The test skips when `sh` or `sha256sum` is unavailable (e.g. a
// developer macOS box without coreutils); CI runners are Linux and have both.
package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// verifyManifest sources lib-host-driver.sh and runs hdrl_verify_manifest
// against the given hostroot + manifest, returning combined output and whether
// it succeeded (exit 0).
func verifyManifest(t *testing.T, hostroot, manifest string) (string, bool) {
	t.Helper()
	lib := libPath(t)
	// hdrl_verify_manifest is the two-pass verifier setup.sh/cleanup.sh call;
	// it drives hdrl_walk_manifest + hdrl_verify_entry underneath.
	script := ". \"$LIB\"; hdrl_verify_manifest \"$HR\" \"$MAN\""
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "LIB="+lib, "HR="+hostroot, "MAN="+manifest)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func libPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	lib := filepath.Join(wd, "lib-host-driver.sh")
	if _, err := os.Stat(lib); err != nil {
		t.Fatalf("lib-host-driver.sh not found next to the test: %v", err)
	}
	return lib
}

// requireShellTools skips the test when the shell or sha256sum the library
// depends on is not on PATH.
func requireShellTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"sh", "sha256sum"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("skipping shell test: %q not on PATH", bin)
		}
	}
}

func sha256Hex(t *testing.T, b []byte) string {
	t.Helper()
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeFile creates hostroot/rel with content and returns its sha256.
func writeFile(t *testing.T, hostroot, rel string, content []byte) string {
	t.Helper()
	full := filepath.Join(hostroot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return sha256Hex(t, content)
}

// writeSymlink creates hostroot/rel -> target.
func writeSymlink(t *testing.T, hostroot, rel, target string) {
	t.Helper()
	full := filepath.Join(hostroot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, full); err != nil {
		t.Fatalf("symlink %s: %v", full, err)
	}
}

func writeManifest(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	man := filepath.Join(dir, "manifest.txt")
	if err := os.WriteFile(man, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return man
}

// TestVerifyManifestAcceptsMatchingEntries proves the happy path: a manifest
// whose recorded file hash, symlink target, and ldconfig entry all match the
// host state verifies successfully (exit 0).
func TestVerifyManifestAcceptsMatchingEntries(t *testing.T) {
	requireShellTools(t)
	hostroot := t.TempDir()
	mandir := t.TempDir()

	smiHash := writeFile(t, hostroot, "usr/bin/nvidia-smi", []byte("fake-smi-elf"))
	writeSymlink(t, hostroot, "config", "/var/lib/nvml-mock/driver/config")
	writeFile(t, hostroot, "etc/ld.so.conf.d/00-nvml-mock.conf", []byte("/usr/lib64\n"))

	man := writeManifest(t,
		mandir,
		"file /usr/bin/nvidia-smi "+smiHash,
		"symlink /config /var/lib/nvml-mock/driver/config",
		"ldconfig /etc/ld.so.conf.d/00-nvml-mock.conf",
	)

	out, ok := verifyManifest(t, hostroot, man)
	if !ok {
		t.Fatalf("expected verify to succeed for matching manifest, got failure:\n%s", out)
	}
}

// TestVerifyManifestToleratesMissingEntry proves a recorded path that no longer
// exists is treated as safe (a crash-before-write partial install), NOT a
// failure -- the append-order invariant makes the manifest a superset of what
// exists on disk.
func TestVerifyManifestToleratesMissingEntry(t *testing.T) {
	requireShellTools(t)
	hostroot := t.TempDir()
	mandir := t.TempDir()

	man := writeManifest(t, mandir, "file /usr/bin/nvidia-smi deadbeefdeadbeef")

	out, ok := verifyManifest(t, hostroot, man)
	if !ok {
		t.Fatalf("expected verify to tolerate a missing recorded path, got failure:\n%s", out)
	}
}

// TestVerifyManifestRefusesOnMismatch is the load-bearing safety test: every
// way the host state can disagree with the manifest must make verify FAIL
// (non-zero) so cleanup/converge refuses to delete anything.
func TestVerifyManifestRefusesOnMismatch(t *testing.T) {
	requireShellTools(t)

	cases := []struct {
		name    string
		setup   func(t *testing.T, hostroot, mandir string) string // returns manifest path
		wantErr string
	}{
		{
			name: "modified file (hash mismatch)",
			setup: func(t *testing.T, hostroot, mandir string) string {
				writeFile(t, hostroot, "usr/bin/nvidia-smi", []byte("tampered-content"))
				// Record a hash that does NOT match the on-disk content.
				return writeManifest(t, mandir, "file /usr/bin/nvidia-smi 0000000000000000000000000000000000000000000000000000000000000000")
			},
			wantErr: "has been modified",
		},
		{
			name: "regular file recorded but path is a resolvable symlink",
			setup: func(t *testing.T, hostroot, mandir string) string {
				// The symlink must RESOLVE (point at an existing target) so the
				// lib's `[ ! -e ]` missing-check does not treat it as absent: a
				// foreign symlink over our recorded regular file must be refused,
				// not silently accepted. (A broken symlink is separately safe:
				// `-e` sees it as missing, so converge leaves it untouched.)
				target := writeFile(t, hostroot, "real-target", []byte("some existing file"))
				_ = target
				writeSymlink(t, hostroot, "usr/bin/nvidia-smi", filepath.Join(hostroot, "real-target"))
				return writeManifest(t, mandir, "file /usr/bin/nvidia-smi abc123")
			},
			wantErr: "not a regular file",
		},
		{
			name: "foreign symlink (wrong target)",
			setup: func(t *testing.T, hostroot, mandir string) string {
				writeSymlink(t, hostroot, "config", "/etc/attacker-owned")
				return writeManifest(t, mandir, "symlink /config /var/lib/nvml-mock/driver/config")
			},
			wantErr: "points at",
		},
		{
			name: "symlink recorded but path is a regular file",
			setup: func(t *testing.T, hostroot, mandir string) string {
				writeFile(t, hostroot, "config", []byte("real config file"))
				return writeManifest(t, mandir, "symlink /config /var/lib/nvml-mock/driver/config")
			},
			wantErr: "not a symlink",
		},
		{
			name: "device recorded but path is a regular file (wrong type)",
			setup: func(t *testing.T, hostroot, mandir string) string {
				writeFile(t, hostroot, "dev/nvidia0", []byte("not a device"))
				return writeManifest(t, mandir, "device /dev/nvidia0 195:0")
			},
			wantErr: "not a char device",
		},
		{
			name: "unknown manifest entry type",
			setup: func(t *testing.T, hostroot, mandir string) string {
				return writeManifest(t, mandir, "wormhole /usr/bin/nvidia-smi whatever")
			},
			wantErr: "unknown manifest entry type",
		},
		{
			name: "malformed manifest line (missing path)",
			setup: func(t *testing.T, hostroot, mandir string) string {
				return writeManifest(t, mandir, "file")
			},
			wantErr: "malformed manifest line",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostroot := t.TempDir()
			mandir := t.TempDir()
			man := c.setup(t, hostroot, mandir)

			out, ok := verifyManifest(t, hostroot, man)
			if ok {
				t.Fatalf("expected verify to REFUSE (non-zero) for %q, but it succeeded:\n%s", c.name, out)
			}
			if !strings.Contains(out, c.wantErr) {
				t.Fatalf("expected refusal message %q, got:\n%s", c.wantErr, out)
			}
		})
	}
}
