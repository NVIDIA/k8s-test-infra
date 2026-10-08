// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cri

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	securejoin "github.com/cyphar/filepath-securejoin"
)

// hostPATH is where host binaries are looked up, in the order a login shell
// on the node would search.
var hostPATH = []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"}

// Chroot runs commands as the node runs them, chrooted into the host's root
// filesystem: only the host's own systemctl agrees with the host's systemd.
type Chroot struct {
	// Root is the host's root filesystem as mounted in this container.
	Root string

	// run starts and waits for cmd; nil runs it. Tests replace it, because a
	// real chroot needs root.
	run func(cmd *exec.Cmd) error
}

// Run runs name, looked up on the host's PATH, with args inside the chroot and
// returns its stdout, also on failure. The environment holds the host PATH and
// env only: nothing of this process's own, such as the mock libraries it
// preloads, reaches the host command.
func (c Chroot) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	path, err := c.LookPath(name)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = "/"
	cmd.Env = append([]string{"PATH=" + strings.Join(hostPATH, ":")}, env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: c.Root}

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	run := c.run
	if run == nil {
		run = (*exec.Cmd).Run
	}

	if err := run(cmd); err != nil {
		return stdout.Bytes(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

// LookPath finds name on the host's PATH and returns its path inside the
// chroot.
func (c Chroot) LookPath(name string) (string, error) {
	for _, dir := range hostPATH {
		path := filepath.Join(dir, name)

		full, err := c.resolve(path)
		if err != nil {
			continue
		}

		info, err := os.Stat(full)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return path, nil
		}
	}

	return "", fmt.Errorf("%s not found on the host's PATH (%s)", name, strings.Join(hostPATH, ":"))
}

// resolve returns where path, a path on the host, is from this side of the
// chroot. Host symlinks are followed inside the root: an absolute link would
// otherwise point into this container.
func (c Chroot) resolve(path string) (string, error) {
	if c.Root == "" {
		return "", errors.New("host root filesystem not set")
	}

	full, err := securejoin.SecureJoin(c.Root, path)
	if err != nil {
		return "", fmt.Errorf("resolve %s under %s: %w", path, c.Root, err)
	}

	return full, nil
}
