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
	"log"
	"os"
	"strings"
	"testing"

	gpuconfig "github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/config"
	"github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/internal/logger"
)

func TestRunFSLogic(t *testing.T) {
	// This test verifies the logic of runFS without actually creating device nodes
	// It focuses on topology handling and logging

	// Capture log output
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	tests := []struct {
		name        string
		cfg         *gpuconfig.Config
		env         map[string]string
		wantErr     bool
		wantLogMsgs []string
	}{
		{
			name: "valid machine type logs correctly",
			cfg: &gpuconfig.Config{
				Base:    "/tmp/test",
				Machine: "dgxa100",
			},
			wantErr: false,
			wantLogMsgs: []string{
				"Creating mock filesystem for machine: dgxa100",
			},
		},
		{
			name: "unsupported machine with override logs warning",
			cfg: &gpuconfig.Config{
				Base:    "/tmp/test-unsupported",
				Machine: "unsupported-test",
			},
			env: map[string]string{
				"ALLOW_UNSUPPORTED": "true",
			},
			wantErr: false,
			wantLogMsgs: []string{
				"Creating mock filesystem for machine: unsupported-test",
				"WARNING: Unsupported machine \"unsupported-test\", using fallback",
			},
		},
		{
			name: "unsupported machine without override returns error",
			cfg: &gpuconfig.Config{
				Base:    "/tmp/test-error",
				Machine: "unsupported-test",
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

			// Note: This will fail at the Write() step due to mknod permissions
			// but we can still verify the logic up to that point
			testLogger := logger.New("test", false)
			err := runFS(tt.cfg, testLogger)

			// For non-error cases, we expect mknod permission error
			if !tt.wantErr && err != nil {
				if !strings.Contains(err.Error(), "operation not permitted") &&
					!strings.Contains(err.Error(), "permission denied") {
					t.Errorf("Unexpected error: %v", err)
				}
			} else if (err != nil && !strings.Contains(err.Error(), "mknod")) != tt.wantErr {
				t.Errorf("runFS() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Check log messages
			logOutput := buf.String()
			for _, msg := range tt.wantLogMsgs {
				if !strings.Contains(logOutput, msg) {
					t.Errorf("Expected log to contain %q, got:\n%s", msg, logOutput)
				}
			}
		})
	}
}

func TestGetLogger(t *testing.T) {
	// Test the getLogger function
	testLogger := logger.New("test", true)

	// Test with nil command
	log := getLogger(nil)
	if log == nil {
		t.Error("Expected fallback logger for nil command")
	}

	// The actual metadata retrieval is tested through integration
	// with the root command in the main test suite
	_ = testLogger // Use the variable to avoid linter warning
}
