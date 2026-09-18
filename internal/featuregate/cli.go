// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package featuregate

import "github.com/urfave/cli/v3"

const (
	// FlagName is the shared command-line flag exposed by Mokka services.
	FlagName = "feature-gates"
	// EnvName is the shared environment variable exposed by Mokka services.
	EnvName = "MOKKA_FEATURE_GATES"
)

// CLIFlag returns the common CLI/environment feature gate control.
func CLIFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    FlagName,
		Sources: cli.EnvVars(EnvName),
		Usage:   "comma-separated feature gates to enable; prefix a gate with '-' to disable it",
	}
}

// ConfigureFromCLI applies the feature gate value parsed by urfave/cli.
func ConfigureFromCLI(cmd *cli.Command) error {
	return global.Configure(cmd.String(FlagName))
}
