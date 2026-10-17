// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
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
)

// FetchArchive places the archive lock pins for arch in dir, under the file
// name NVIDIA publishes it with, and returns its path. A copy already there
// that matches the pin is kept, so a populated dir needs no network. Otherwise
// the archive is downloaded from the lock's base URL, and it takes that name
// only once its SHA-256 matches. hack/tools/imex-archives uses it to build the IMEX
// binaries image; the node agent uses it once the IMEXDownload gate is on.
func FetchArchive(ctx context.Context, client *http.Client, lock Lock, arch, dir string) (string, error) {
	artifact, err := lock.artifact(arch)
	if err != nil {
		return "", err
	}
	archive := filepath.Join(dir, path.Base(artifact.Path))

	valid, err := fileHasDigest(archive, artifact.SHA256)
	if err != nil {
		return "", err
	}
	if valid {
		return archive, nil
	}

	source, err := url.JoinPath(lock.BaseURL, artifact.Path)
	if err != nil {
		return "", fmt.Errorf("IMEX archive URL: %w", err)
	}
	if err := download(ctx, client, source, archive, artifact.SHA256); err != nil {
		return "", err
	}
	return archive, nil
}

// download fetches source and stores it as dst once it matches expected.
func download(ctx context.Context, client *http.Client, source, dst, expected string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", source, err)
	}
	https := *client
	https.CheckRedirect = httpsOnly
	resp, err := https.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", source, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", source, resp.Status)
	}
	if err := storeVerified(resp.Body, dst, expected); err != nil {
		return fmt.Errorf("download %s: %w", source, err)
	}
	return nil
}

// httpsOnly follows a redirect only to HTTPS: the download becomes a binary
// the node executes, so a redirect must not downgrade it to cleartext.
func httpsOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing a redirect to %s, which is not HTTPS", req.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// storeVerified writes body to a hidden sibling of dst, then renames it over
// dst once its size and SHA-256 check out.
func storeVerified(body io.Reader, dst, expected string) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", dst, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // a no-op once the rename lands

	hash := sha256.New()
	read, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(body, maxArchiveBytes+1))
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		return copyErr
	case closeErr != nil:
		return fmt.Errorf("close %s: %w", tmp, closeErr)
	case read > maxArchiveBytes:
		return fmt.Errorf("larger than %d bytes", maxArchiveBytes)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != expected {
		return fmt.Errorf("SHA-256 %s does not match the pinned %s", got, expected)
	}

	// CreateTemp opens 0600; the archive is not secret.
	if err := os.Chmod(tmp, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename %s: %w", dst, err)
	}
	return nil
}
