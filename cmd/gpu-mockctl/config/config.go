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
	"fmt"
	"os"
)

// Config holds all configuration for gpu-mockctl
type Config struct {
	// Common flags
	Verbose bool

	// FS mode flags
	Base    string
	Machine string

	// Driver mode flags
	DriverRoot string
}

// NewDefault creates config with default values
func NewDefault() *Config {
	machine := "dgxa100"
	if v := os.Getenv("MACHINE_TYPE"); v != "" {
		machine = v
	}

	return &Config{
		Base:    "/run/nvidia/driver",
		Machine: machine,
	}
}

// Validate checks config for errors
func (c *Config) Validate() error {
	if c.Machine != "dgxa100" && os.Getenv("ALLOW_UNSUPPORTED") != "true" {
		return fmt.Errorf("unsupported machine type %q (set ALLOW_UNSUPPORTED=true to override)", c.Machine)
	}
	return nil
}

// ValidateFS validates configuration for FS mode
func (c *Config) ValidateFS() error {
	if c.Base == "" {
		return fmt.Errorf("base path cannot be empty")
	}
	return c.Validate()
}
