// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ulikunitz/xz"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const (
	maxArchiveBytes = 64 << 20
	maxMemberBytes  = 64 << 20
)

type userspaceFile struct {
	destination string
	mode        os.FileMode
}

var userspaceFiles = map[string]userspaceFile{
	"usr/bin/nvidia-imex":        {destination: "driver/usr/bin/nvidia-imex.real", mode: 0o755},
	"usr/bin/nvidia-imex-ctl":    {destination: "driver/usr/bin/nvidia-imex-ctl", mode: 0o755},
	"etc/nvidia-imex/config.cfg": {destination: "driver/etc/nvidia-imex/config.cfg", mode: 0o644},
}

var publishedUserspacePaths = []string{
	"driver/usr/bin/nvidia-imex",
	"driver/usr/bin/nvidia-imex.real",
	"driver/usr/bin/nvidia-imex-ctl",
	"driver/etc/nvidia-imex/config.cfg",
}

type userspaceInstaller struct {
	root       string
	shimPath   string
	arch       string
	lock       Lock
	httpClient *http.Client
}

func (i *userspaceInstaller) stage(ctx context.Context) error {
	if err := i.lock.validate(); err != nil {
		return i.fail(fmt.Errorf("validate lock: %w", err))
	}
	artifact, err := i.lock.artifact(i.arch)
	if err != nil {
		return i.fail(err)
	}

	archive, err := i.ensureArchive(ctx, artifact)
	if err != nil {
		return i.fail(err)
	}

	extracted, cleanup, err := extractUserspace(archive)
	if err != nil {
		return i.fail(err)
	}
	defer cleanup()

	// The shim is the activation point: publish the real executable, control
	// tool and configuration first so a concurrent consumer can never discover
	// nvidia-imex before everything it needs is present.
	keys := make([]string, 0, len(userspaceFiles))
	for key := range userspaceFiles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		file := userspaceFiles[key]
		if err := fsutil.Copy(extracted[key], filepath.Join(i.root, file.destination), file.mode); err != nil {
			return i.fail(fmt.Errorf("publish %s: %w", key, err))
		}
	}
	if err := fsutil.Copy(i.shimPath, filepath.Join(i.root, "driver/usr/bin/nvidia-imex"), 0o755); err != nil {
		return i.fail(fmt.Errorf("publish nvidia-imex shim: %w", err))
	}
	return nil
}

func (i *userspaceInstaller) fail(stageErr error) error {
	if discardErr := i.discard(); discardErr != nil {
		return errors.Join(stageErr, fmt.Errorf("discard incomplete IMEX userspace: %w", discardErr))
	}
	return stageErr
}

