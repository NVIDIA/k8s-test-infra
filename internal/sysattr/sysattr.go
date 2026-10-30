// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package sysattr writes sysfs attribute files the way the kernel shows them:
// one value per file, on a single line ending in a newline. Readers such as
// NFD trim that newline, so a value spanning lines would read as something
// else.
package sysattr

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// Attributes maps attribute file names in one sysfs directory to their values.
type Attributes map[string]string

// validate reports names that are not a single file in the directory and
// values that are not a single line.
func (a Attributes) validate() error {
	var errs []error
	for name, value := range a {
		if name == "" || name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) {
			errs = append(errs, fmt.Errorf("attribute name %q: not a file name", name))
		}
		if strings.ContainsRune(value, '\n') {
			errs = append(errs, fmt.Errorf("attribute %q value %q: spans lines", name, value))
		}
	}
	return errors.Join(errs...)
}

// Write atomically replaces each attribute file in dir, creating dir as
// needed. Invalid attributes leave dir untouched.
func Write(dir string, a Attributes, perm os.FileMode) error {
	if err := a.validate(); err != nil {
		return fmt.Errorf("sysfs attributes for %s: %w", dir, err)
	}

	for _, name := range slices.Sorted(maps.Keys(a)) {
		if err := fsutil.Write(filepath.Join(dir, name), []byte(a[name]+"\n"), perm); err != nil {
			return err
		}
	}

	zap.L().Debug("sysfs attributes written", zap.String("dir", dir), zap.Int("attributes", len(a)))

	return nil
}

// Read returns every attribute in dir, trimmed the way readers trim them.
func Read(dir string) (Attributes, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	a := make(Attributes, len(entries))
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		a[e.Name()] = strings.TrimSpace(string(data))
	}

	return a, nil
}
