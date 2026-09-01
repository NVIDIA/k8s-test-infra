// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package host abstracts on-host filesystem roots so simulators can retarget
// to t.TempDir() in tests without mounting a real /host.
package host

import (
	"path/filepath"
)

// Host holds the roots for each host filesystem namespace the agent writes to.
// Production: hostPrefix = "/host". Tests: hostPrefix = t.TempDir().
type Host struct {
	// Prefix is where the host filesystem is mounted in the agent's namespace,
	// so a host path the deployment names can be reached through it.
	Prefix string // /host

	Root string // /host/var/lib/nvml-mock — simulator staging area
	Dev  string // /host/dev
	Proc string // /host/proc
	Sys  string // /host/sys
	Etc  string // /host/etc
	Run  string // /host/run
}

// New returns a Host whose paths are rooted under hostPrefix.
func New(hostPrefix string) *Host {
	return &Host{
		Prefix: filepath.Clean(hostPrefix),

		Root: filepath.Join(hostPrefix, "var/lib/nvml-mock"),
		Dev:  filepath.Join(hostPrefix, "dev"),
		Proc: filepath.Join(hostPrefix, "proc"),
		Sys:  filepath.Join(hostPrefix, "sys"),
		Etc:  filepath.Join(hostPrefix, "etc"),
		Run:  filepath.Join(hostPrefix, "run"),
	}
}

// RootPath joins parts under Root, the simulator staging area. Prefer it over
// filepath.Join(h.Root, ...) so a call site names the namespace it writes to.
func (h *Host) RootPath(parts ...string) string { return join(h.Root, parts) }

// DevPath joins parts under Dev.
func (h *Host) DevPath(parts ...string) string { return join(h.Dev, parts) }

// ProcPath joins parts under Proc.
func (h *Host) ProcPath(parts ...string) string { return join(h.Proc, parts) }

// SysPath joins parts under Sys.
func (h *Host) SysPath(parts ...string) string { return join(h.Sys, parts) }

// EtcPath joins parts under Etc.
func (h *Host) EtcPath(parts ...string) string { return join(h.Etc, parts) }

// RunPath joins parts under Run.
func (h *Host) RunPath(parts ...string) string { return join(h.Run, parts) }

// HostPath maps an absolute host path into the agent's namespace, for
// directories the deployment names rather than this package deriving them.
func (h *Host) HostPath(abs string) string { return filepath.Join(h.Prefix, abs) }

func join(root string, parts []string) string {
	return filepath.Join(append([]string{root}, parts...)...)
}
