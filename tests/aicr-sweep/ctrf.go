// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"sigs.k8s.io/yaml"
)

// ctrfReport is the subset of the CTRF schema that AICR emits and this harness
// consumes. AICR's own definition is pkg/validator/ctrf/types.go; the full
// schema is at https://ctrf.io.
type ctrfReport struct {
	ReportFormat string `json:"reportFormat"`
	Results      struct {
		Tests []struct {
			Name    string   `json:"name"`
			Status  string   `json:"status"`
			Suite   []string `json:"suite"`
			Message string   `json:"message"`
		} `json:"tests"`
	} `json:"results"`
}

// ParseCTRF reads one CTRF report.
//
// A report with no tests is an error rather than an empty success. An empty
// result set and a clean run are indistinguishable in the output otherwise, and
// the first would silently become a 0-denominator pass.
func ParseCTRF(data []byte) (*RunResults, error) {
	var report ctrfReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse CTRF report: %w", err)
	}

	if len(report.Results.Tests) == 0 {
		return nil, fmt.Errorf("CTRF report contains no tests, refusing to treat an empty report as a run")
	}

	run := &RunResults{Tests: make([]RunTest, 0, len(report.Results.Tests))}
	for _, test := range report.Results.Tests {
		var suite string
		if len(test.Suite) > 0 {
			suite = test.Suite[0]
		}
		run.Tests = append(run.Tests, RunTest{
			Name:    test.Name,
			Status:  test.Status,
			Suite:   suite,
			Message: test.Message,
		})
	}

	return run, nil
}

// LoadCTRFDir merges every *.json CTRF report in dir, so a run split across
// deployment, performance and conformance phase reports lands as one result set.
func LoadCTRFDir(dir string) (*RunResults, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("scan CTRF directory %s: %w", dir, err)
	}
	sort.Strings(entries)

	merged := &RunResults{}
	for _, path := range entries {
		data, err := os.ReadFile(path) //nolint:gosec // operator-supplied results path
		if err != nil {
			return nil, fmt.Errorf("read CTRF report %s: %w", path, err)
		}
		run, err := ParseCTRF(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		merged.Tests = append(merged.Tests, run.Tests...)
	}

	if len(merged.Tests) == 0 {
		return nil, fmt.Errorf("no CTRF reports with results found in %s", dir)
	}

	return merged, nil
}

// LoadCatalog reads and validates the coverage catalog. An invalid catalog is an
// error rather than a warning, because every downstream number depends on it.
func LoadCatalog(path string) (*Catalog, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied catalog path
	if err != nil {
		return nil, fmt.Errorf("read catalog %s: %w", path, err)
	}

	var catalog Catalog
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}

	if err := catalog.Validate(); err != nil {
		return nil, fmt.Errorf("catalog %s is invalid: %w", path, err)
	}

	return &catalog, nil
}
