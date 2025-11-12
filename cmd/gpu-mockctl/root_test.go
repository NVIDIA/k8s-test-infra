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

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	gpuconfig "github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/config"
	"github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/internal/logger"
)

func TestNew(t *testing.T) {
	cmd := New()

	// Test basic properties
	if cmd.Name != "gpu-mockctl" {
		t.Errorf("Expected name=gpu-mockctl, got %s", cmd.Name)
	}

	// Test that subcommands are registered
	expectedCommands := []string{"fs", "version"}
	if len(cmd.Commands) != len(expectedCommands) {
		t.Errorf("Expected %d commands, got %d", len(expectedCommands), len(cmd.Commands))
	}

	for _, expected := range expectedCommands {
		found := false
		for _, c := range cmd.Commands {
			if c.Name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected command %s not found", expected)
		}
	}

	// Test flags
	hasVerbose := false
	hasMachine := false
	for _, f := range cmd.Flags {
		switch f.Names()[0] {
		case "verbose":
			hasVerbose = true
		case "machine":
			hasMachine = true
		}
	}
	if !hasVerbose {
		t.Error("Expected verbose flag not found")
	}
	if !hasMachine {
		t.Error("Expected machine flag not found")
	}
}

func TestGetLogger(t *testing.T) {
	// Test with logger in metadata
	cmd := &cli.Command{
		Metadata: map[string]interface{}{
			"logger": logger.New("test", true),
		},
	}
	log := getLogger(cmd)
	if log == nil {
		t.Error("Expected logger, got nil")
	}

	// Test fallback
	cmdEmpty := &cli.Command{}
	logFallback := getLogger(cmdEmpty)
	if logFallback == nil {
		t.Error("Expected fallback logger, got nil")
	}
}

func TestGetConfig(t *testing.T) {
	// Test with config in metadata
	testCfg := gpuconfig.NewDefault()
	testCfg.Machine = "test-machine"
	cmd := &cli.Command{
		Metadata: map[string]interface{}{
			"config": testCfg,
		},
	}
	retrievedCfg := getConfig(cmd)
	if retrievedCfg.Machine != "test-machine" {
		t.Errorf("Expected machine=test-machine, got %s", retrievedCfg.Machine)
	}

	// Test fallback
	cmdEmpty := &cli.Command{}
	cfgFallback := getConfig(cmdEmpty)
	if cfgFallback == nil {
		t.Error("Expected fallback config, got nil")
	}
}

func TestRootCommandValidation(t *testing.T) {
	// Test that validation runs in Before hook
	cmd := New()

	// Simulate running with invalid machine type
	args := []string{"gpu-mockctl", "--machine", "invalid"}
	err := cmd.Run(context.Background(), args)

	if err == nil {
		t.Error("Expected validation error for invalid machine type")
	}
	if !strings.Contains(err.Error(), "unsupported machine type") {
		t.Errorf("Expected unsupported machine type error, got: %v", err)
	}
}
