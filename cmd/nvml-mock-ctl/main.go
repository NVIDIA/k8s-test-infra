// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

// nvml-mock-ctl mutates the nvml-mock runtime overlay so a running node's
// simulated GPU state can be changed without a Helm upgrade or pod restart.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

const (
	defaultOverlay = "/var/lib/nvml-mock/driver/config/overrides.yaml"
	defaultConfig  = "/var/lib/nvml-mock/driver/config/config.yaml"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// Writes to the CLI's stdout/stderr can't meaningfully fail-recover, so these
// helpers swallow the error (and satisfy errcheck, which only whitelists the
// literal os.Stdout/os.Stderr destinations, not io.Writer parameters).
func fprint(w io.Writer, a ...any)                 { _, _ = fmt.Fprint(w, a...) }
func fprintln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }
func fprintf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

func usage(w io.Writer) {
	fprint(w, `usage: nvml-mock-ctl <command> [flags]

commands:
  fail   --gpu <idx|all|uuid> --mode <healthy|lost|fallen_off_bus|ecc_uncorrectable> [--after-calls N] [--xid CODE]
  set    --gpu <idx|all|uuid> key.path=value [key.path=value ...]
  apply  --gpu <idx|all|uuid> -f patch.yaml
  status [--gpu <idx>]
  reset  [--gpu <idx|all|uuid>]

global flags:
  --file    overlay path (default $MOCK_NVML_OVERRIDES or `+defaultOverlay+`)
  --config  config path for UUID resolution/validation (default $MOCK_NVML_CONFIG or `+defaultConfig+`)
`)
}

