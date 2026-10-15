// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/guestdisk"
	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// BundleFile is the archive a KubeVirt guest attaches as a raw disk. A VM's
// hostDisk volume names it under the guest placeholder, so the two must agree.
const BundleFile = "bundle.tar"

// guestUnit runs nvml-mock-guest, which mirrors the host's runtime overrides
// from the disk's live area. The --once pass makes `systemctl enable --now`
// return only after the guest has the host's current overrides; its failure is
// ignored ("-") so the polling loop still starts and retries.
const guestUnit = `[Unit]
Description=Mirror the node's nvml-mock runtime overrides into this guest

[Service]
ExecStartPre=-/usr/local/bin/nvml-mock-guest --once
ExecStart=/usr/local/bin/nvml-mock-guest
Restart=always
RestartSec=5
StandardOutput=journal+console
StandardError=journal+console

[Install]
WantedBy=multi-user.target
`

// bundleSource is where a bundle's files come from: the node agent's staged
// overlay, and the guest program this plugin ships in its own image.
type bundleSource struct {
	OverlayRoot  string
	GuestProgram string
}

// hostOverridesPath is the node's runtime override file, which nvml-mock-ctl
// and the NVML setters write.
func hostOverridesPath(overlayRoot string) string {
	return filepath.Join(overlayRoot, "driver", "config", "overrides.yaml")
}

// guestLibDir sits beside nvidia-smi's bin directory because the image build
// sets its RPATH to $ORIGIN/../lib64.
const guestLibDir = "usr/local/lib64"

// bundleEntry is one file of the bundle: copied from source in the overlay, or
// data when source is empty.
type bundleEntry struct {
	name   string
	mode   int64
	source string
	data   string
}

// migTableFile is the board's MIG profile table. The node agent stages it
// beside config.yaml only for a MIG-capable board, and the engine finds it
// there by this name.
const migTableFile = "config.mig.yaml"

// bundleEntries lays the bundle out at the guest's real paths, so extracting it
// at / is the whole install: the engine finds /etc/nvml-mock/config.yaml and
// the files beside it without MOCK_NVML_CONFIG. Its overrides.yaml is left to
// nvml-mock-guest, which copies the host's.
func bundleEntries(src bundleSource, minors []int) ([]bundleEntry, error) {
	driver := filepath.Join(src.OverlayRoot, "driver")
	lines := make([]string, len(minors))
	for i, minor := range minors {
		lines[i] = strconv.Itoa(minor) + "\n"
	}
	profile, err := os.ReadFile(filepath.Join(driver, "config", "config.yaml"))
	if err != nil {
		return nil, err
	}
	config, err := passthroughConfig(profile)
	if err != nil {
		return nil, err
	}

	files := []bundleEntry{
		{name: "etc/nvml-mock/config.yaml", mode: 0o644, data: string(config)},
		{name: "etc/nvml-mock/allocation", mode: 0o644, data: strings.Join(lines, "")},
		{name: guestLibDir + "/libnvidia-ml.so.1", mode: 0o755, source: filepath.Join(driver, "usr", "lib64", "libnvidia-ml.so.1")},
		{name: "usr/local/bin/nvidia-smi", mode: 0o755, source: filepath.Join(driver, "usr", "bin", "nvidia-smi")},
		{name: "usr/local/bin/nvml-mock-guest", mode: 0o755, source: src.GuestProgram},
		{name: "etc/systemd/system/nvml-mock-guest.service", mode: 0o644, data: guestUnit},
		{name: "etc/ld.so.conf.d/nvml-mock.conf", mode: 0o644, data: "/" + guestLibDir + "\n"},
	}
	// Only absence means the board has no table. Any other failure fails the
	// bundle rather than handing the guest a board silently without partitions.
	table := filepath.Join(driver, "config", migTableFile)
	if _, err := os.Stat(table); err == nil {
		files = append(files, bundleEntry{name: "etc/nvml-mock/" + migTableFile, mode: 0o644, source: table})
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat MIG profile table: %w", err)
	}
	entries := make([]bundleEntry, 0, len(files)+len(minors))
	entries = append(entries, files...)
	// An empty file is enough: the engine only stats /dev/nvidia<minor> to
	// decide which GPUs are visible, and the guest has no driver to back a
	// character device.
	for _, minor := range minors {
		entries = append(entries, bundleEntry{name: "dev/nvidia" + strconv.Itoa(minor), mode: 0o666})
	}

	return entries, nil
}

