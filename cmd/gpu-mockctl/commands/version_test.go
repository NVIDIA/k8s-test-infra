// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestNewVersionCommand(t *testing.T) {
	cmd := NewVersionCommand()

	// Test basic properties
	if cmd.Name != "version" {
		t.Errorf("Expected name=version, got %s", cmd.Name)
	}

	// Test flags
	expectedFlags := map[string]bool{
		"short": false,
		"json":  false,
	}

	for _, f := range cmd.Flags {
		name := f.Names()[0]
		if _, expected := expectedFlags[name]; expected {
			expectedFlags[name] = true
		}
	}

	for flag, found := range expectedFlags {
		if !found {
			t.Errorf("Expected flag %s not found", flag)
		}
	}
}

func TestShowVersion(t *testing.T) {
	// Save original stdout
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()

	// Save original version values
	origVersion := Version
	origGitCommit := GitCommit
	origBuildDate := BuildDate
	defer func() {
		Version = origVersion
		GitCommit = origGitCommit
		BuildDate = origBuildDate
	}()

	// Set test values
	Version = "1.2.3"
	GitCommit = "abc123"
	BuildDate = "2025-01-01T00:00:00Z"

	tests := []struct {
		name    string
		flags   map[string]interface{}
		wantOut []string
		notWant []string
	}{
		{
			name:  "default output",
			flags: map[string]interface{}{},
			wantOut: []string{
				"gpu-mockctl version: 1.2.3",
				"git commit: abc123",
				"build date: 2025-01-01T00:00:00Z",
				fmt.Sprintf("go version: %s", runtime.Version()),
				fmt.Sprintf("platform:   %s/%s", runtime.GOOS, runtime.GOARCH),
			},
		},
		{
			name: "short output",
			flags: map[string]interface{}{
				"short": true,
			},
			wantOut: []string{"1.2.3"},
			notWant: []string{"git commit", "build date", "go version"},
		},
		{
			name: "json output",
			flags: map[string]interface{}{
				"json": true,
			},
			wantOut: []string{
				`"version": "1.2.3"`,
				`"gitCommit": "abc123"`,
				`"buildDate": "2025-01-01T00:00:00Z"`,
				`"goVersion":`,
				`"platform":`,
				"{",
				"}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create pipe to capture output
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe: %v", err)
			}
			os.Stdout = w

			// Create the version command
			versionCmd := NewVersionCommand()

			// Build args based on flags
			args := []string{"test", "version"}
			for flag, value := range tt.flags {
				if boolVal, ok := value.(bool); ok && boolVal {
					args = append(args, "--"+flag)
				}
			}

			// Create app with version command
			app := &cli.Command{
				Commands: []*cli.Command{versionCmd},
			}

			// Run the command
			runErr := app.Run(context.Background(), args)
			if runErr != nil {
				t.Errorf("Run() error = %v", runErr)
			}

			// Close writer and read output
			if err := w.Close(); err != nil {
				t.Logf("Failed to close writer: %v", err)
			}
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(r); err != nil {
				t.Fatalf("Failed to read output: %v", err)
			}
			output := buf.String()

			// Check expected output
			for _, want := range tt.wantOut {
				if !strings.Contains(output, want) {
					t.Errorf("Expected output to contain %q, got:\n%s", want, output)
				}
			}

			// Check unwanted output
			for _, notWant := range tt.notWant {
				if strings.Contains(output, notWant) {
					t.Errorf("Expected output NOT to contain %q, got:\n%s", notWant, output)
				}
			}
		})
	}
}

func TestVersionInfo(t *testing.T) {
	// Test that VersionInfo struct contains expected fields
	v := VersionInfo{
		Version:   "test",
		GitCommit: "test-commit",
		BuildDate: "test-date",
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}

	if v.Version != "test" {
		t.Errorf("Expected Version=test, got %s", v.Version)
	}
	if v.GitCommit != "test-commit" {
		t.Errorf("Expected GitCommit=test-commit, got %s", v.GitCommit)
	}
	if v.BuildDate != "test-date" {
		t.Errorf("Expected BuildDate=test-date, got %s", v.BuildDate)
	}
	if v.GoVersion == "" {
		t.Error("Expected GoVersion to be non-empty")
	}
	if v.Compiler == "" {
		t.Error("Expected Compiler to be non-empty")
	}
	if v.Platform == "" {
		t.Error("Expected Platform to be non-empty")
	}
}

func TestVersionCommandIntegration(t *testing.T) {
	// Test that the command can be created and has correct structure
	cmd := NewVersionCommand()

	// Test running with context
	ctx := context.Background()

	// Create a test app to run the command
	app := &cli.Command{
		Commands: []*cli.Command{cmd},
	}

	// Test help output
	err := app.Run(ctx, []string{"test", "version", "--help"})
	// Help returns an error but it's expected
	// Some versions of cli don't return error for help
	// This is fine, we just want to make sure it doesn't panic
	_ = err // Ignore the error - we're just testing it doesn't panic
}
