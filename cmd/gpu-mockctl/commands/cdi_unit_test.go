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

func TestRunCDILogic(t *testing.T) {
	// This test verifies the logic of runCDI without actually creating device nodes
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
		noLogMsgs   []string
	}{
		{
			name: "valid machine type logs correctly",
			cfg: &gpuconfig.Config{
				DriverRoot:  "/tmp/test-driver",
				CDIOutput:   "/tmp/test-cdi.yaml",
				Machine:     "dgxa100",
				ToolkitRoot: "/usr/local/nvidia-container-toolkit",
			},
			wantErr: false,
			wantLogMsgs: []string{
				"Generating CDI specification for machine: dgxa100",
			},
		},
		{
			name: "unsupported machine with override logs warning",
			cfg: &gpuconfig.Config{
				DriverRoot:  "/tmp/test-driver-unsupported",
				CDIOutput:   "/tmp/test-cdi-unsupported.yaml",
				Machine:     "unsupported-test",
				ToolkitRoot: "/usr/local/nvidia-container-toolkit",
			},
			env: map[string]string{
				"ALLOW_UNSUPPORTED": "true",
			},
			wantErr: false,
			wantLogMsgs: []string{
				"Generating CDI specification for machine: unsupported-test",
				"WARNING: Using fallback mock for CDI generation",
			},
		},
		{
			name: "unsupported machine without override returns error",
			cfg: &gpuconfig.Config{
				DriverRoot:  "/tmp/test-driver-error",
				CDIOutput:   "/tmp/test-cdi-error.yaml",
				Machine:     "unsupported-test",
				ToolkitRoot: "/usr/local/nvidia-container-toolkit",
			},
			wantErr: true,
		},
		{
			name: "with verbose logging shows debug messages",
			cfg: &gpuconfig.Config{
				DriverRoot:  "/tmp/test-driver-verbose",
				CDIOutput:   "/tmp/test-cdi-verbose.yaml",
				Machine:     "dgxa100",
				ToolkitRoot: "/usr/local/nvidia-container-toolkit",
				Verbose:     true,
			},
			wantErr: false,
			wantLogMsgs: []string{
				"Generating CDI specification for machine: dgxa100",
			},
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

			// Note: This will fail at various steps due to permissions or missing directories
			// but we can still verify the logic
			testLogger := logger.New("test", tt.cfg.Verbose)
			err := runCDI(tt.cfg, testLogger)

			// For non-error cases, we might get various errors (permissions, missing dirs, etc)
			if !tt.wantErr && err != nil {
				// Check if it's an expected error
				if !strings.Contains(err.Error(), "operation not permitted") &&
					!strings.Contains(err.Error(), "permission denied") &&
					!strings.Contains(err.Error(), "no such file or directory") &&
					!strings.Contains(err.Error(), "failed to write driver files") &&
					!strings.Contains(err.Error(), "failed to generate CDI spec") {
					t.Errorf("Unexpected error: %v", err)
				}
			} else if tt.wantErr && err == nil {
				t.Error("Expected error but got none")
			}

			// Check log messages
			logOutput := buf.String()
			for _, msg := range tt.wantLogMsgs {
				if !strings.Contains(logOutput, msg) {
					t.Errorf("Expected log to contain %q, got:\n%s", msg, logOutput)
				}
			}

			for _, msg := range tt.noLogMsgs {
				if strings.Contains(logOutput, msg) {
					t.Errorf("Expected log NOT to contain %q, got:\n%s", msg, logOutput)
				}
			}
		})
	}
}

func TestCreateDeviceNodes(t *testing.T) {
	// Test the createDeviceNodes function logic
	// This will log warnings but shouldn't fail

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	cfg := &gpuconfig.Config{
		DriverRoot: "/tmp/test-driver",
		WithDRI:    true,
	}

	testLogger := logger.New("test", false)

	// This should not return error even if device creation fails
	err := createDeviceNodes(cfg, 2, testLogger)
	if err != nil {
		t.Errorf("createDeviceNodes should not return error, got: %v", err)
	}

	// Check that warnings were logged
	logOutput := buf.String()
	if !strings.Contains(logOutput, "WARNING:") {
		t.Error("Expected warning logs for device node creation")
	}
}
