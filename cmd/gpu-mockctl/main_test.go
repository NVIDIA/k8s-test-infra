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
	"testing"
)

func TestMain(t *testing.T) {
	// Test that the New() function is available and returns a valid command
	cmd := New()
	if cmd == nil {
		t.Fatal("New() returned nil")
	}

	// Test that the command has the expected name
	if cmd.Name != "gpu-mockctl" {
		t.Errorf("Expected command name 'gpu-mockctl', got %s", cmd.Name)
	}

	// Test that help works
	err := cmd.Run(context.Background(), []string{"gpu-mockctl", "--help"})
	if err != nil {
		t.Errorf("Help command failed: %v", err)
	}

	// Test that it has the expected subcommands
	expectedCommands := map[string]bool{
		"fs":      false,
		"cdi":     false,
		"version": false,
	}

	for _, subcmd := range cmd.Commands {
		if _, ok := expectedCommands[subcmd.Name]; ok {
			expectedCommands[subcmd.Name] = true
		}
	}

	for name, found := range expectedCommands {
		if !found {
			t.Errorf("Expected command %s not found", name)
		}
	}
}