// bundleOwner is who a bundle and its directory belong to.
type bundleOwner struct{ UID, GID int }

// kubevirtQEMU is KubeVirt's unprivileged qemu user. virt-handler chowns a
// hostDisk volume to it before the VM starts, unless it already belongs to it,
// and the launcher sees the bundle read-only, where that chown fails.
var kubevirtQEMU = bundleOwner{UID: 107, GID: 107}

// writeBundle writes the bundle into b.Dir in a single rename, so QEMU never
// opens a half-written disk: the archive tar extracts, zero padding to the next
// area boundary, and the live area carrying overrides. The file is
// world-readable because QEMU opens it as the unprivileged qemu user.
func writeBundle(src bundleSource, b inject.GuestBundle, owner bundleOwner, overrides []byte) error {
	area, err := guestdisk.Encode(overrides)
	if err != nil {
		return err
	}
	entries, err := bundleEntries(src, b.Minors)
	if err != nil {
		return err
	}
	path := filepath.Join(b.Dir, BundleFile)
	if err := fsutil.WriteWith(path, 0o644, func(w io.Writer) error {
		counted := &countingWriter{w: w}
		if err := writeArchive(counted, entries); err != nil {
			return err
		}
		if _, err := w.Write(make([]byte, guestdisk.Pad(counted.n))); err != nil {
			return err
		}
		_, err := w.Write(area)

		return err
	}); err != nil {
		return err
	}
	for _, p := range []string{path, b.Dir} {
		if err := os.Chown(p, owner.UID, owner.GID); err != nil {
			return fmt.Errorf("chown bundle: %w", err)
		}
	}

	return nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)

	return n, err
}

func writeArchive(w io.Writer, entries []bundleEntry) error {
	archive := tar.NewWriter(w)
	now := time.Now()
	for _, entry := range entries {
		if err := addEntry(archive, entry, now); err != nil {
			return fmt.Errorf("bundle %s: %w", entry.name, err)
		}
	}

	return archive.Close()
}

func addEntry(archive *tar.Writer, entry bundleEntry, now time.Time) error {
	header := &tar.Header{Name: entry.name, Mode: entry.mode, Typeflag: tar.TypeReg, ModTime: now}
	if entry.source == "" {
		header.Size = int64(len(entry.data))
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		_, err := io.WriteString(archive, entry.data)

		return err
	}

	// Open follows the staged libnvidia-ml.so.1 symlink to the library itself;
	// inside the guest there is nothing for the link to point at.
	f, err := os.Open(entry.source)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read-only; a close error cannot corrupt the bundle

	info, err := f.Stat()
	if err != nil {
		return err
	}
	header.Size = info.Size()
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	_, err = io.Copy(archive, f)

	return err
}

// removeBundle deletes the bundle of a removed container. Most containers never
// had one, so a missing directory is not an error.
func removeBundle(root, containerID string) error {
	if root == "" || !inject.ValidContainerID(containerID) {
		return nil
	}

	return os.RemoveAll(filepath.Join(root, containerID))
}

// sweepBundles removes the bundles of containers the runtime no longer
// reports. RemoveContainer misses every container removed while the plugin was
// not registered, and on a long-lived node those would accumulate.
//
// Only directories named like a containerd container ID are bundles: a root
// misconfigured onto a shared directory such as /run must keep everything the
// plugin did not create.
func sweepBundles(root string, live map[string]bool) error {
	if root == "" {
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", root, err)
	}

	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() || !isBundleDirName(entry.Name()) || live[entry.Name()] {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", dir, err))
		}
	}

	return errors.Join(errs...)
}

// isBundleDirName reports whether name has the shape of a containerd container
// ID: 64 lowercase hex characters.
func isBundleDirName(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, c := range name {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}
