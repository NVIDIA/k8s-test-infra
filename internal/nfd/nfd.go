// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nfd writes feature files for the local source of Node Feature
// Discovery (NFD). NFD reads every file in FeaturesDir and labels the node
// feature.node.kubernetes.io/<name>=<value> for each line, or <name>=<value>
// verbatim when the name carries its own prefix.
package nfd

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// FeaturesDir is the NFD local-source directory, relative to the host /etc.
const FeaturesDir = "kubernetes/node-feature-discovery/features.d"

// Features maps feature names to their values.
type Features map[string]string

// Encode renders f in the local-source format, one name=value per line.
// Lines are sorted so that republishing the same features is byte-identical.
// Names and values must be valid label names and values: NFD drops anything
// else, and a stray '=' or newline would silently change what it reads.
func (f Features) Encode() ([]byte, error) {
	var errs []error
	for name, value := range f {
		for _, msg := range validation.IsQualifiedName(name) {
			errs = append(errs, fmt.Errorf("feature name %q: %s", name, msg))
		}
		for _, msg := range validation.IsValidLabelValue(value) {
			errs = append(errs, fmt.Errorf("feature %q value %q: %s", name, value, msg))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(f)) {
		b.WriteString(name + "=" + f[name] + "\n")
	}

	return []byte(b.String()), nil
}

// Decode parses local-source file contents the way NFD reads them: blank lines
// and '#' lines (comments and directives) are skipped, and a bare name without
// '=' stands for name=true.
func Decode(data []byte) Features {
	f := Features{}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		name, value, ok := strings.Cut(line, "=")
		if !ok {
			value = "true"
		}
		f[name] = value
	}

	return f
}

// Has reports whether name is published, whatever its value.
func (f Features) Has(name string) bool {
	_, ok := f[name]

	return ok
}

// Write atomically replaces path with the encoded features. Invalid features
// leave any existing file untouched.
func Write(path string, f Features) error {
	data, err := f.Encode()
	if err != nil {
		return fmt.Errorf("encode NFD features for %s: %w", path, err)
	}

	if err := fsutil.Write(path, data, 0o644); err != nil {
		return err
	}

	zap.L().Debug("NFD feature file written", zap.String("path", path), zap.Int("features", len(f)))

	return nil
}

// Delete removes the feature file at path so NFD withdraws its labels.
// An absent file is not an error.
func Delete(path string) error {
	if err := fsutil.Remove(path); err != nil {
		return err
	}

	zap.L().Debug("NFD feature file deleted", zap.String("path", path))

	return nil
}
