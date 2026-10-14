// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command imex-archives places the IMEX archives pinned in
// internal/agent/imex/imex.lock.json in a local directory, laid out as the
// build context of deployments/imex-payload/Dockerfile:
//
//	<dir>/<version>/<arch>/<archive, named as NVIDIA publishes it>
//
// It downloads only what the directory lacks or holds unverified, so a
// populated directory, such as a CI cache, needs no network. It prints the
// build context, <dir>/<version>.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/imex"
)

func main() {
	dir := flag.String("dir", "", "directory to keep the archives in (default: <user cache dir>/mokka/imex)")
	archs := flag.String("arch", runtime.GOARCH, "comma-separated architectures to place: amd64, arm64")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, &http.Client{Timeout: 5 * time.Minute}, imex.DefaultLock(), *dir, strings.Split(*archs, ","), os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "imex-archives:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, client *http.Client, lock imex.Lock, dir string, archs []string, stdout, stderr io.Writer) error {
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return fmt.Errorf("no -dir and no user cache directory: %w", err)
		}
		dir = filepath.Join(cache, "mokka", "imex")
	}

	buildContext := filepath.Join(dir, lock.Version)
	for _, arch := range archs {
		arch = strings.TrimSpace(arch)
		if arch == "" {
			return errors.New("-arch names no architecture")
		}
		archive, err := imex.FetchArchive(ctx, client, lock, arch, filepath.Join(buildContext, arch))
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "IMEX %s for %s: %s\n", lock.Version, arch, archive)
	}
	_, err := fmt.Fprintln(stdout, buildContext)
	return err
}
