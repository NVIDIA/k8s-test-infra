// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ulikunitz/xz"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/imex"
)

// The pinned SHA-256, checked before extraction, is what bounds an archive;
// these limits are defense in depth. maxArchiveBytes caps the compressed file,
// maxDecompressedBytes the whole tar stream, including members that are
// skipped, and maxMemberBytes each extracted file. IMEX 595.91.07 is about
// 8 MB compressed and 27 MB decompressed; its largest member is 17 MB.
const (
	maxArchiveBytes      = 64 << 20
	maxDecompressedBytes = 128 << 20
	maxMemberBytes       = 64 << 20
)

type archiveFile struct {
	// member is the path inside the archive's top-level directory.
	member string
	// destination is the path under the host root.
	destination string
	mode        os.FileMode
}

// archiveFiles is the extraction allowlist. The license and third-party notices
// travel with the binaries they cover.
var archiveFiles = []archiveFile{
	{member: "usr/bin/nvidia-imex", destination: path.Join(imex.BinDir, imex.RealDaemonBinary), mode: 0o755},
	{member: "usr/bin/nvidia-imex-ctl", destination: path.Join(imex.BinDir, imex.CtlBinary), mode: 0o755},
	{member: "etc/nvidia-imex/config.cfg", destination: "driver/etc/nvidia-imex/config.cfg", mode: 0o644},
	{member: "LICENSE", destination: "driver/usr/share/doc/nvidia-imex/LICENSE", mode: 0o644},
	{member: "usr/share/doc/third-party-notices.txt", destination: "driver/usr/share/doc/nvidia-imex/third-party-notices.txt", mode: 0o644},
}

// shimDestination is where the --nogpu shim is published, under the name the
// DRA driver runs.
const shimDestination = imex.BinDir + "/" + imex.DaemonBinary

// realDestination is NVIDIA's daemon, which the shim runs. It is the presence
// signal: published last and withdrawn first, so on disk it means the shim and
// every file of archiveFiles are a complete set from one archive.
const realDestination = imex.BinDir + "/" + imex.RealDaemonBinary

// stageFromArchive replaces the staged IMEX with the allowlisted members of
// archive, whose bytes must hash to expected. It extracts everything first,
// withdraws nvidia-imex.real, writes the shim and the rest of archiveFiles, and
// publishes nvidia-imex.real last, so a consumer that finds it finds a complete
// set from this archive. Each file is replaced atomically but the set is not:
// the node has no IMEX until nvidia-imex.real is back. A tree that already
// holds exactly these files, as a node agent finds after its predecessor
// crashed, is left alone, so IMEX stays available across that restart. On
// failure it withdraws IMEX, including an install that worked before.
func stageFromArchive(root, shimPath, archive, expected string) error {
	extracted, cleanup, err := extractArchive(archive, expected)
	if err != nil {
		return err
	}
	defer cleanup()

	if sameInstall(root, shimPath, extracted) {
		return nil
	}

	if err := withdrawNodeSoftware(root); err != nil {
		return fmt.Errorf("withdraw %s: %w", imex.RealDaemonBinary, err)
	}
	if err := fsutil.Copy(shimPath, filepath.Join(root, shimDestination), 0o755); err != nil {
		return withdrawAfter(root, fmt.Errorf("publish nvidia-imex shim: %w", err))
	}
	var daemon archiveFile
	for _, file := range archiveFiles {
		if file.destination == realDestination {
			daemon = file
			continue
		}
		if err := fsutil.Copy(extracted[file.member], filepath.Join(root, file.destination), file.mode); err != nil {
			return withdrawAfter(root, fmt.Errorf("publish %s: %w", file.member, err))
		}
	}
	if err := fsutil.Copy(extracted[daemon.member], filepath.Join(root, daemon.destination), daemon.mode); err != nil {
		return withdrawAfter(root, fmt.Errorf("publish %s: %w", daemon.member, err))
	}
	return nil
}

func withdrawAfter(root string, publishErr error) error {
	if err := withdrawNodeSoftware(root); err != nil {
		return errors.Join(publishErr, fmt.Errorf("withdraw incomplete IMEX node software: %w", err))
	}
	return publishErr
}

// withdrawNodeSoftware withdraws IMEX by removing nvidia-imex.real only. The
// shim then fails as nvidia-imex does on a node without the IMEX package. The
// other files stay, because the DRA driver's GPU kubelet plugin adds
// nvidia-imex and nvidia-imex-ctl, found when it started, to the CDI spec of
// every claim it prepares, and a container restarted from such a claim fails
// if a bind source is gone.
func withdrawNodeSoftware(root string) error {
	return fsutil.Remove(filepath.Join(root, realDestination))
}

// Staged reports whether IMEX node software is staged under root.
// nvidia-imex.real is published last and withdrawn first, so its presence
// means the whole set is there.
func Staged(root string) bool {
	return exists(filepath.Join(root, realDestination))
}

// sameInstall reports whether root holds the shim and every file of
// archiveFiles with the content of shimPath and the extracted members.
func sameInstall(root, shimPath string, extracted map[string]string) bool {
	if !sameContent(shimPath, filepath.Join(root, shimDestination)) {
		return false
	}
	for _, file := range archiveFiles {
		if !sameContent(extracted[file.member], filepath.Join(root, file.destination)) {
			return false
		}
	}
	return true
}

