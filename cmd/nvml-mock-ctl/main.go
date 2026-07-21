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

// nvml-mock-ctl mutates the nvml-mock runtime config override so a running node's
// simulated GPU state can be changed without a Helm upgrade or pod restart.
package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

const (
	defaultConfigOverride = "/var/lib/nvml-mock/driver/config/overrides.yaml"
	defaultConfig         = "/var/lib/nvml-mock/driver/config/config.yaml"

	// maxPowerWatts bounds the `power` command. Real GPUs top out around ~1.5kW;
	// this generous cap keeps watts*1000 far under the uint32 milliwatt ceiling
	// (~4.29e6 W) so the schema-field conversion can never overflow.
	maxPowerWatts = 100000
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
  temp   --gpu <idx|all|uuid> <celsius>    pin reported GPU temperature
  power  --gpu <idx|all|uuid> <watts>      pin reported power draw
  fan    --gpu <idx|all|uuid> <percent>    pin reported fan speed (forces fan count >= 1)
  util   --gpu <idx|all|uuid> <percent>    pin reported GPU + memory utilization
  clocks --gpu <idx|all|uuid> <mhz>        pin reported SM + graphics clocks
  throttle --gpu <idx|all|uuid> <reason>[ reason ...]  set active throttle reasons ('none' clears)
  pstate --gpu <idx|all|uuid> <0-15>       pin reported performance state (P-state)
  set    --gpu <idx|all|uuid> key.path=value [key.path=value ...]
  status [--gpu <idx>]
  reset  [--gpu <idx|all|uuid>]

global flags:
  --file    config override path (default $MOCK_NVML_OVERRIDES or `+defaultConfigOverride+`)
  --config  config path for UUID resolution/validation (default $MOCK_NVML_CONFIG or `+defaultConfig+`)
`)
}

func run(args []string, stdout, stderr io.Writer) int {
	var configOverridePath, configPath, gpu, mode string
	var afterCalls int
	var xid uint64
	fs := flag.NewFlagSet("nvml-mock-ctl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // usage printed explicitly below so it isn't duplicated
	fs.StringVar(&configOverridePath, "file", envOr("MOCK_NVML_OVERRIDES", defaultConfigOverride), "config override path")
	fs.StringVar(&configPath, "config", envOr("MOCK_NVML_CONFIG", defaultConfig), "config path")
	fs.StringVar(&gpu, "gpu", "", "target: index, 'all', or UUID")
	fs.StringVar(&mode, "mode", "", "failure mode (fail command)")
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

	cfg := loadConfig(configPath, stderr) // best-effort; nil-safe downstream
	base := deviceDefaults(cfg)

	switch cmd {
	case "help":
		usage(stdout)
		return 0
	case "status":
		return doStatus(configOverridePath, gpu, stdout, stderr)
	case "fail", "temp", "temperature", "power", "fan", "util", "utilization",
		"clocks", "throttle", "pstate", "set", "reset":
		return mutate(cmd, configOverridePath, gpu, mode, afterCalls, xid, positional, cfg, base, stdout, stderr)
	default:
		fprintf(stderr, "unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

func mutate(cmd, configOverridePath, gpu, mode string, afterCalls int, xid uint64,
	positional []string, cfg *engine.Config, base *engine.DeviceConfig, stdout, stderr io.Writer) int {

	if gpu == "" && cmd != "reset" {
		fprintln(stderr, "--gpu is required")
		return 2
	}

	unlock, err := lockConfigOverride(configOverridePath)
	if err != nil {
		fprintf(stderr, "lock: %v\n", err)
		return 1
	}
	defer unlock()

	doc, err := mockctl.Load(configOverridePath)
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
	case "temp", "temperature":
		celsius, perr := singleIntArg(positional, cmd, 0, 200)
		if perr != nil {
			fprintf(stderr, "%v\n", perr)
			return 2
		}
		if code := applyPatch(doc, target, base, mockctl.TemperaturePatch(celsius), stderr); code != 0 {
			return code
		}
	case "power":
		// Bound watts to a finite, non-negative range well under the uint32
		// milliwatt ceiling so watts*1000 can't overflow the schema field.
		watts, perr := singleFloatArg(positional, cmd, 0, maxPowerWatts)
		if perr != nil {
			fprintf(stderr, "%v\n", perr)
			return 2
		}
		// NVML power fields are milliwatts; the CLI takes the watts value
		// nvidia-smi displays and converts.
		mw := uint32(watts*1000 + 0.5)
		if code := applyPatch(doc, target, base, mockctl.PowerPatch(mw), stderr); code != 0 {
			return code
		}
	case "fan":
		pct, perr := singleIntArg(positional, cmd, 0, 100)
		if perr != nil {
			fprintf(stderr, "%v\n", perr)
			return 2
		}
		if code := applyPatch(doc, target, base, mockctl.FanPatch(pct, baseFanCount(base)), stderr); code != 0 {
			return code
		}
	case "util", "utilization":
		pct, perr := singleIntArg(positional, cmd, 0, 100)
		if perr != nil {
			fprintf(stderr, "%v\n", perr)
			return 2
		}
		if code := applyPatch(doc, target, base, mockctl.UtilizationPatch(pct), stderr); code != 0 {
			return code
		}
	case "clocks":
		mhz, perr := singleIntArg(positional, cmd, 0, 100000)
		if perr != nil {
			fprintf(stderr, "%v\n", perr)
			return 2
		}
		if code := applyPatch(doc, target, base, mockctl.ClocksPatch(mhz), stderr); code != 0 {
			return code
		}
	case "throttle":
		if len(positional) == 0 {
			fprintln(stderr, "throttle requires at least one reason (or 'none')")
			return 2
		}
		patch, perr := mockctl.ThrottlePatch(positional)
		if perr != nil {
			fprintf(stderr, "%v\n", perr)
			return 2
		}
		if code := applyPatch(doc, target, base, patch, stderr); code != 0 {
			return code
		}
	case "pstate":
		n, perr := singleIntArg(positional, cmd, 0, 15)
		if perr != nil {
			fprintf(stderr, "%v\n", perr)
			return 2
		}
		if code := applyPatch(doc, target, base, mockctl.PStatePatch(n), stderr); code != 0 {
			return code
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
	case "reset":
		doc.Reset(target)
	}

	// Validate the resulting merged config for the affected bucket(s).
	if err := validateDoc(doc, base); err != nil {
		fprintf(stderr, "invalid config override: %v\n", err)
		return 2
	}

	if err := writeAtomic(configOverridePath, doc); err != nil {
		fprintf(stderr, "write: %v\n", err)
		return 1
	}
	fprintf(stdout, "ok: %s applied to %s\n", cmd, gpuLabel(gpu))
	return 0
}

// applyPatch validates a convenience-command patch against the device schema
// and merges it into the target bucket, returning a non-zero exit code (already
// reported to stderr) on failure so callers can propagate it.
func applyPatch(doc *mockctl.Doc, target mockctl.Target, base *engine.DeviceConfig, patch map[string]any, stderr io.Writer) int {
	if err := mockctl.Validate(base, patch); err != nil {
		fprintf(stderr, "invalid: %v\n", err)
		return 2
	}
	doc.SetFields(target, patch)
	return 0
}

// singleIntArg parses the lone positional value of a convenience command as an
// integer within [lo, hi].
func singleIntArg(positional []string, name string, lo, hi int) (int, error) {
	if len(positional) != 1 {
		return 0, fmt.Errorf("%s requires exactly one value (got %d)", name, len(positional))
	}
	v, err := strconv.Atoi(positional[0])
	if err != nil {
		return 0, fmt.Errorf("%s value %q must be an integer", name, positional[0])
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("%s value %d out of range [%d,%d]", name, v, lo, hi)
	}
	return v, nil
}

// singleFloatArg parses the lone positional value of a convenience command as a
// finite floating-point number within [lo, hi]. NaN and +/-Inf are rejected:
// strconv.ParseFloat accepts them with a nil error, and they would otherwise
// slip past a bare comparison and overflow a downstream integer conversion.
func singleFloatArg(positional []string, name string, lo, hi float64) (float64, error) {
	if len(positional) != 1 {
		return 0, fmt.Errorf("%s requires exactly one value (got %d)", name, len(positional))
	}
	v, err := strconv.ParseFloat(positional[0], 64)
	if err != nil {
		return 0, fmt.Errorf("%s value %q must be a number", name, positional[0])
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s value %q must be finite", name, positional[0])
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("%s value %v out of range [%v,%v]", name, v, lo, hi)
	}
	return v, nil
}

// baseFanCount reports the fan count declared by the base config so the fan
// command can preserve a multi-fan count instead of collapsing it to 1.
func baseFanCount(base *engine.DeviceConfig) int {
	if base != nil && base.Fan != nil {
		return base.Fan.Count
	}
	return 0
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// loadConfig loads the pristine profile so `--gpu` can resolve UUIDs and the
// index bounds check knows the real device count. NumDevices is resolved the
// same way the engine's LoadConfig does: the device-list length, unless
// system.num_devices (which setup.sh injects from GPU_COUNT) overrides it — so
// the bounds check matches the count the running engine actually serves even
// when gpu.count differs from the profile's device list. A load failure is
// non-fatal (UUID resolution and the bounds check degrade to best-effort) but
// is surfaced as a warning instead of being swallowed silently.
func loadConfig(path string, stderr io.Writer) *engine.Config {
	yc, err := engine.LoadYAMLConfig(path)
	if err != nil {
		fprintf(stderr, "warning: could not load config %q: %v; UUID resolution and --gpu bounds checks are disabled\n", path, err)
		return nil
	}
	numDevices := len(yc.Devices)
	if numDevices == 0 {
		numDevices = 8 // engine default when no device list is present
	}
	if yc.System.NumDevices > 0 {
		numDevices = yc.System.NumDevices // system.num_devices wins, matching the engine
	}
	return &engine.Config{YAMLConfig: yc, NumDevices: numDevices, DriverVersion: yc.System.DriverVersion}
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

func doStatus(configOverridePath, gpu string, stdout, stderr io.Writer) int {
	doc, err := mockctl.Load(configOverridePath)
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
	// os.CreateTemp makes the file 0600, but the published config override is
	// bind-mounted into consumer containers and read by the mock library,
	// which may run as a non-root UID. Make it world-readable (matching how
	// config.yaml is consumed) so those reads don't silently fail.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// lockConfigOverride takes an exclusive flock on a sibling .lock file so concurrent
// kubectl exec invocations serialize their read-modify-write.
func lockConfigOverride(path string) (func(), error) {
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