func (i *userspaceInstaller) discard() error {
	var errs []error
	for _, rel := range publishedUserspacePaths {
		if err := fsutil.Remove(filepath.Join(i.root, rel)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (i *userspaceInstaller) ensureArchive(ctx context.Context, artifact Artifact) (string, error) {
	cacheDir := filepath.Join(i.root, "cache/imex", i.lock.Version, i.arch)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create IMEX cache: %w", err)
	}
	archive := filepath.Join(cacheDir, "archive.tar.xz")

	valid, err := fileHasDigest(archive, artifact.SHA256)
	if err != nil {
		return "", err
	}
	if valid {
		return archive, nil
	}
	if err := os.Remove(archive); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove invalid cached archive: %w", err)
	}
	return i.downloadArchive(ctx, artifact, cacheDir, archive)
}

func (i *userspaceInstaller) downloadArchive(
	ctx context.Context, artifact Artifact, cacheDir, archive string,
) (string, error) {
	downloadURL, err := url.JoinPath(i.lock.BaseURL, artifact.Path)
	if err != nil {
		return "", fmt.Errorf("build download URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}
	resp, err := i.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download IMEX %s: %w", i.lock.Version, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := i.validateDownloadResponse(resp); err != nil {
		return "", err
	}
	return i.saveArchive(resp.Body, artifact, cacheDir, archive)
}

func (i *userspaceInstaller) validateDownloadResponse(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download IMEX %s: unexpected HTTP status %s", i.lock.Version, resp.Status)
	}
	if resp.ContentLength > maxArchiveBytes {
		return fmt.Errorf("download IMEX %s: archive is larger than %d bytes", i.lock.Version, maxArchiveBytes)
	}
	return nil
}

func (i *userspaceInstaller) saveArchive(
	reader io.Reader, artifact Artifact, cacheDir, archive string,
) (string, error) {
	tmp, err := os.CreateTemp(cacheDir, ".archive-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create archive temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(reader, maxArchiveBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return "", fmt.Errorf("download IMEX %s: %w", i.lock.Version, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close downloaded archive: %w", closeErr)
	}
	if written > maxArchiveBytes {
		return "", fmt.Errorf("download IMEX %s: archive is larger than %d bytes", i.lock.Version, maxArchiveBytes)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != artifact.SHA256 {
		return "", fmt.Errorf("verify IMEX %s archive: SHA-256 mismatch: got %s", i.lock.Version, actual)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return "", fmt.Errorf("chmod downloaded archive: %w", err)
	}
	if err := os.Rename(tmpPath, archive); err != nil {
		return "", fmt.Errorf("publish downloaded archive: %w", err)
	}
	return archive, nil
}

func fileHasDigest(filename, expected string) (bool, error) {
	f, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("open cached archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(f, maxArchiveBytes+1))
	if err != nil {
		return false, fmt.Errorf("hash cached archive: %w", err)
	}
	if read > maxArchiveBytes {
		return false, nil
	}
	return hex.EncodeToString(hash.Sum(nil)) == expected, nil
}

func extractUserspace(archive string) (map[string]string, func(), error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open IMEX archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	return extractUserspaceFrom(f)
}

func extractUserspaceFrom(reader io.Reader) (map[string]string, func(), error) {
	xzr, err := xz.NewReader(reader)
	if err != nil {
		return nil, func() {}, fmt.Errorf("read IMEX xz stream: %w", err)
	}
	tmp, err := os.MkdirTemp("", "mokka-imex-extract-*")
	if err != nil {
		return nil, func() {}, fmt.Errorf("create IMEX extraction directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	found, err := extractRequiredMembers(tar.NewReader(xzr), tmp)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if err := requireAllUserspaceFiles(found); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return found, cleanup, nil
}

func extractRequiredMembers(tr *tar.Reader, tmp string) (map[string]string, error) {
	found := make(map[string]string, len(userspaceFiles))
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read IMEX tar stream: %w", err)
		}
		relative, file, required, err := requiredArchiveMember(header)
		if err != nil {
			return nil, err
		}
		if !required {
			continue
		}
		dst, err := extractArchiveMember(tr, header, tmp, relative, file)
		if err != nil {
			return nil, err
		}
		found[relative] = dst
	}
	return found, nil
}

func requireAllUserspaceFiles(found map[string]string) error {
	for required := range userspaceFiles {
		if _, ok := found[required]; !ok {
			return fmt.Errorf("IMEX archive is missing %s", required)
		}
	}
	return nil
}

func requiredArchiveMember(header *tar.Header) (string, userspaceFile, bool, error) {
	memberName := strings.TrimSuffix(header.Name, "/")
	clean := path.Clean(memberName)
	if path.IsAbs(header.Name) || clean != memberName || strings.HasPrefix(clean, "../") {
		return "", userspaceFile{}, false, fmt.Errorf("unsafe IMEX archive member %q", header.Name)
	}
	_, relative, ok := strings.Cut(clean, "/")
	if !ok {
		return "", userspaceFile{}, false, nil
	}
	file, required := userspaceFiles[relative]
	if required && (header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxMemberBytes) {
		return "", file, false, fmt.Errorf("invalid IMEX archive member %q", header.Name)
	}
	return relative, file, required, nil
}

func extractArchiveMember(
	tr io.Reader, header *tar.Header, tmp, relative string, file userspaceFile,
) (string, error) {
	dst := filepath.Join(tmp, filepath.Base(relative))
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, file.mode)
	if err != nil {
		return "", fmt.Errorf("create extracted %s: %w", relative, err)
	}
	_, copyErr := io.CopyN(out, tr, header.Size)
	closeErr := out.Close()
	if copyErr != nil {
		return "", fmt.Errorf("extract %s: %w", relative, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close extracted %s: %w", relative, closeErr)
	}
	return dst, nil
}
