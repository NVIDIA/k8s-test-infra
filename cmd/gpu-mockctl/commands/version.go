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
	"context"
	"fmt"
	"runtime"

	"github.com/urfave/cli/v3"
)

// Version information set by build flags
var (
	// Version is the version string (can be set at build time)
	Version = "development"

	// GitCommit is the git commit hash (can be set at build time)
	GitCommit = "unknown"

	// BuildDate is the build timestamp (can be set at build time)
	BuildDate = "unknown"
)

// VersionInfo holds version information
type VersionInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	Compiler  string `json:"compiler"`
	Platform  string `json:"platform"`
}

// NewVersionCommand creates the 'version' subcommand
func NewVersionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print version information",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "short",
				Aliases: []string{"s"},
				Usage:   "print only the version number",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "output version information in JSON format",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return showVersion(cmd)
		},
	}
}

func showVersion(cmd *cli.Command) error {
	versionInfo := VersionInfo{
		Version:   Version,
		GitCommit: GitCommit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}

	// Short version - just print version string
	if cmd.Bool("short") {
		fmt.Println(versionInfo.Version)
		return nil
	}

	// JSON output
	if cmd.Bool("json") {
		return printVersionJSON(versionInfo)
	}

	// Default output - human readable
	fmt.Printf("gpu-mockctl version: %s\n", versionInfo.Version)
	fmt.Printf("  git commit: %s\n", versionInfo.GitCommit)
	fmt.Printf("  build date: %s\n", versionInfo.BuildDate)
	fmt.Printf("  go version: %s\n", versionInfo.GoVersion)
	fmt.Printf("  compiler:   %s\n", versionInfo.Compiler)
	fmt.Printf("  platform:   %s\n", versionInfo.Platform)

	return nil
}

func printVersionJSON(v VersionInfo) error {
	// Simple JSON output without external dependencies
	fmt.Printf(`{
  "version": %q,
  "gitCommit": %q,
  "buildDate": %q,
  "goVersion": %q,
  "compiler": %q,
  "platform": %q
}
`, v.Version, v.GitCommit, v.BuildDate, v.GoVersion, v.Compiler, v.Platform)
	return nil
}
