//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package runner is a context-aware exec wrapper used by every adapter
// (kind/helm/docker/kubectl). It captures stdout/stderr while teeing both to
// the Ginkgo writer, and returns a typed CmdError carrying the captured output
// so Gomega failure messages are self-explanatory (parity with the bash
// scripts' "echo $OUTPUT" on failure). All calls take a context so a spec
// timeout cancels the in-flight process instead of orphaning it.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/onsi/ginkgo/v2"
)

// Result is the captured outcome of a command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Combined returns stdout followed by stderr, approximating a shell `2>&1`
// merge for content assertions.
func (r Result) Combined() string { return r.Stdout + r.Stderr }

// CmdError is returned when a command exits non-zero or fails to start.
type CmdError struct {
	Cmd      string
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

func (e *CmdError) Error() string {
	return fmt.Sprintf("command %q failed (exit %d): %v\n--- stdout ---\n%s\n--- stderr ---\n%s",
		e.Cmd, e.ExitCode, e.Err, e.Stdout, e.Stderr)
}

// Unwrap exposes the underlying *exec error for errors.As checks.
func (e *CmdError) Unwrap() error { return e.Err }

// redactArgs returns a copy of args with any explicit `--kubeconfig <path>` flag
// dropped entirely, so command traces stay readable if a caller ever uses a
// kubeconfig override. Only the printed trace is affected — the real args
// passed to exec are untouched.
func redactArgs(args []string) []string {
	out := make([]string, 0, len(args))
	skip := false
	for _, a := range args {
		switch {
		case skip:
			skip = false
		case a == "--kubeconfig":
			skip = true
		case strings.HasPrefix(a, "--kubeconfig="):
		default:
			out = append(out, a)
		}
	}
	return out
}

// Run executes name+args under ctx, teeing stdout+stderr to the Ginkgo writer.
func Run(ctx context.Context, name string, args ...string) (Result, error) {
	return run(ctx, runOpts{}, name, args...)
}

// RunInDir is Run with the command's working directory set to dir (used for
// host-side builds that must run from the repo root, e.g. the stub kmod
// prebuild's `make -C /lib/modules/.../build M=$PWD/...`).
func RunInDir(ctx context.Context, dir, name string, args ...string) (Result, error) {
	return run(ctx, runOpts{dir: dir}, name, args...)
}

// RunQuiet is Run but does NOT stream stdout to the Ginkgo writer. The output
// is still captured (returned in Result and attached to CmdError on failure),
// so failures stay self-explanatory — this only suppresses the noisy live echo
// of high-frequency read-only bodies (e.g. `kubectl get -o json` polled inside
// Eventually loops), which otherwise flood `-v` output.
func RunQuiet(ctx context.Context, name string, args ...string) (Result, error) {
	return run(ctx, runOpts{maxStdoutLines: -1}, name, args...)
}

// RunTruncated is Run but streams only the first maxStdoutLines stdout lines to
// the Ginkgo writer. Full stdout is still captured in Result/CmdError.
func RunTruncated(ctx context.Context, maxStdoutLines int, name string, args ...string) (Result, error) {
	return run(ctx, runOpts{maxStdoutLines: maxStdoutLines}, name, args...)
}

// RunInput is Run with stdin piped from the given string (used for
// `kubectl apply -f -` and `kind create cluster --config /dev/stdin`).
func RunInput(ctx context.Context, stdin, name string, args ...string) (Result, error) {
	return run(ctx, runOpts{stdin: stdin}, name, args...)
}

// runOpts collects the optional knobs threaded into run.
type runOpts struct {
	stdin          string
	maxStdoutLines int
	dir            string
}

func run(ctx context.Context, opts runOpts, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if opts.stdin != "" {
		cmd.Stdin = strings.NewReader(opts.stdin)
	}
	if opts.dir != "" {
		cmd.Dir = opts.dir
	}
	var outBuf, errBuf bytes.Buffer
	switch {
	case opts.maxStdoutLines < 0:
		cmd.Stdout = &outBuf
	case opts.maxStdoutLines > 0:
		cmd.Stdout = io.MultiWriter(&outBuf, &lineLimitWriter{dst: ginkgo.GinkgoWriter, remaining: opts.maxStdoutLines})
	default:
		cmd.Stdout = io.MultiWriter(&outBuf, ginkgo.GinkgoWriter)
	}
	cmd.Stderr = io.MultiWriter(&errBuf, ginkgo.GinkgoWriter)

	full := name + " " + strings.Join(args, " ")
	_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "+ %s\n", name+" "+strings.Join(redactArgs(args), " "))

	err := cmd.Run()
	res := Result{Stdout: outBuf.String(), Stderr: errBuf.String()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		return res, &CmdError{Cmd: full, Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode, Err: err}
	}
	return res, nil
}

type lineLimitWriter struct {
	dst       io.Writer
	remaining int
}

func (w *lineLimitWriter) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 && w.remaining > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			_, err := w.dst.Write(p)
			return written, err
		}
		if _, err := w.dst.Write(p[:i+1]); err != nil {
			return written, err
		}
		w.remaining--
		p = p[i+1:]
	}
	return written, nil
}