func run(args []string, stdout, stderr io.Writer) int {
	var overlayPath, configPath, gpu, mode, patchFile string
	var afterCalls int
	var xid uint64
	fs := flag.NewFlagSet("nvml-mock-ctl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // usage printed explicitly below so it isn't duplicated
	fs.StringVar(&overlayPath, "file", envOr("MOCK_NVML_OVERRIDES", defaultOverlay), "overlay path")
	fs.StringVar(&configPath, "config", envOr("MOCK_NVML_CONFIG", defaultConfig), "config path")
	fs.StringVar(&gpu, "gpu", "", "target: index, 'all', or UUID")
	fs.StringVar(&mode, "mode", "", "failure mode (fail command)")
	fs.StringVar(&patchFile, "f", "", "patch file (apply command)")
	fs.IntVar(&afterCalls, "after-calls", 0, "trip after N guarded calls (fail)")
	fs.Uint64Var(&xid, "xid", 0, "Xid code to surface (fail)")

	// Global flags may appear before or after the subcommand, interspersed
	// with the command's positional key.path=value args. The stdlib flag
	// package stops at the first non-flag token, so we parse in a loop:
	// the first bare token is the command, the rest are positionals.
	var cmd string
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			if err == flag.ErrHelp {
				usage(stdout)
				return 0
			}
			usage(stderr)
			return 2
		}
		remaining := fs.Args()
		if len(remaining) == 0 {
			break
		}
		if cmd == "" {
			cmd = remaining[0]
		} else {
			positional = append(positional, remaining[0])
		}
		rest = remaining[1:]
	}

	if cmd == "" {
		usage(stderr)
		return 2
	}

	cfg := loadConfig(configPath) // best-effort; nil-safe downstream
	base := deviceDefaults(cfg)

	switch cmd {
	case "help":
		usage(stdout)
		return 0
	case "status":
		return doStatus(overlayPath, gpu, stdout, stderr)
	case "fail", "set", "apply", "reset":
		return mutate(cmd, overlayPath, gpu, mode, patchFile, afterCalls, xid, positional, cfg, base, stdout, stderr)
	default:
		fprintf(stderr, "unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

func mutate(cmd, overlayPath, gpu, mode, patchFile string, afterCalls int, xid uint64,
	positional []string, cfg *engine.Config, base *engine.DeviceConfig, stdout, stderr io.Writer) int {

	if gpu == "" && cmd != "reset" {
		fprintln(stderr, "--gpu is required")
		return 2
	}

	unlock, err := lockOverlay(overlayPath)
	if err != nil {
		fprintf(stderr, "lock: %v\n", err)
		return 1
	}
	defer unlock()

	doc, err := mockctl.Load(overlayPath)
	if err != nil {
		fprintf(stderr, "load: %v\n", err)
		return 1
	}

	var target mockctl.Target
	if gpu != "" {
		target, err = mockctl.ResolveTarget(gpu, cfg)
		if err != nil {
			fprintf(stderr, "%v\n", err)
			return 2
		}
	} else {
		target = mockctl.Target{All: true} // reset with no --gpu means everything
	}

	switch cmd {
	case "fail":
		if mode == "" {
			fprintln(stderr, "--mode is required for fail")
			return 2
		}
		if err := doc.Fail(target, mode, afterCalls, xid); err != nil {
			fprintf(stderr, "%v\n", err)
			return 2
		}
	case "set":
		if len(positional) == 0 {
			fprintln(stderr, "set requires at least one key.path=value")
			return 2
		}
		kv, err := mockctl.ParseSet(positional)
		if err != nil {
			fprintf(stderr, "%v\n", err)
			return 2
		}
		if err := mockctl.Validate(base, kv); err != nil {
			fprintf(stderr, "invalid: %v\n", err)
			return 2
		}
		doc.SetFields(target, kv)
	case "apply":
		if patchFile == "" {
			fprintln(stderr, "-f patch file is required for apply")
			return 2
		}
		data, err := os.ReadFile(patchFile)
		if err != nil {
			fprintf(stderr, "read patch: %v\n", err)
			return 1
		}
		patch, err := parseYAMLMap(data)
		if err != nil {
			fprintf(stderr, "parse patch: %v\n", err)
			return 2
		}
		if err := mockctl.Validate(base, patch); err != nil {
			fprintf(stderr, "invalid patch: %v\n", err)
			return 2
		}
		doc.SetFields(target, patch)
	case "reset":
		doc.Reset(target)
	}

	// Validate the resulting merged config for the affected bucket(s).
	if err := validateDoc(doc, base); err != nil {
		fprintf(stderr, "invalid overlay: %v\n", err)
		return 2
	}

	if err := writeAtomic(overlayPath, doc); err != nil {
		fprintf(stderr, "write: %v\n", err)
		return 1
	}
	fprintf(stdout, "ok: %s applied to %s\n", cmd, gpuLabel(gpu))
	return 0
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadConfig(path string) *engine.Config {
	yc, err := engine.LoadYAMLConfig(path)
	if err != nil {
		return nil
	}
	return &engine.Config{YAMLConfig: yc, NumDevices: len(yc.Devices), DriverVersion: yc.System.DriverVersion}
}

func deviceDefaults(cfg *engine.Config) *engine.DeviceConfig {
	if cfg == nil || cfg.YAMLConfig == nil {
		return &engine.DeviceConfig{}
	}
	dd := cfg.YAMLConfig.DeviceDefaults
	return &dd
}

func gpuLabel(g string) string {
	if g == "" {
		return "all"
	}
	return g
}

func doStatus(overlayPath, gpu string, stdout, stderr io.Writer) int {
	doc, err := mockctl.Load(overlayPath)
	if err != nil {
		fprintf(stderr, "load: %v\n", err)
		return 1
	}

	// A targeted status only reports a single device's bucket (plus the
	// shared "all" bucket). Only an integer index is supported here.
	if gpu != "" {
		idx, err := strconv.Atoi(gpu)
		if err != nil {
			fprintf(stderr, "status --gpu expects an integer index, got %q\n", gpu)
			return 2
		}
		dev := doc.Devices[strconv.Itoa(idx)]
		if dev == nil && doc.All == nil {
			fprintf(stdout, "no active overrides for gpu %d\n", idx)
			return 0
		}
		filtered := &mockctl.Doc{Version: doc.Version, All: doc.All}
		if dev != nil {
			filtered.Devices = map[string]map[string]any{strconv.Itoa(idx): dev}
		}
		b, err := filtered.Bytes()
		if err != nil {
			fprintf(stderr, "%v\n", err)
			return 1
		}
		fprint(stdout, string(b))
		return 0
	}

	if doc.All == nil && len(doc.Devices) == 0 {
		fprintln(stdout, "no active overrides")
		return 0
	}
	b, err := doc.Bytes()
	if err != nil {
		fprintf(stderr, "%v\n", err)
		return 1
	}
	fprint(stdout, string(b))
	return 0
}

// validateDoc runs MergeDeviceConfig for All and each per-device bucket so a
// bad value anywhere fails the command.
func validateDoc(doc *mockctl.Doc, base *engine.DeviceConfig) error {
	if doc.All != nil {
		if err := mockctl.Validate(base, doc.All); err != nil {
			return fmt.Errorf("all: %w", err)
		}
	}
	for idx, patch := range doc.Devices {
		if err := mockctl.Validate(base, patch); err != nil {
			return fmt.Errorf("device %s: %w", idx, err)
		}
	}
	return nil
}

func parseYAMLMap(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := yaml.UnmarshalStrict(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeAtomic writes the doc via a temp file + rename in the same directory so
// readers (and the bind-mounted view in consumer containers) never observe a
// partial file.
func writeAtomic(path string, doc *mockctl.Doc) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := doc.Bytes()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".overrides-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// os.CreateTemp makes the file 0600, but the published overlay is
	// bind-mounted into consumer containers and read by the mock library,
	// which may run as a non-root UID. Make it world-readable (matching how
	// config.yaml is consumed) so those reads don't silently fail.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// lockOverlay takes an exclusive flock on a sibling .lock file so concurrent
// kubectl exec invocations serialize their read-modify-write.
func lockOverlay(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX); err != nil {
		_ = lf.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(lf.Fd()), unix.LOCK_UN)
		_ = lf.Close()
	}, nil
}
