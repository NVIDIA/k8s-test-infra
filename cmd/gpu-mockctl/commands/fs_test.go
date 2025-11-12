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
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gpuconfig "github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/config"
	"github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/internal/logger"
)

func TestNewFSCommand(t *testing.T) {
	cfg := gpuconfig.NewDefault()
	cmd := NewFSCommand(cfg)

	// Test basic properties
	if cmd.Name != "fs" {
		t.Errorf("Expected name=fs, got %s", cmd.Name)
	}

	// Test flags
	hasBase := false
	for _, f := range cmd.Flags {
		if f.Names()[0] == "base" {
			hasBase = true
		}
	}
	if !hasBase {
		t.Error("Expected base flag not found")
	}
}

func TestRunFS(t *testing.T) {
	// Note: This test may fail with "operation not permitted" errors
	// when trying to create device nodes with mknod.
	// This is expected behavior when not running as root.
	// In production, this command would typically run in a privileged container.

	// Skip this test if not running as root
	if os.Getuid() != 0 {
		t.Skip("Skipping test that requires root privileges for mknod")
	}

	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "gpu-mockctl-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

	// Capture log output
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	tests := []struct {
		name    string
		cfg     *gpuconfig.Config
		env     map[string]string
		wantErr bool
		wantLog []string
	}{
		{
			name: "valid dgxa100",
			cfg: &gpuconfig.Config{
				Base:    filepath.Join(tmpDir, "valid"),
				Machine: "dgxa100",
			},
			wantErr: false,
			wantLog: []string{
				"Creating mock filesystem for machine: dgxa100",
				"Mock filesystem written under",
			},
		},
		{
			name: "unsupported machine with override",
			cfg: &gpuconfig.Config{
				Base:    filepath.Join(tmpDir, "unsupported"),
				Machine: "unsupported",
			},
			env: map[string]string{
				"ALLOW_UNSUPPORTED": "true",
			},
			wantErr: false,
			wantLog: []string{
				"WARNING: Unsupported machine \"unsupported\", using fallback",
				"Mock filesystem written under",
			},
		},
		{
			name: "unsupported machine without override",
			cfg: &gpuconfig.Config{
				Base:    filepath.Join(tmpDir, "unsupported-no-override"),
				Machine: "unsupported",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for k, v := range tt.env {
				if err := os.Setenv(k, v); err != nil {
					t.Fatalf("Failed to set env %s: %v", k, err)
				}
				defer func(key string) {
					if err := os.Unsetenv(key); err != nil {
						t.Logf("Failed to unset env %s: %v", key, err)
					}
				}(k)
			}

			buf.Reset()
			testLogger := logger.New("test", false)
			err := runFS(tt.cfg, testLogger)

			if (err != nil) != tt.wantErr {
				t.Errorf("runFS() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Check log output
			logOutput := buf.String()
			for _, want := range tt.wantLog {
				if !strings.Contains(logOutput, want) {
					t.Errorf("Expected log to contain %q, got:\n%s", want, logOutput)
				}
			}

			// If successful, check that files were created
			if !tt.wantErr && err == nil {
				// Check that the base directory exists
				if _, err := os.Stat(tt.cfg.Base); os.IsNotExist(err) {
					t.Errorf("Expected base directory %s to exist", tt.cfg.Base)
				}
			}
		})
	}
}

func TestFSCommandValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *gpuconfig.Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &gpuconfig.Config{
				Base:    "/tmp/test",
				Machine: "dgxa100",
			},
			wantErr: false,
		},
		{
			name: "empty base path",
			cfg: &gpuconfig.Config{
				Base:    "",
				Machine: "dgxa100",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewFSCommand(tt.cfg)

			// The Before hook should validate
			ctx := context.Background()
			_, err := cmd.Before(ctx, cmd)

			if (err != nil) != tt.wantErr {
				t.Errorf("Before() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
