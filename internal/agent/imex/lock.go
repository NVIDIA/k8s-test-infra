// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

//go:embed imex.lock.json
var embeddedLock []byte

// Artifact identifies one architecture-specific archive.
type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Lock pins the IMEX release staged by the node daemon.
type Lock struct {
	Version   string              `json:"version"`
	BaseURL   string              `json:"baseURL"`
	Artifacts map[string]Artifact `json:"artifacts"`
}

func mustDefaultLock() Lock {
	lock, err := parseLock(embeddedLock)
	if err != nil {
		panic(fmt.Sprintf("invalid embedded IMEX lock: %v", err))
	}
	return lock
}

func parseLock(data []byte) (Lock, error) {
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lock{}, fmt.Errorf("decode: %w", err)
	}
	if err := lock.validate(); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

func (l Lock) validate() error {
	if strings.TrimSpace(l.Version) == "" || path.Base(l.Version) != l.Version || l.Version == "." {
		return errors.New("version must be a non-empty path component")
	}

	if err := validateBaseURL(l.BaseURL); err != nil {
		return err
	}

	for _, arch := range []string{"amd64", "arm64"} {
		artifact, ok := l.Artifacts[arch]
		if !ok {
			return fmt.Errorf("artifact for %s is required", arch)
		}
		if err := artifact.validate(arch); err != nil {
			return err
		}
	}
	return nil
}

func validateBaseURL(raw string) error {
	base, err := url.Parse(raw)
	if err != nil || base.Host == "" {
		return errors.New("baseURL must be an absolute HTTP(S) URL")
	}
	if base.Scheme != "https" && base.Scheme != "http" {
		return errors.New("baseURL must be an absolute HTTP(S) URL")
	}
	return nil
}

func (a Artifact) validate(arch string) error {
	if a.Path == "" || path.IsAbs(a.Path) || path.Clean(a.Path) != a.Path || strings.HasPrefix(a.Path, "../") {
		return fmt.Errorf("artifact path for %s must be a clean relative path", arch)
	}
	digest, err := hex.DecodeString(a.SHA256)
	if err != nil || len(digest) != 32 || a.SHA256 != strings.ToLower(a.SHA256) {
		return fmt.Errorf("artifact SHA-256 for %s must contain 64 lowercase hexadecimal characters", arch)
	}
	return nil
}

func (l Lock) artifact(arch string) (Artifact, error) {
	artifact, ok := l.Artifacts[arch]
	if !ok {
		return Artifact{}, fmt.Errorf("unsupported architecture %q", arch)
	}
	return artifact, nil
}
