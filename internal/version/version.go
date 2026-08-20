// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package version carries build-time-injected identity strings. The vars
// below are overridden at link time via -ldflags -X (see LDFLAGS_COMMON in
// the Makefile); their defaults are what `go run` / `go test` will see.
package version

import (
	"fmt"
	"runtime"
)

// Version is the release identifier (e.g. `v0.1.0-3-gabc123`). Defaults to
// `dev` under `go run` / `go test`.
var Version = "dev"

// GitCommit is `git describe --dirty` output for the built commit.
var GitCommit = "unknown"

// BuildDate is the ISO-8601 UTC timestamp of the build.
var BuildDate = "unknown"

// FullVersion is the human-readable summary CLIs emit on --version.
var FullVersion string

func init() {
	FullVersion = fmt.Sprintf(
		"%s (commit: %s, built at: %s, runtime: %s)",
		Version, GitCommit, BuildDate, runtime.Version(),
	)
}