func sameContent(a, b string) bool {
	left, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	right, err := os.ReadFile(b)
	return err == nil && bytes.Equal(left, right)
}

// fullyPublished reports whether the shim and every file of archiveFiles are on
// disk under root.
func fullyPublished(root string) bool {
	if !exists(filepath.Join(root, shimDestination)) {
		return false
	}
	for _, file := range archiveFiles {
		if !exists(filepath.Join(root, file.destination)) {
			return false
		}
	}
	return true
}

// openArchive opens filename for reading only if it is a regular file. It
// refuses a symlink, and a FIFO or device, which could block staging under the
// staging lock.
func openArchive(filename string) (*os.File, error) {
	f, err := os.OpenFile(filename, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", filename)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// fileHasDigest reports whether filename exists and its SHA-256 is expected. A
// missing file is not an error.
func fileHasDigest(filename, expected string) (bool, error) {
	f, err := openArchive(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("open IMEX archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(f, maxArchiveBytes+1))
	if err != nil {
		return false, fmt.Errorf("hash IMEX archive: %w", err)
	}
	if read > maxArchiveBytes {
		return false, nil
	}
	return hex.EncodeToString(hash.Sum(nil)) == expected, nil
}

// extractArchive extracts the allowlisted members of archive and checks that
// the bytes it read hash to expected, so what it extracts is what was
// verified even if the file changed after an earlier check.
func extractArchive(archive, expected string) (map[string]string, func(), error) {
	f, err := openArchive(archive)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open IMEX archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	limited := &io.LimitedReader{R: f, N: maxArchiveBytes + 1}
	hash := sha256.New()
	compressed := io.TeeReader(limited, hash)
	extracted, cleanup, err := extractArchiveFrom(compressed, maxDecompressedBytes)
	if err != nil {
		return nil, func() {}, err
	}
	// The tar reader stops at its end-of-archive marker; hash the rest too.
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("hash IMEX archive: %w", err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); limited.N == 0 || got != expected {
		cleanup()
		return nil, func() {}, fmt.Errorf("IMEX archive %s changed after it was verified", archive)
	}
	return extracted, cleanup, nil
}

// extractArchiveFrom extracts the allowlisted members of an xz-compressed tar
// stream into a temporary directory, reading at most maxDecompressed bytes of
// tar, and returns where each member landed and how to remove them.
func extractArchiveFrom(reader io.Reader, maxDecompressed int64) (map[string]string, func(), error) {
	xzr, err := xz.NewReader(reader)
	if err != nil {
		return nil, func() {}, fmt.Errorf("read IMEX xz stream: %w", err)
	}
	tmp, err := os.MkdirTemp("", "mokka-imex-extract-*")
	if err != nil {
		return nil, func() {}, fmt.Errorf("create IMEX extraction directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	stream := &io.LimitedReader{R: xzr, N: maxDecompressed + 1}
	found, err := extractRequiredMembers(tar.NewReader(stream), tmp)
	if stream.N == 0 {
		err = fmt.Errorf("IMEX archive decompresses to more than %d bytes", maxDecompressed)
	}
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return found, cleanup, nil
}

// extractRequiredMembers extracts every allowlisted member into tmp and fails
// unless the archive holds each of them exactly once. Every member's name is
// checked, allowlisted or not.
func extractRequiredMembers(tr *tar.Reader, tmp string) (map[string]string, error) {
	found := make(map[string]string, len(archiveFiles))
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read IMEX tar stream: %w", err)
		}
		member, err := memberPath(header.Name)
		if err != nil {
			return nil, err
		}
		file, ok := allowlisted(member)
		if !ok {
			continue
		}
		if found[member], err = extractMember(tr, header, file, tmp); err != nil {
			return nil, err
		}
	}
	for _, file := range archiveFiles {
		if _, ok := found[file.member]; !ok {
			return nil, fmt.Errorf("IMEX archive is missing %s", file.member)
		}
	}
	return found, nil
}

// memberPath validates an archive member's name and returns it relative to the
// archive's top-level directory, or "" for that directory itself.
func memberPath(name string) (string, error) {
	trimmed := strings.TrimSuffix(name, "/")
	clean := path.Clean(trimmed)
	if path.IsAbs(name) || clean != trimmed || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe IMEX archive member %q", name)
	}
	_, member, _ := strings.Cut(clean, "/")
	return member, nil
}

func allowlisted(member string) (archiveFile, bool) {
	for _, file := range archiveFiles {
		if file.member == member {
			return file, true
		}
	}
	return archiveFile{}, false
}

// extractMember writes the member tr is positioned at into tmp and returns its
// path there.
func extractMember(tr io.Reader, header *tar.Header, file archiveFile, tmp string) (string, error) {
	if header.Typeflag != tar.TypeReg || header.Size > maxMemberBytes {
		return "", fmt.Errorf("invalid IMEX archive member %q", header.Name)
	}
	// Named from the allowlist, never from the archive entry, so no name the
	// archive controls reaches the filesystem. O_EXCL rejects a member the
	// archive holds twice.
	dst := filepath.Join(tmp, filepath.Base(file.destination))
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, file.mode)
	if err != nil {
		return "", fmt.Errorf("create extracted %s: %w", file.member, err)
	}
	_, copyErr := io.CopyN(out, tr, header.Size)
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return "", fmt.Errorf("extract %s: %w", file.member, err)
	}
	return dst, nil
}
