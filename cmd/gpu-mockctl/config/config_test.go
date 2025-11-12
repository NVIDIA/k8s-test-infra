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

package config

import (
	"os"
	"testing"
)

func TestNewDefault(t *testing.T) {
	// Test default values
	cfg := NewDefault()

	if cfg.Base != "/run/nvidia/driver" {
		t.Errorf("Expected base=/run/nvidia/driver, got %s", cfg.Base)
	}
	if cfg.Machine != "dgxa100" {
		t.Errorf("Expected machine=dgxa100, got %s", cfg.Machine)
	}

	// Test environment variable override
	if err := os.Setenv("MACHINE_TYPE", "test-machine"); err != nil {
		t.Fatalf("Failed to set env: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("MACHINE_TYPE"); err != nil {
			t.Logf("Failed to unset env: %v", err)
		}
	}()

	cfg2 := NewDefault()
	if cfg2.Machine != "test-machine" {
		t.Errorf("Expected machine from env=test-machine, got %s", cfg2.Machine)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		env     map[string]string
		wantErr bool
	}{
		{
			name: "valid dgxa100",
			cfg: &Config{
				Machine: "dgxa100",
			},
			wantErr: false,
		},
		{
			name: "unsupported machine without override",
			cfg: &Config{
				Machine: "unsupported",
			},
			wantErr: true,
		},
		{
			name: "unsupported machine with override",
			cfg: &Config{
				Machine: "unsupported",
			},
			env: map[string]string{
				"ALLOW_UNSUPPORTED": "true",
			},
			wantErr: false,
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

			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateFS(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &Config{
				Base:    "/run/nvidia/driver",
				Machine: "dgxa100",
			},
			wantErr: false,
		},
		{
			name: "empty base path",
			cfg: &Config{
				Base:    "",
				Machine: "dgxa100",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.ValidateFS()
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateFS() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
