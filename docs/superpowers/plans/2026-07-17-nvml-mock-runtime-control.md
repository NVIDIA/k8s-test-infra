# nvml-mock Runtime Control (`nvml-mock-ctl`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add runtime control of nvml-mock GPU state via a `nvml-mock-ctl` CLI that writes a node-local overlay file the mock library re-reads on a short TTL, without restarting consumers, resetting to pristine YAML on DaemonSet pod restart.

**Architecture:** `nvml-mock-ctl` (run via `kubectl exec` in the nvml-mock DaemonSet pod) atomically writes an `overrides.yaml` sibling to `config.yaml`. The engine deep-merges that overlay over each device's pristine base config and re-reads it on a ~1s TTL (mirroring `fabric_readiness.go`). Each `ConfigurableDevice` holds its effective config and failure injector behind `atomic.Pointer`s and refreshes lazily when the overlay's generation changes, so both already-running and new consumer processes converge within one TTL.

**Tech Stack:** Go 1.26 (CGo shared library), `sigs.k8s.io/yaml` (already vendored, JSON-tag based), `encoding/json` for deep-merge, `golang.org/x/sys/unix` for `flock` (already vendored), Helm, Docker, Ginkgo/Gomega for E2E.

## Global Constraints

- Go module: `github.com/NVIDIA/k8s-test-infra`, Go 1.26; build uses `-mod=vendor`. **Do not add new external dependencies** — use only stdlib and already-vendored packages (`sigs.k8s.io/yaml`, `golang.org/x/sys/unix`).
- Every source file starts with the existing NVIDIA Apache-2.0 license header (copy the header block verbatim from a neighboring file in the same directory; note `.go` engine files use the `// Copyright (c) 2026, NVIDIA CORPORATION.` form and `cmd/`/test files use the SPDX form — match the sibling files).
- Every git commit MUST be signed off: `git commit -s`.
- Hot NVML getters must not `stat()`/read the filesystem per call — all overlay filesystem access goes through a TTL-gated cache (default 1s), exactly like `pkg/gpu/mocknvml/engine/fabric_readiness.go`.
- The pristine `config.yaml` is never mutated; overrides live only in `overrides.yaml`.
- Failure modes and their string constants already exist in `pkg/gpu/mocknvml/engine/config_types.go`: `FailureModeHealthy` = `"healthy"`, `FailureModeLost` = `"lost"`, `FailureModeFallenOffBus` = `"fallen_off_bus"`, `FailureModeECCUncorrectable` = `"ecc_uncorrectable"`.
- Overlay path resolution order (used by both the engine and `nvml-mock-ctl`): (1) `MOCK_NVML_OVERRIDES` env var; (2) sibling of the resolved config path (`<dir(config.yaml)>/overrides.yaml`).
- Overlay TTL env var: `MOCK_NVML_OVERLAY_TTL` (Go duration string, default `1s`).
- Run tests with: `go test -race $(go list ./... | grep -v vendor)`. Lint with `golangci-lint run -v --timeout 5m`. Helm with `helm unittest deployments/nvml-mock/helm/nvml-mock`.

## Scope note: what runtime override covers

The engine's `ConfigurableDevice` getters read config-derived values through the device's config pointer (thermal, power, utilization, ECC, clocks, fan, failure, etc.). These are covered by hot-reload. A small set of identity/topology values are baked onto the embedded `dgxa100.Device` **once** at construction (device `Name`, `Architecture`, `Brand`, `ComputeCapability`, UUID, PCI bus id) and are **not** hot-reloadable in v1. This is expected and must be documented (Task 9). The motivating use cases (failure modes, ECC, temperature, power, utilization) are all covered.

---

## File Structure

**New files:**
- `pkg/gpu/mocknvml/engine/overlay_merge.go` — pure deep-merge + overlay-document types & typed-validation helpers.
- `pkg/gpu/mocknvml/engine/overlay_merge_test.go`
- `pkg/gpu/mocknvml/engine/overlay_store.go` — TTL-gated overlay file loader with a generation counter (models `fabric_readiness.go`).
- `pkg/gpu/mocknvml/engine/overlay_store_test.go`
- `pkg/gpu/mocknvml/engine/overlay_refresh_test.go` — device-level refresh behavior tests.
- `pkg/gpu/mockctl/overlay.go` — shared CLI logic: load/mutate/save an overlay doc, path resolution, UUID→index resolution, validation. (Kept out of `engine` so the CLI doesn't pull the CGo build tags.)
- `pkg/gpu/mockctl/overlay_test.go`
- `cmd/nvml-mock-ctl/main.go` — CLI entrypoint (subcommand dispatch, file locking, atomic write).
- `cmd/nvml-mock-ctl/main_test.go`
- `docs/nvml-mock-ctl.md` — user documentation.
- `tests/e2e/go/scenario_runtime_control.go` — E2E using `nvml-mock-ctl`.

**Modified files:**
- `pkg/gpu/mocknvml/engine/device.go` — `config`/`failure` become `atomic.Pointer`s; add `baseConfig`, `cfg()`, `failureInjector()`, `refresh()`; replace `d.config`→`d.cfg()`, `d.failure`→`d.failureInjector()`.
- `pkg/gpu/mocknvml/engine/failure_injector.go` — add `Reset()` for ctl-driven recovery; relax the "sticky forever" contract to "sticky until replaced/reset".
- `pkg/gpu/mocknvml/engine/engine.go` — refresh all devices in `PendingXidEvent`; add `ResetForTesting` hook for overlay store.
- `pkg/gpu/mocknvml/engine/config.go` — export a helper to resolve the overlay path from the config path.
- `deployments/nvml-mock/Dockerfile` — build + install `nvml-mock-ctl`.
- `deployments/nvml-mock/scripts/setup.sh` — clear `overrides.yaml` on startup; bind-mount the config **directory** (not just the file) via CDI; inject `MOCK_NVML_OVERRIDES` env.
- `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml` — set `MOCK_NVML_OVERRIDES` in the DaemonSet container so `nvml-mock-ctl` writes to the host mount path.
- `pkg/gpu/mocknvml/README.md` — cross-link the new doc.
- `tests/e2e/go/*_test.go` — register the new scenario (follow how `scenario_failure_injection.go` is wired).

---

## Task 1: Overlay document types + deep-merge helper

**Files:**
- Create: `pkg/gpu/mocknvml/engine/overlay_merge.go`
- Test: `pkg/gpu/mocknvml/engine/overlay_merge_test.go`

**Interfaces:**
- Produces:
  - `type OverlayDoc struct { Version int; All map[string]any; Devices map[string]map[string]any }` (YAML/JSON tags `version`, `all`, `devices`).
  - `func ParseOverlay(data []byte) (*OverlayDoc, error)` — strict parse; returns `(nil, nil)` for empty input.
  - `func (o *OverlayDoc) DeviceOverlay(index int) map[string]any` — deep-merge of `All` then `Devices[strconv.Itoa(index)]`; returns nil when neither present.
  - `func MergeDeviceConfig(base *DeviceConfig, patch map[string]any) (*DeviceConfig, error)` — deep-merge `patch` over `base`'s JSON representation, unmarshal into a new `*DeviceConfig` (strict, unknown fields rejected). When `patch` is nil/empty, returns a deep copy of `base`.
  - `func deepMergeMaps(dst, src map[string]any)` — recursive map merge (src wins; nested maps merged, scalars/slices replaced).

- [ ] **Step 1: Write failing tests**

```go
// pkg/gpu/mocknvml/engine/overlay_merge_test.go
package engine

import "testing"

func TestDeepMergeMaps_NestedOverrideAndPreserve(t *testing.T) {
	dst := map[string]any{"ecc": map[string]any{"mode_current": "enabled", "default_mode": "enabled"}}
	src := map[string]any{"ecc": map[string]any{"mode_current": "disabled"}}
	deepMergeMaps(dst, src)
	ecc := dst["ecc"].(map[string]any)
	if ecc["mode_current"] != "disabled" {
		t.Fatalf("mode_current not overridden: %v", ecc["mode_current"])
	}
	if ecc["default_mode"] != "enabled" {
		t.Fatalf("default_mode should be preserved: %v", ecc["default_mode"])
	}
}

func TestDeviceOverlay_AllThenPerIndexPrecedence(t *testing.T) {
	o := &OverlayDoc{
		All:     map[string]any{"failure": map[string]any{"mode": "lost"}},
		Devices: map[string]map[string]any{"0": {"failure": map[string]any{"mode": "ecc_uncorrectable"}}},
	}
	if got := o.DeviceOverlay(1)["failure"].(map[string]any)["mode"]; got != "lost" {
		t.Fatalf("device 1 should inherit All: %v", got)
	}
	if got := o.DeviceOverlay(0)["failure"].(map[string]any)["mode"]; got != "ecc_uncorrectable" {
		t.Fatalf("device 0 per-index should win: %v", got)
	}
}

func TestMergeDeviceConfig_AppliesFailureMode(t *testing.T) {
	base := &DeviceConfig{}
	patch := map[string]any{"failure": map[string]any{"mode": "ecc_uncorrectable", "after_calls": 1}}
	merged, err := MergeDeviceConfig(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Failure == nil || merged.Failure.Mode != "ecc_uncorrectable" {
		t.Fatalf("failure mode not applied: %+v", merged.Failure)
	}
	if base.Failure != nil {
		t.Fatal("base must not be mutated")
	}
}

func TestMergeDeviceConfig_RejectsUnknownField(t *testing.T) {
	if _, err := MergeDeviceConfig(&DeviceConfig{}, map[string]any{"not_a_field": 1}); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestParseOverlay_Empty(t *testing.T) {
	o, err := ParseOverlay(nil)
	if err != nil || o != nil {
		t.Fatalf("empty overlay should be (nil,nil): %v %v", o, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/gpu/mocknvml/engine/ -run 'Overlay|DeepMerge|MergeDeviceConfig' -v`
Expected: FAIL (undefined: `deepMergeMaps`, `OverlayDoc`, `MergeDeviceConfig`, `ParseOverlay`).

- [ ] **Step 3: Implement `overlay_merge.go`**

```go
// pkg/gpu/mocknvml/engine/overlay_merge.go
package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"sigs.k8s.io/yaml"
)

// OverlayDoc is the runtime override document written by nvml-mock-ctl and
// read by the engine. It is intentionally schema-light: All and per-device
// patches are generic maps deep-merged over the pristine DeviceConfig, so any
// config field is controllable without per-field plumbing.
type OverlayDoc struct {
	Version int                       `json:"version,omitempty"`
	All     map[string]any            `json:"all,omitempty"`
	Devices map[string]map[string]any `json:"devices,omitempty"`
}

// ParseOverlay strictly parses overlay bytes. Empty/whitespace input returns
// (nil, nil) so an absent or empty overlay is treated as "no overrides".
func ParseOverlay(data []byte) (*OverlayDoc, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc OverlayDoc
	if err := yaml.UnmarshalStrict(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing overlay: %w", err)
	}
	return &doc, nil
}

// DeviceOverlay returns the deep-merged patch for a device index: All first,
// then the per-index entry (which wins). Returns nil when neither is present.
func (o *OverlayDoc) DeviceOverlay(index int) map[string]any {
	if o == nil {
		return nil
	}
	out := map[string]any{}
	if o.All != nil {
		deepMergeMaps(out, deepCopyMap(o.All))
	}
	if per, ok := o.Devices[strconv.Itoa(index)]; ok {
		deepMergeMaps(out, deepCopyMap(per))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeDeviceConfig deep-merges patch over a JSON view of base and unmarshals
// the result into a new *DeviceConfig. Unknown fields are rejected so typos in
// overlays fail loudly. base is never mutated.
func MergeDeviceConfig(base *DeviceConfig, patch map[string]any) (*DeviceConfig, error) {
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshaling base config: %w", err)
	}
	baseMap := map[string]any{}
	if err := json.Unmarshal(baseJSON, &baseMap); err != nil {
		return nil, fmt.Errorf("unmarshaling base config to map: %w", err)
	}
	if len(patch) > 0 {
		deepMergeMaps(baseMap, patch)
	}
	mergedJSON, err := json.Marshal(baseMap)
	if err != nil {
		return nil, fmt.Errorf("marshaling merged config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(mergedJSON))
	dec.DisallowUnknownFields()
	var out DeviceConfig
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding merged config: %w", err)
	}
	return &out, nil
}

// deepMergeMaps recursively merges src into dst. Nested maps are merged; all
// other values (scalars, slices) replace dst wholesale.
func deepMergeMaps(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMergeMaps(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if vm, ok := v.(map[string]any); ok {
			out[k] = deepCopyMap(vm)
			continue
		}
		out[k] = v
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/gpu/mocknvml/engine/ -run 'Overlay|DeepMerge|MergeDeviceConfig' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/gpu/mocknvml/engine/overlay_merge.go pkg/gpu/mocknvml/engine/overlay_merge_test.go
git commit -s -m "feat(nvml-mock): add overlay document parsing and deep-merge"
```

---

## Task 2: TTL-gated overlay store with generation counter

**Files:**
- Create: `pkg/gpu/mocknvml/engine/overlay_store.go`
- Modify: `pkg/gpu/mocknvml/engine/config.go` (add `OverlayPathFor`)
- Test: `pkg/gpu/mocknvml/engine/overlay_store_test.go`

**Interfaces:**
- Consumes: `ParseOverlay`, `OverlayDoc` (Task 1).
- Produces:
  - `func OverlayPathFor(configPath string) string` (in `config.go`): `MOCK_NVML_OVERRIDES` if set, else `filepath.Join(filepath.Dir(configPath), "overrides.yaml")`; returns `""` when both are empty.
  - `type overlayStore struct { ... }` with `func (s *overlayStore) snapshot() (gen uint64, doc *OverlayDoc)`.
  - Package var `overlays = newOverlayStore()` and `func resetOverlayStoreForTesting()`.
  - `overlayTTL()` reading `MOCK_NVML_OVERLAY_TTL` (default `time.Second`).

The store stat()s the overlay file at most once per TTL. When the file's mtime+size changes (or it appears/disappears), it re-reads+re-parses and increments `gen`. `gen` starts at 0 ("nothing observed"); the first observed non-empty overlay makes it ≥1.

- [ ] **Step 1: Write failing tests**

```go
// pkg/gpu/mocknvml/engine/overlay_store_test.go
package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOverlayPathFor_SiblingDefault(t *testing.T) {
	t.Setenv("MOCK_NVML_OVERRIDES", "")
	got := OverlayPathFor("/x/config/config.yaml")
	if got != "/x/config/overrides.yaml" {
		t.Fatalf("got %q", got)
	}
}

func TestOverlayPathFor_EnvWins(t *testing.T) {
	t.Setenv("MOCK_NVML_OVERRIDES", "/custom/o.yaml")
	if got := OverlayPathFor("/x/config/config.yaml"); got != "/custom/o.yaml" {
		t.Fatalf("got %q", got)
	}
}

func TestOverlayStore_GenBumpsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	now := time.Unix(0, 0)
	s := newOverlayStoreAt(func() string { return path }, func() time.Time { return now })

	// Absent file: gen 0, nil doc.
	if gen, doc := s.snapshot(); gen != 0 || doc != nil {
		t.Fatalf("absent overlay: gen=%d doc=%v", gen, doc)
	}

	// Write a file; TTL not elapsed yet -> still cached as absent.
	if err := os.WriteFile(path, []byte("all:\n  failure:\n    mode: lost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if gen, _ := s.snapshot(); gen != 0 {
		t.Fatalf("within TTL gen should stay 0, got %d", gen)
	}

	// Advance beyond TTL -> re-read, gen bumps, doc parsed.
	now = now.Add(2 * time.Second)
	gen, doc := s.snapshot()
	if gen != 1 || doc == nil {
		t.Fatalf("after change: gen=%d doc=%v", gen, doc)
	}
	if doc.All["failure"].(map[string]any)["mode"] != "lost" {
		t.Fatalf("parsed wrong: %+v", doc.All)
	}

	// No change -> gen stable across TTL windows.
	now = now.Add(2 * time.Second)
	if gen2, _ := s.snapshot(); gen2 != 1 {
		t.Fatalf("unchanged file should keep gen=1, got %d", gen2)
	}

	// Remove file -> gen bumps again, doc nil.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if gen3, doc3 := s.snapshot(); gen3 != 2 || doc3 != nil {
		t.Fatalf("after removal: gen=%d doc=%v", gen3, doc3)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/gpu/mocknvml/engine/ -run Overlay -v`
Expected: FAIL (undefined: `OverlayPathFor`, `newOverlayStoreAt`).

- [ ] **Step 3: Add `OverlayPathFor` to `config.go`**

Add near the other path helpers in `pkg/gpu/mocknvml/engine/config.go`:

```go
// OverlayPathFor resolves the runtime overrides file path from the resolved
// config path. MOCK_NVML_OVERRIDES wins; otherwise overrides.yaml sits next to
// config.yaml. Returns "" when no config path is known.
func OverlayPathFor(configPath string) string {
	if p := os.Getenv("MOCK_NVML_OVERRIDES"); p != "" {
		return p
	}
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "overrides.yaml")
}
```

(`os` and `path/filepath` are already imported in `config.go`.)

- [ ] **Step 4: Implement `overlay_store.go`**

```go
// pkg/gpu/mocknvml/engine/overlay_store.go
package engine

import (
	"os"
	"sync"
	"time"
)

const defaultOverlayTTL = time.Second

func overlayTTL() time.Duration {
	if v := os.Getenv("MOCK_NVML_OVERLAY_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultOverlayTTL
}

// overlayStore reads the overrides file at most once per TTL and exposes a
// monotonic generation that bumps whenever the file's observable state
// (absent / mtime / size) changes. Devices compare this generation to decide
// when to recompute their effective config, keeping the hot path allocation-
// and IO-free between changes. Modeled on fabricReadinessCache.
type overlayStore struct {
	mu       sync.Mutex
	checked  time.Time
	gen      uint64
	doc      *OverlayDoc
	lastMod  time.Time
	lastSize int64
	present  bool

	now      func() time.Time
	pathFn   func() string
	ttl      time.Duration
}

func newOverlayStore() *overlayStore {
	return newOverlayStoreAt(resolveOverlayPath, time.Now)
}

func newOverlayStoreAt(pathFn func() string, now func() time.Time) *overlayStore {
	return &overlayStore{now: now, pathFn: pathFn, ttl: overlayTTL()}
}

// resolveOverlayPath derives the overlay path from the same resolution the
// engine uses for config. It is cheap and only called on cache misses.
func resolveOverlayPath() string {
	configPath := os.Getenv("MOCK_NVML_CONFIG")
	if configPath == "" {
		configPath = discoverConfigPath()
	}
	return OverlayPathFor(configPath)
}

func (s *overlayStore) snapshot() (uint64, *OverlayDoc) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if !s.checked.IsZero() && now.Sub(s.checked) < s.ttl {
		return s.gen, s.doc
	}
	s.checked = now

	path := s.pathFn()
	if path == "" {
		s.transition(false, time.Time{}, 0, nil)
		return s.gen, s.doc
	}

	fi, err := os.Stat(path)
	if err != nil {
		s.transition(false, time.Time{}, 0, nil)
		return s.gen, s.doc
	}

	// Unchanged file: no re-parse, no gen bump.
	if s.present && fi.ModTime().Equal(s.lastMod) && fi.Size() == s.lastSize {
		return s.gen, s.doc
	}

	data, err := os.ReadFile(path)
	if err != nil {
		s.transition(false, time.Time{}, 0, nil)
		return s.gen, s.doc
	}
	doc, err := ParseOverlay(data)
	if err != nil {
		warnLog("Failed to parse overrides %s: %v\n", path, err)
		// Keep the last good doc but do not bump gen on parse errors.
		return s.gen, s.doc
	}
	s.transition(true, fi.ModTime(), fi.Size(), doc)
	return s.gen, s.doc
}

// transition records new observed state and bumps gen when the effective
// content changed (presence flip or new mtime/size while present).
func (s *overlayStore) transition(present bool, mod time.Time, size int64, doc *OverlayDoc) {
	changed := present != s.present
	if present && s.present && (!mod.Equal(s.lastMod) || size != s.lastSize) {
		changed = true
	}
	s.present = present
	s.lastMod = mod
	s.lastSize = size
	s.doc = doc
	if changed {
		s.gen++
	}
}

var overlays = newOverlayStore()

func resetOverlayStoreForTesting() {
	overlays = newOverlayStore()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/gpu/mocknvml/engine/ -run Overlay -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/gpu/mocknvml/engine/overlay_store.go pkg/gpu/mocknvml/engine/config.go pkg/gpu/mocknvml/engine/overlay_store_test.go
git commit -s -m "feat(nvml-mock): add TTL-gated overlay store with generation counter"
```

---

## Task 3: Failure injector reset support

**Files:**
- Modify: `pkg/gpu/mocknvml/engine/failure_injector.go`
- Test: `pkg/gpu/mocknvml/engine/failure_injector_test.go` (add cases; create the file if it does not exist, matching the engine test header style)

**Interfaces:**
- Produces: `func (f *failureInjector) Reset()` — clears `tripped`, `callCount`, and `xidDelivered` so a device can recover to healthy behavior when the overlay clears a failure. Safe on a nil receiver.

Rationale: today the injector is "sticky forever". Runtime control needs `nvml-mock-ctl reset` / `fail --mode healthy` to recover a device. We keep the injector sticky **within a mode**, but allow the refresh path to either replace the injector (mode change) or reset it (back to healthy). Update the struct doc comment to say "sticky until replaced or Reset()".

- [ ] **Step 1: Write failing test**

```go
// add to pkg/gpu/mocknvml/engine/failure_injector_test.go
func TestFailureInjector_ResetRecoversHealthy(t *testing.T) {
	f := newFailureInjector(&FailureInjectionConfig{Mode: FailureModeLost})
	if f == nil {
		t.Fatal("expected injector")
	}
	if !f.Tick() || !f.IsLost() {
		t.Fatal("expected tripped lost device")
	}
	f.Reset()
	if f.Triggered() || f.IsLost() {
		t.Fatalf("Reset should clear tripped state")
	}
	if f.CallCount() != 0 {
		t.Fatalf("Reset should zero call count, got %d", f.CallCount())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/gpu/mocknvml/engine/ -run TestFailureInjector_ResetRecoversHealthy -v`
Expected: FAIL (`f.Reset undefined`).

- [ ] **Step 3: Implement `Reset`**

Add to `pkg/gpu/mocknvml/engine/failure_injector.go`:

```go
// Reset returns the injector to its untripped state. It is used by runtime
// control (nvml-mock-ctl reset / mode healthy) to recover a device without a
// process restart. Callers that want a genuinely healthy device should drop
// the injector entirely (set it to nil); Reset exists for the case where the
// same injector object is reused. Safe on a nil receiver.
func (f *failureInjector) Reset() {
	if f == nil {
		return
	}
	f.tripped.Store(false)
	f.xidDelivered.Store(false)
	f.callCount.Store(0)
}
```

Also update the struct doc comment (lines ~26-31) from "sticky: once a device trips ... it stays failed for the lifetime of the engine" to note it is "sticky until the injector is replaced or Reset() is called (runtime control)".

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/gpu/mocknvml/engine/ -run TestFailureInjector_ResetRecoversHealthy -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/gpu/mocknvml/engine/failure_injector.go pkg/gpu/mocknvml/engine/failure_injector_test.go
git commit -s -m "feat(nvml-mock): add failure injector Reset for runtime recovery"
```

---

## Task 4: Device atomic config/failure pointers + lazy refresh

This task converts `ConfigurableDevice` to hold its effective config and failure injector behind `atomic.Pointer`s and recompute them when the overlay generation changes. It includes the mechanical `d.config`→`d.cfg()` / `d.failure`→`d.failureInjector()` replacement.

**Files:**
- Modify: `pkg/gpu/mocknvml/engine/device.go`
- Modify: `pkg/gpu/mocknvml/engine/engine.go` (`PendingXidEvent` refresh; `ResetForTesting` overlay reset)
- Test: `pkg/gpu/mocknvml/engine/overlay_refresh_test.go`

**Interfaces:**
- Consumes: `overlays.snapshot()` (Task 2), `MergeDeviceConfig`, `OverlayDoc.DeviceOverlay` (Task 1), `failureInjector.Reset` (Task 3).
- Produces on `*ConfigurableDevice`:
  - `func (d *ConfigurableDevice) cfg() *DeviceConfig` — returns the current effective config, refreshing first.
  - `func (d *ConfigurableDevice) failureInjector() *failureInjector` — returns the current injector, refreshing first.
  - `func (d *ConfigurableDevice) refresh()` — recompute effective config + injector when `overlays` generation changed since last applied.

- [ ] **Step 1: Change the struct fields**

In `pkg/gpu/mocknvml/engine/device.go`, replace the `config` and `failure` fields (lines ~42 and ~61) and add refresh bookkeeping. New struct shape:

```go
type ConfigurableDevice struct {
	*dgxa100.Device
	fabric      *NodeFabric
	index       int
	minorNumber int

	// baseConfig is the pristine, merged (defaults+per-device) YAML config.
	// It is immutable after construction and is the base every overlay merges
	// over. May be nil in legacy/default mode.
	baseConfig *DeviceConfig

	// effective holds the current DeviceConfig (baseConfig + overlay). Swapped
	// atomically on refresh so concurrent getters never see a torn value.
	effective atomic.Pointer[DeviceConfig]

	// Cached computed values
	bar1Memory nvml.BAR1Memory
	pciInfo    nvml.PciInfo

	// Mutable in-memory state (not persisted across restarts)
	persistenceModeOverride *nvml.EnableState

	dynamicMetrics *dynamicMetricsSimulator

	// failure holds the current injector (nil == healthy). Swapped atomically
	// on refresh. Read via failureInjector().
	failure atomic.Pointer[failureInjector]

	// refresh bookkeeping
	refreshMu  sync.Mutex
	appliedGen uint64
}
```

Add `"sync"` and `"sync/atomic"` to the imports in `device.go`.

- [ ] **Step 2: Update the constructor**

In `NewConfigurableDevice` (lines ~69-133), stop assigning `config:`/`failure:` as plain fields. Instead set `baseConfig`, initialize `effective` to a copy of the base, and initialize the injector. Replace the constructor body's field init and the two `dev.dynamicMetrics`/`dev.failure` blocks with:

```go
	dev := &ConfigurableDevice{
		Device:      baseDevice,
		fabric:      fabric,
		index:       index,
		minorNumber: minorNumber,
		baseConfig:  config,
	}

	// ... keep the existing "Override base device properties from config",
	// UUID, PCI, minor, initBAR1Memory, initPciInfo blocks unchanged, but
	// every read of `config` there stays as the local parameter `config`
	// (they run once at construction and intentionally use the base). ...

	if config != nil && config.DynamicMetrics != nil {
		dev.dynamicMetrics = newDynamicMetricsSimulator(config.DynamicMetrics)
	}

	// Seed effective config + injector from the base. appliedGen stays 0 so
	// the first overlay generation observed triggers a refresh.
	initial := config
	if initial == nil {
		initial = &DeviceConfig{}
	}
	dev.effective.Store(initial)
	if config != nil && config.Failure != nil {
		dev.failure.Store(newFailureInjector(config.Failure))
	}
```

Note: `newFailureInjector` returns nil for healthy; storing a typed nil into `atomic.Pointer[failureInjector]` is fine and `failureInjector()` returns it as nil.

- [ ] **Step 3: Add accessors + refresh**

Add to `device.go`:

```go
// cfg returns the current effective device config, applying any pending
// overlay refresh first. Never returns nil (returns an empty config when the
// device was built without YAML, matching the previous nil-config getters that
// checked for nil).
func (d *ConfigurableDevice) cfg() *DeviceConfig {
	d.refresh()
	return d.effective.Load()
}

// failureInjector returns the current injector (nil == healthy) after applying
// any pending overlay refresh.
func (d *ConfigurableDevice) failureInjector() *failureInjector {
	d.refresh()
	return d.failure.Load()
}

// refresh recomputes the effective config and failure injector when the
// overlay generation has advanced since this device last applied it. The
// generation check is a cheap atomic compare on the hot path; the merge only
// runs when overrides actually changed.
func (d *ConfigurableDevice) refresh() {
	gen, doc := overlays.snapshot()
	if atomic.LoadUint64(&d.appliedGen) == gen {
		return
	}
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()
	if d.appliedGen == gen {
		return
	}

	base := d.baseConfig
	if base == nil {
		base = &DeviceConfig{}
	}
	patch := doc.DeviceOverlay(d.index)
	merged, err := MergeDeviceConfig(base, patch)
	if err != nil {
		warnLog("[OVERLAY] device %d: %v (keeping previous config)\n", d.index, err)
		atomic.StoreUint64(&d.appliedGen, gen) // avoid hot re-merge on a bad doc
		return
	}
	d.effective.Store(merged)
	d.reconcileFailure(merged.Failure)
	atomic.StoreUint64(&d.appliedGen, gen)
}

// reconcileFailure aligns the injector with the effective failure config.
// Unchanged mode keeps the existing injector (preserving accumulated ECC
// counters); a changed mode installs a fresh injector; healthy clears it.
func (d *ConfigurableDevice) reconcileFailure(cfg *FailureInjectionConfig) {
	cur := d.failure.Load()
	newMode := FailureModeHealthy
	if cfg != nil {
		newMode = normalizedMode(cfg.Mode)
	}
	if cur.Mode() == newMode {
		if newMode == FailureModeHealthy {
			return
		}
		// Same non-healthy mode: keep accumulated state.
		return
	}
	if newMode == FailureModeHealthy {
		d.failure.Store(nil)
		return
	}
	d.failure.Store(newFailureInjector(cfg))
}
```

(`cur.Mode()` is nil-safe — see `failure_injector.go`.)

- [ ] **Step 4: Mechanical replacement of field reads**

Replace every remaining `d.config` read with `d.cfg()` and every `d.failure` read with `d.failureInjector()` in `device.go`. There are ~141 `d.config` occurrences and 8 `d.failure` occurrences.

Do it with the editor's replace, then verify no stale references remain:

Run:
```bash
cd /Users/gcalzolari/git/opensource/k8s-test-infra
rg -n 'd\.config([^(]|$)' pkg/gpu/mocknvml/engine/device.go || echo "no d.config field reads remain"
rg -n 'd\.failure([^I(]|$)' pkg/gpu/mocknvml/engine/device.go || echo "no d.failure field reads remain"
```
Expected: both print the "no ... remain" message. (The regex excludes `d.cfg(` and `d.failureInjector(`.)

Then check for the same fields referenced from other engine files:
```bash
rg -n '\.config\b|\.failure\b' pkg/gpu/mocknvml/engine/*.go | rg -v '_test.go|e\.config|\.configurableDevices|dev\.Config|\.Config\b'
```
Update any `ConfigurableDevice.failure`/`.config` usage found outside `device.go` to the accessors. The known one is `engine.go` `PendingXidEvent` (handled in Step 6). Also confirm no code **assigns** to `d.cfg()` (accessors are read-only): `rg -n 'd\.cfg\(\) *=' pkg/gpu/mocknvml/engine/device.go` should be empty.

- [ ] **Step 5: Write refresh behavior tests**

```go
// pkg/gpu/mocknvml/engine/overlay_refresh_test.go
package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
)

// newTestDevice builds a ConfigurableDevice backed by a dgxa100 base device
// and points the package overlay store at a temp file with a controllable clock.
func newTestDevice(t *testing.T, base *DeviceConfig) (*ConfigurableDevice, string, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	now := time.Unix(0, 0)
	clock := &now
	overlays = newOverlayStoreAt(func() string { return path }, func() time.Time { return *clock })
	t.Cleanup(resetOverlayStoreForTesting)

	srv := dgxa100.New()
	bd := srv.Devices[0].(*mockserver.Device) // import mockserver
	dev := NewConfigurableDevice(0, bd, base, "GPU-test", "0000:01:00.0", 0, nil)
	return dev, path, clock
}

func writeOverlay(t *testing.T, path, content string, clock *time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(2 * time.Second) // move past TTL
}

func TestRefresh_InjectsLostThenResets(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{})
	if dev.failureInjector() != nil {
		t.Fatal("device should start healthy")
	}
	writeOverlay(t, path, "devices:\n  \"0\":\n    failure:\n      mode: lost\n", clock)
	fi := dev.failureInjector()
	if fi == nil || fi.Mode() != FailureModeLost {
		t.Fatalf("expected lost injector, got %+v", fi)
	}
	// Clear overlay -> back to healthy.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(2 * time.Second)
	if dev.failureInjector() != nil {
		t.Fatal("device should recover to healthy after overlay removed")
	}
}

func TestRefresh_AllAppliesToDevice(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{})
	writeOverlay(t, path, "all:\n  failure:\n    mode: ecc_uncorrectable\n    after_calls: 1\n", clock)
	fi := dev.failureInjector()
	if fi == nil || fi.Mode() != FailureModeECCUncorrectable {
		t.Fatalf("expected ecc_uncorrectable from all: %+v", fi)
	}
	_ = nvml.SUCCESS
}
```

(Add the `mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"` import used by `newTestDevice`.)

- [ ] **Step 6: Refresh devices in `PendingXidEvent`**

In `engine.go` `PendingXidEvent` (lines ~528-536), refresh each device and read the injector through the accessor before claiming its Xid, so overlay-injected failures deliver their Xid to already-running consumers:

```go
	for _, dev := range e.server.configurableDevices {
		if dev == nil {
			continue
		}
		fi := dev.failureInjector()
		if fi == nil {
			continue
		}
		xid, ok := fi.ClaimXid()
		if !ok {
			continue
		}
```

Also update `ResetForTesting` in `engine.go` to reset the overlay store:

```go
func ResetForTesting() {
	ClearConfigCache()
	resetFabricReadinessForTesting()
	resetOverlayStoreForTesting()
	engineOnce = sync.Once{}
	engineInstance = nil
	debugLog("[ENGINE] Reset for testing\n")
}
```

- [ ] **Step 7: Build the shared library + run the full engine test suite**

Run:
```bash
go build ./pkg/gpu/mocknvml/...
go test -race ./pkg/gpu/mocknvml/engine/ -v
```
Expected: PASS (existing tests still green, new refresh tests pass). If any pre-existing test set `d.config`/`d.failure` directly, update it to the accessors/`baseConfig`.

- [ ] **Step 8: Commit**

```bash
git add pkg/gpu/mocknvml/engine/device.go pkg/gpu/mocknvml/engine/engine.go pkg/gpu/mocknvml/engine/overlay_refresh_test.go
git commit -s -m "feat(nvml-mock): hot-reload device config from runtime overlay"
```

---

## Task 5: `nvml-mock-ctl` overlay library (pure logic)

**Files:**
- Create: `pkg/gpu/mockctl/overlay.go`
- Test: `pkg/gpu/mockctl/overlay.go`'s tests in `pkg/gpu/mockctl/overlay_test.go`

**Interfaces:**
- Produces (package `mockctl`):
  - `type Doc struct { Version int; All map[string]any; Devices map[string]map[string]any }` (YAML tags `version`,`all`,`devices`).
  - `func Load(path string) (*Doc, error)` — returns an empty `*Doc` (not nil) when the file is absent.
  - `func (d *Doc) Bytes() ([]byte, error)` — marshal to YAML.
  - `func (d *Doc) SetFields(target Target, kv map[string]any)` — merge `kv` into `all` (Target.All) or `devices[idx]`.
  - `func (d *Doc) Fail(target Target, mode string, afterCalls int, xidCode uint64) error` — sugar building the `failure` block; validates `mode`.
  - `func (d *Doc) Reset(target Target)` — remove overrides for the target (or all).
  - `func ParseSet(pairs []string) (map[string]any, error)` — turn `a.b.c=val` args into a nested map, parsing each value as a YAML scalar (so `1`→int, `true`→bool, `disabled`→string).
  - `func Validate(base *engine.DeviceConfig, patch map[string]any) error` — wraps `engine.MergeDeviceConfig` and discards the result, surfacing unknown-field/type errors.
  - `type Target struct { All bool; Index int }`.
  - `func ResolveTarget(spec string, cfg *engine.Config) (Target, error)` — `"all"`→All; integer→index; otherwise treat as UUID and resolve via `cfg` (`GetDeviceUUID`), erroring if unknown.

- [ ] **Step 1: Write failing tests**

```go
// pkg/gpu/mockctl/overlay_test.go
package mockctl

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func TestParseSet_TypesAndNesting(t *testing.T) {
	m, err := ParseSet([]string{"ecc.mode_current=disabled", "failure.after_calls=1", "failure.mode=lost"})
	if err != nil {
		t.Fatal(err)
	}
	ecc := m["ecc"].(map[string]any)
	if ecc["mode_current"] != "disabled" {
		t.Fatalf("bad ecc: %v", ecc)
	}
	fail := m["failure"].(map[string]any)
	if fail["after_calls"] != float64(1) && fail["after_calls"] != 1 {
		t.Fatalf("after_calls should parse numeric: %#v", fail["after_calls"])
	}
}

func TestDocFail_SetsFailureForIndex(t *testing.T) {
	d := &Doc{}
	if err := d.Fail(Target{Index: 2}, "ecc_uncorrectable", 1, 79); err != nil {
		t.Fatal(err)
	}
	f := d.Devices["2"]["failure"].(map[string]any)
	if f["mode"] != "ecc_uncorrectable" {
		t.Fatalf("mode not set: %v", f)
	}
}

func TestDocFail_RejectsBadMode(t *testing.T) {
	if err := (&Doc{}).Fail(Target{All: true}, "banana", 0, 0); err == nil {
		t.Fatal("expected invalid mode error")
	}
}

func TestReset_All(t *testing.T) {
	d := &Doc{All: map[string]any{"x": 1}, Devices: map[string]map[string]any{"0": {"y": 2}}}
	d.Reset(Target{All: true})
	if d.All != nil || len(d.Devices) != 0 {
		t.Fatalf("reset all should clear everything: %+v", d)
	}
}

func TestValidate_RejectsUnknownField(t *testing.T) {
	if err := Validate(&engine.DeviceConfig{}, map[string]any{"nope": 1}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestResolveTarget_UUID(t *testing.T) {
	cfg := &engine.Config{YAMLConfig: &engine.YAMLConfig{
		Devices: []engine.DeviceOverride{{Index: 3, UUID: "GPU-abc"}},
	}}
	tg, err := ResolveTarget("GPU-abc", cfg)
	if err != nil || tg.Index != 3 {
		t.Fatalf("uuid resolve failed: %+v %v", tg, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/gpu/mockctl/ -v`
Expected: FAIL (package/symbols undefined).

- [ ] **Step 3: Implement `pkg/gpu/mockctl/overlay.go`**

```go
// pkg/gpu/mockctl/overlay.go
package mockctl

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// Doc mirrors engine.OverlayDoc but lives in the CLI package so nvml-mock-ctl
// never links the CGo bridge/build tags. The on-disk schema is identical.
type Doc struct {
	Version int                       `json:"version,omitempty"`
	All     map[string]any            `json:"all,omitempty"`
	Devices map[string]map[string]any `json:"devices,omitempty"`
}

type Target struct {
	All   bool
	Index int
}

func Load(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Doc{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &Doc{}, nil
	}
	var d Doc
	if err := yaml.UnmarshalStrict(data, &d); err != nil {
		return nil, fmt.Errorf("parsing overrides: %w", err)
	}
	return &d, nil
}

func (d *Doc) Bytes() ([]byte, error) {
	if d.Version == 0 {
		d.Version = 1
	}
	return yaml.Marshal(d)
}

func (d *Doc) bucket(t Target) map[string]any {
	if t.All {
		if d.All == nil {
			d.All = map[string]any{}
		}
		return d.All
	}
	if d.Devices == nil {
		d.Devices = map[string]map[string]any{}
	}
	key := strconv.Itoa(t.Index)
	if d.Devices[key] == nil {
		d.Devices[key] = map[string]any{}
	}
	return d.Devices[key]
}

// SetFields deep-merges kv into the target bucket.
func (d *Doc) SetFields(t Target, kv map[string]any) {
	mergeInto(d.bucket(t), kv)
}

func (d *Doc) Fail(t Target, mode string, afterCalls int, xidCode uint64) error {
	switch mode {
	case engine.FailureModeHealthy, engine.FailureModeLost,
		engine.FailureModeFallenOffBus, engine.FailureModeECCUncorrectable:
	default:
		return fmt.Errorf("invalid mode %q (want healthy|lost|fallen_off_bus|ecc_uncorrectable)", mode)
	}
	if mode == engine.FailureModeHealthy {
		// healthy == remove the failure block for the target.
		b := d.bucket(t)
		delete(b, "failure")
		return nil
	}
	failure := map[string]any{"mode": mode}
	if afterCalls > 0 {
		failure["after_calls"] = afterCalls
	}
	if xidCode > 0 {
		failure["xid"] = map[string]any{"code": xidCode}
	}
	d.SetFields(t, map[string]any{"failure": failure})
	return nil
}

func (d *Doc) Reset(t Target) {
	if t.All {
		d.All = nil
		d.Devices = nil
		return
	}
	delete(d.Devices, strconv.Itoa(t.Index))
}

// ParseSet converts ["a.b=1","c=x"] into a nested map, parsing each value as a
// YAML scalar so numbers/bools/strings get their natural type.
func ParseSet(pairs []string) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range pairs {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			return nil, fmt.Errorf("invalid set %q (want path=value)", p)
		}
		path, raw := p[:eq], p[eq+1:]
		var val any
		if err := yaml.Unmarshal([]byte(raw), &val); err != nil {
			return nil, fmt.Errorf("parsing value for %q: %w", path, err)
		}
		cur := out
		keys := strings.Split(path, ".")
		for i, k := range keys {
			if k == "" {
				return nil, fmt.Errorf("invalid path %q", path)
			}
			if i == len(keys)-1 {
				cur[k] = val
				break
			}
			next, ok := cur[k].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[k] = next
			}
			cur = next
		}
	}
	return out, nil
}

func Validate(base *engine.DeviceConfig, patch map[string]any) error {
	_, err := engine.MergeDeviceConfig(base, patch)
	return err
}

// ResolveTarget interprets a --gpu spec: "all", a device index, or a UUID.
func ResolveTarget(spec string, cfg *engine.Config) (Target, error) {
	if spec == "all" {
		return Target{All: true}, nil
	}
	if idx, err := strconv.Atoi(spec); err == nil {
		return Target{Index: idx}, nil
	}
	if cfg != nil && cfg.YAMLConfig != nil {
		for _, dev := range cfg.YAMLConfig.Devices {
			if dev.UUID == spec {
				return Target{Index: dev.Index}, nil
			}
		}
	}
	return Target{}, fmt.Errorf("cannot resolve --gpu %q (not 'all', an index, or a known UUID)", spec)
}

func mergeInto(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeInto(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/gpu/mockctl/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/gpu/mockctl/overlay.go pkg/gpu/mockctl/overlay_test.go
git commit -s -m "feat(nvml-mock): add nvml-mock-ctl overlay mutation library"
```

---

## Task 6: `nvml-mock-ctl` CLI entrypoint

**Files:**
- Create: `cmd/nvml-mock-ctl/main.go`
- Test: `cmd/nvml-mock-ctl/main_test.go`

**Interfaces:**
- Consumes: `pkg/gpu/mockctl` (Task 5), `engine.LoadYAMLConfig`, `engine.OverlayPathFor` (Tasks 1-2).
- Produces: `func run(args []string, stdout, stderr io.Writer) int` — testable core; `main` calls `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))`.

Subcommands: `fail`, `set`, `apply`, `status`, `reset`. Each takes `--gpu` (required except `status`). Overlay path from `MOCK_NVML_OVERRIDES` or `--file`, default `/var/lib/nvml-mock/driver/config/overrides.yaml`. Config path from `MOCK_NVML_CONFIG` or `--config`, default `/var/lib/nvml-mock/driver/config/config.yaml` (used for UUID resolution + validation base = merged device defaults). Mutations: acquire an exclusive `flock` on `<overlay>.lock`, load, mutate, validate, write atomically (temp file in same dir + `os.Rename`).

- [ ] **Step 1: Write failing tests**

```go
// cmd/nvml-mock-ctl/main_test.go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, overlay string, args ...string) (string, string, int) {
	t.Helper()
	full := append([]string{"--file", overlay}, args...)
	var out, errb bytes.Buffer
	code := run(full, &out, &errb)
	return out.String(), errb.String(), code
}

func TestCLI_FailWritesOverlay(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, errStr, code := runCLI(t, overlay, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errStr)
	}
	data, _ := os.ReadFile(overlay)
	if !strings.Contains(string(data), "ecc_uncorrectable") {
		t.Fatalf("overlay missing mode: %s", data)
	}
}

func TestCLI_SetRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	_, _, code := runCLI(t, overlay, "set", "--gpu", "all", "bogus.field=1")
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown field")
	}
}

func TestCLI_StatusEmpty(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	out, _, code := runCLI(t, overlay, "status")
	if code != 0 {
		t.Fatal("status should succeed on absent overlay")
	}
	if !strings.Contains(out, "no active overrides") {
		t.Fatalf("unexpected status: %s", out)
	}
}

func TestCLI_ResetGPU(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "overrides.yaml")
	if _, e, c := runCLI(t, overlay, "fail", "--gpu", "1", "--mode", "lost"); c != 0 {
		t.Fatalf("setup fail: %s", e)
	}
	if _, e, c := runCLI(t, overlay, "reset", "--gpu", "1"); c != 0 {
		t.Fatalf("reset: %s", e)
	}
	data, _ := os.ReadFile(overlay)
	if strings.Contains(string(data), "lost") {
		t.Fatalf("reset did not remove device 1: %s", data)
	}
}
```

(These tests pass `--config` implicitly absent; validation base falls back to an empty `DeviceConfig`, and UUID targets are unused, so no config file is required.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/nvml-mock-ctl/ -v`
Expected: FAIL (`run` undefined).

- [ ] **Step 3: Implement `cmd/nvml-mock-ctl/main.go`**

```go
// cmd/nvml-mock-ctl/main.go
// nvml-mock-ctl mutates the nvml-mock runtime overlay so a running node's
// simulated GPU state can be changed without a Helm upgrade or pod restart.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
)

const (
	defaultOverlay = "/var/lib/nvml-mock/driver/config/overrides.yaml"
	defaultConfig  = "/var/lib/nvml-mock/driver/config/config.yaml"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: nvml-mock-ctl <command> [flags]

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
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]

	var overlayPath, configPath, gpu, mode, patchFile string
	var afterCalls int
	var xid uint64
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&overlayPath, "file", envOr("MOCK_NVML_OVERRIDES", defaultOverlay), "overlay path")
	fs.StringVar(&configPath, "config", envOr("MOCK_NVML_CONFIG", defaultConfig), "config path")
	fs.StringVar(&gpu, "gpu", "", "target: index, 'all', or UUID")
	fs.StringVar(&mode, "mode", "", "failure mode (fail command)")
	fs.StringVar(&patchFile, "f", "", "patch file (apply command)")
	fs.IntVar(&afterCalls, "after-calls", 0, "trip after N guarded calls (fail)")
	fs.Uint64Var(&xid, "xid", 0, "Xid code to surface (fail)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}

	cfg := loadConfig(configPath) // best-effort; nil-safe downstream
	base := deviceDefaults(cfg)

	switch cmd {
	case "status":
		return doStatus(overlayPath, gpu, stdout, stderr)
	case "fail", "set", "apply", "reset":
		return mutate(cmd, overlayPath, gpu, mode, patchFile, afterCalls, xid, cfg, base, stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

func mutate(cmd, overlayPath, gpu, mode, patchFile string, afterCalls int, xid uint64,
	cfg *engine.Config, base *engine.DeviceConfig, stdout, stderr io.Writer) int {

	if gpu == "" && cmd != "reset" {
		fmt.Fprintln(stderr, "--gpu is required")
		return 2
	}

	unlock, err := lockOverlay(overlayPath)
	if err != nil {
		fmt.Fprintf(stderr, "lock: %v\n", err)
		return 1
	}
	defer unlock()

	doc, err := mockctl.Load(overlayPath)
	if err != nil {
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}

	var target mockctl.Target
	if gpu != "" {
		target, err = mockctl.ResolveTarget(gpu, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
	} else {
		target = mockctl.Target{All: true} // reset with no --gpu means everything
	}

	switch cmd {
	case "fail":
		if mode == "" {
			fmt.Fprintln(stderr, "--mode is required for fail")
			return 2
		}
		if err := doc.Fail(target, mode, afterCalls, xid); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
	case "set":
		kv, err := mockctl.ParseSet(fsArgs(patchFile, os.Args))
		_ = err // replaced below
		return doSet(doc, target, base, stdout, stderr, overlayPath)
	case "apply":
		if patchFile == "" {
			fmt.Fprintln(stderr, "-f patch file is required for apply")
			return 2
		}
		data, err := os.ReadFile(patchFile)
		if err != nil {
			fmt.Fprintf(stderr, "read patch: %v\n", err)
			return 1
		}
		patch, err := parseYAMLMap(data)
		if err != nil {
			fmt.Fprintf(stderr, "parse patch: %v\n", err)
			return 2
		}
		if err := mockctl.Validate(base, patch); err != nil {
			fmt.Fprintf(stderr, "invalid patch: %v\n", err)
			return 2
		}
		doc.SetFields(target, patch)
	case "reset":
		doc.Reset(target)
	}

	// Validate the resulting merged config for the affected bucket(s).
	if err := validateDoc(doc, base); err != nil {
		fmt.Fprintf(stderr, "invalid overlay: %v\n", err)
		return 2
	}

	if err := writeAtomic(overlayPath, doc); err != nil {
		fmt.Fprintf(stderr, "write: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ok: %s applied to %s\n", cmd, gpuLabel(gpu))
	return 0
}
```

Add the small helpers below to the same file:

```go
func doSet(doc *mockctl.Doc, target mockctl.Target, base *engine.DeviceConfig,
	stdout, stderr io.Writer, overlayPath string) int {
	// set pairs are the non-flag args; re-parse from the raw command tail.
	pairs := setPairs()
	if len(pairs) == 0 {
		fmt.Fprintln(stderr, "set requires at least one key.path=value")
		return 2
	}
	kv, err := mockctl.ParseSet(pairs)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 2
	}
	if err := mockctl.Validate(base, kv); err != nil {
		fmt.Fprintf(stderr, "invalid: %v\n", err)
		return 2
	}
	doc.SetFields(target, kv)
	if err := writeAtomic(overlayPath, doc); err != nil {
		fmt.Fprintf(stderr, "write: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok: set applied")
	return 0
}
```

> **Implementation note for the engineer:** the `set` command mixes flags (`--gpu`) with positional `key=value` args. Rather than the sketchy `fsArgs`/`setPairs` placeholders above, implement `set` by taking `fs.Args()` after `fs.Parse` as the positional pairs. Refactor `mutate` so it receives `fs.Args()` and, for `set`, passes them to `mockctl.ParseSet`. Concretely: in `run`, after `fs.Parse(rest)`, capture `positional := fs.Args()` and thread it into `mutate` as a `[]string`; delete `doSet`/`setPairs`/`fsArgs` and handle `set` inline in the `switch` using `mockctl.ParseSet(positional)`. This keeps a single code path. (The stubbed helpers above exist only to show intent; the final code must compile without them.)

Helpers used above:

```go
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
		fmt.Fprintf(stderr, "load: %v\n", err)
		return 1
	}
	b, err := doc.Bytes()
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	if doc.All == nil && len(doc.Devices) == 0 {
		fmt.Fprintln(stdout, "no active overrides")
		return 0
	}
	fmt.Fprint(stdout, string(b))
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
	if err := yamlUnmarshalStrict(data, &m); err != nil {
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
```

Use `sigs.k8s.io/yaml` for `yamlUnmarshalStrict` (import it and define `yamlUnmarshalStrict = yaml.UnmarshalStrict`, or call `yaml.UnmarshalStrict` directly).

> **Engineer:** apply the refactor described in the implementation note so `set` uses `fs.Args()` and the stub helpers (`doSet`, `setPairs`, `fsArgs`) are removed. The file must compile cleanly and `go vet` must pass.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/nvml-mock-ctl/ -v`
Expected: PASS.

- [ ] **Step 5: Build the binary + vet**

Run:
```bash
go build -o /tmp/nvml-mock-ctl ./cmd/nvml-mock-ctl && go vet ./cmd/nvml-mock-ctl/
/tmp/nvml-mock-ctl --help
```
Expected: builds; `--help` prints usage.

- [ ] **Step 6: Commit**

```bash
git add cmd/nvml-mock-ctl/
git commit -s -m "feat(nvml-mock): add nvml-mock-ctl CLI"
```

---

## Task 7: Deployment wiring (Dockerfile, setup.sh, Helm)

**Files:**
- Modify: `deployments/nvml-mock/Dockerfile`
- Modify: `deployments/nvml-mock/scripts/setup.sh`
- Modify: `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml`

**Interfaces:**
- Consumes: `cmd/nvml-mock-ctl` binary; `engine` overlay path resolution (Task 2/6).
- Produces: `nvml-mock-ctl` on `PATH` in the DaemonSet pod; `overrides.yaml` cleared on pod start; overlay visible to consumers via a CDI **directory** mount; `MOCK_NVML_OVERRIDES` set in both the DaemonSet container (host path) and the CDI env (container path).

- [ ] **Step 1: Dockerfile — build and install the CLI**

In `deployments/nvml-mock/Dockerfile`, add the source copy near the other `COPY cmd/...` lines (after line ~32):

```dockerfile
COPY cmd/nvml-mock-ctl/ cmd/nvml-mock-ctl/
```

Add to the `go build` chain in the builder stage (inside the `RUN mkdir -p /out && ...` block, add a line):

```dockerfile
    && go build -mod=vendor -o /out/nvml-mock-ctl ./cmd/nvml-mock-ctl \
```

Note: `pkg/gpu/mockctl/` imports `pkg/gpu/mocknvml/engine`, which is already copied into the builder (`COPY pkg/gpu/mocknvml/ ...`). Add the new package copy is unnecessary (it lives under `pkg/gpu/`), but confirm the build sees it — if `pkg/gpu/mockctl` is a new top-level dir under `pkg/gpu`, ensure it is included. It is under `pkg/gpu/` but not under `pkg/gpu/mocknvml/`, so add:

```dockerfile
COPY pkg/gpu/mockctl/ pkg/gpu/mockctl/
```

In the final image stage, install the binary (near the other `COPY --from=builder /out/...` lines):

```dockerfile
COPY --from=builder /out/nvml-mock-ctl /usr/local/bin/nvml-mock-ctl
```

- [ ] **Step 2: setup.sh — clear overlay on start, mount config dir, inject env**

In `deployments/nvml-mock/scripts/setup.sh`:

(a) After the config is copied and `num_devices` injected (after line ~252), clear any stale overlay so a pod restart resets to pristine YAML:

```sh
# Runtime overrides (written by nvml-mock-ctl) are ephemeral: wipe them on
# every pod start so a restart of this DaemonSet resets simulated GPU state
# back to the pristine profile config.
rm -f "$CONFIG_DIR/overrides.yaml" "$DRIVER_ROOT/config/overrides.yaml"
```

(b) Change the CDI config mount from a single file to the directory, so `overrides.yaml` (created later at runtime by `nvml-mock-ctl`, via temp-file+rename) is visible to consumers and rename is observed. Replace the config.yaml file mount block (lines ~110-112) with a directory mount:

```yaml
    - hostPath: /var/lib/nvml-mock/driver/config
      containerPath: /etc/nvml-mock
      options: [ro, nosuid, nodev, bind]
```

`MOCK_NVML_CONFIG=/etc/nvml-mock/config.yaml` (already set in the CDI env block) is unchanged and still resolves.

(c) Add the overlay env to the CDI env block (after `MOCK_NVML_CONFIG=...`, line ~136):

```sh
    - MOCK_NVML_OVERRIDES=/etc/nvml-mock/overrides.yaml
```

- [ ] **Step 3: Helm daemonset — env for the CLI**

In `deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml`, add to the main container's `env:` list so `nvml-mock-ctl` (run via `kubectl exec`) writes to the host-mounted overlay path:

```yaml
            - name: MOCK_NVML_OVERRIDES
              value: /host/var/lib/nvml-mock/driver/config/overrides.yaml
```

(Confirm the host `/var/lib/nvml-mock` is mounted at `/host/var/lib/nvml-mock` in this container — it is, per `setup.sh`'s `HOST=/host/var/lib/nvml-mock`. If the mountPath differs, match it.)

- [ ] **Step 4: Helm unittest + template render**

Run:
```bash
helm template deployments/nvml-mock/helm/nvml-mock | rg -n 'MOCK_NVML_OVERRIDES' || echo "check env rendered"
helm unittest deployments/nvml-mock/helm/nvml-mock
```
Expected: env renders; existing Helm unit tests pass. If a snapshot test covers the daemonset env, update the snapshot (`helm unittest -u ...`) and review the diff.

- [ ] **Step 5: Commit**

```bash
git add deployments/nvml-mock/Dockerfile deployments/nvml-mock/scripts/setup.sh deployments/nvml-mock/helm/nvml-mock/
git commit -s -m "build(nvml-mock): ship nvml-mock-ctl and wire runtime overlay mount"
```

---

## Task 8: End-to-end scenario using `nvml-mock-ctl`

**Files:**
- Create: `tests/e2e/go/scenario_runtime_control.go`
- Modify: the E2E suite file that lists scenarios (find it: `rg -l 'assertECCUncorrectableFailure|scenario' tests/e2e/go/*_test.go`), register the new scenario there following the existing pattern.

**Interfaces:**
- Consumes: the harness helpers already used by `scenario_failure_injection.go` (`h.Kube.Exec`, `firstNvmlPod`, `nvidiaSMILCount`, `eccQuery`, `hasFailureMarker`, `nvmlMockNamespace`, `nvmlMockSelector`).

Scenario: with a running consumer, exec `nvml-mock-ctl` in the DaemonSet pod to inject `ecc_uncorrectable` on GPU 0, assert the **already-running** consumer's `nvidia-smi` ECC query rises above 0 within the TTL (poll with `Eventually`), then `reset` and assert it returns to healthy. Because the overlay changes without a Helm upgrade or pod delete, this validates the "both observers, no restart" requirement.

- [ ] **Step 1: Write the scenario (build-tagged e2e)**

```go
//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

func nvmlMockCtl(ctx SpecContext, h *harness.Harness, args ...string) string {
	GinkgoHelper()
	pod := firstNvmlPod(ctx, h)
	full := append([]string{"nvml-mock-ctl"}, args...)
	res, err := h.Kube.Exec(ctx, pod, full...)
	Expect(err).NotTo(HaveOccurred(), "nvml-mock-ctl %v: %s", args, res.Combined())
	return res.Stdout
}

func assertRuntimeECCInjection(ctx SpecContext, h *harness.Harness, consumer kube.PodRef) {
	GinkgoHelper()
	By("inject ecc_uncorrectable on GPU 0 at runtime via nvml-mock-ctl")
	nvmlMockCtl(ctx, h, "fail", "--gpu", "0", "--mode", "ecc_uncorrectable", "--after-calls", "1", "--xid", "79")

	Eventually(func() int {
		return maxIntegerLine(eccQuery(ctx, h, consumer))
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(2*time.Second).
		Should(BeNumerically(">", 0), "running consumer should observe injected ECC errors within the TTL")

	By("reset runtime overrides")
	nvmlMockCtl(ctx, h, "reset", "--gpu", "all")

	Eventually(func() int {
		return maxIntegerLine(eccQuery(ctx, h, consumer))
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(2*time.Second).
		Should(Equal(0), "consumer should return to healthy after reset")
}
```

- [ ] **Step 2: Register + wire the scenario**

Follow how `assertECCUncorrectableFailure` is invoked in the suite. Add an `It(...)`/`By(...)` entry that: ensures a long-lived consumer pod exists (reuse the existing consumer/workload the failure-injection suite uses, or start a sleep pod that has the mock injected), captures its `kube.PodRef`, then calls `assertRuntimeECCInjection(ctx, h, consumer)`. Do not delete or restart the consumer between inject and assert.

- [ ] **Step 3: Verify it compiles under the e2e tag**

Run:
```bash
go build -tags e2e ./tests/e2e/go/...
go vet -tags e2e ./tests/e2e/go/...
```
Expected: compiles and vets cleanly. (Full E2E requires a Kind cluster + image build; run it if the environment is available: `make e2e` per the repo Makefile. Otherwise compilation is the gate for this task.)

- [ ] **Step 4: Commit**

```bash
git add tests/e2e/go/
git commit -s -m "test(nvml-mock): e2e runtime control via nvml-mock-ctl"
```

---

## Task 9: User documentation

**Files:**
- Create: `docs/nvml-mock-ctl.md`
- Modify: `pkg/gpu/mocknvml/README.md` (cross-link)

- [ ] **Step 1: Write `docs/nvml-mock-ctl.md`**

Write the document with these sections (content must be concrete, no placeholders):

1. **Overview / principle.** nvml-mock is a per-process shared library, not a daemon. `nvml-mock-ctl` writes a node-local `overrides.yaml` that the engine re-reads on a short TTL (default 1s, `MOCK_NVML_OVERLAY_TTL`) and deep-merges over the pristine `config.yaml`. Both already-running and new consumers converge within one TTL. The base config is never mutated. Note the v1 scope caveat (identity/topology fields baked at construction are not hot-reloadable; failure/ECC/thermal/power/utilization/clocks/fan are).
2. **Where it runs.** `kubectl exec` into the target node's nvml-mock DaemonSet pod; per-node scope. Show the pod-selection one-liner.
3. **Command reference.** `fail`, `set`, `apply`, `status`, `reset`, with `--gpu <idx|all|uuid>`, `--after-calls`, `--xid`, `--file`, `--config`.
4. **Reset semantics table** (ctl reset / DaemonSet restart / consumer restart / Helm upgrade).
5. **Worked examples** (copy-pasteable), at least:

```bash
# Pick the nvml-mock pod on a node
POD=$(kubectl -n nvml-mock get pod -l app.kubernetes.io/name=nvml-mock \
  --field-selector spec.nodeName=<node> -o jsonpath='{.items[0].metadata.name}')

# 1) Force uncorrectable ECC on GPU 0, deliver Xid 79
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fail --gpu 0 --mode ecc_uncorrectable --after-calls 1 --xid 79
# verify from any consumer pod:
kubectl exec <consumer> -- nvidia-smi --query-gpu=ecc.errors.uncorrected.aggregate.total --format=csv,noheader

# 2) Mark ALL GPUs lost
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fail --gpu all --mode lost

# 3) Set an arbitrary field
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl set --gpu 3 temperature.gpu_temp_c=95

# 4) Apply a multi-field snippet
cat > /tmp/patch.yaml <<'EOF'
ecc:
  mode_current: disabled
utilization:
  gpu: 100
EOF
kubectl -n nvml-mock cp /tmp/patch.yaml "$POD":/tmp/patch.yaml -n nvml-mock
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl apply --gpu 0 -f /tmp/patch.yaml

# 5) Target by UUID
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fail --gpu GPU-xxxxxxxx-... --mode fallen_off_bus

# 6) Inspect active overrides
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl status

# 7) Recover one GPU / reset everything
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl fail --gpu 0 --mode healthy
kubectl -n nvml-mock exec "$POD" -- nvml-mock-ctl reset --gpu all

# 8) Full reset via pod restart (state reverts to pristine YAML)
kubectl -n nvml-mock delete pod "$POD"
```

6. **Troubleshooting.** Propagation delay = TTL; confirm the overlay with `status` or by reading `overrides.yaml`; validation errors (unknown field / bad type) reject the write; per-node scope (repeat per node).

- [ ] **Step 2: Cross-link from the mock README**

Add a short subsection under the failure-injection area of `pkg/gpu/mocknvml/README.md` pointing to `docs/nvml-mock-ctl.md` for runtime control (contrast: Helm values = boot-time; `nvml-mock-ctl` = runtime).

- [ ] **Step 3: Commit**

```bash
git add docs/nvml-mock-ctl.md pkg/gpu/mocknvml/README.md
git commit -s -m "docs(nvml-mock): document nvml-mock-ctl runtime control"
```

---

## Task 10: Full verification pass

- [ ] **Step 1: Unit tests (race) across the module**

Run: `go test -race $(go list ./... | grep -v vendor)`
Expected: PASS.

- [ ] **Step 2: Build the mock shared library**

Run: `cd pkg/gpu/mocknvml && make clean && make`
Expected: `libnvidia-ml.so.*` builds (the atomic-pointer refactor compiles under CGo build mode).

- [ ] **Step 3: Lint**

Run: `golangci-lint run -v --timeout 5m`
Expected: no new findings. Fix any introduced issues.

- [ ] **Step 4: Helm unittest**

Run: `helm unittest deployments/nvml-mock/helm/nvml-mock`
Expected: PASS.

- [ ] **Step 5: E2E compile check**

Run: `go build -tags e2e ./tests/e2e/go/...`
Expected: compiles.

---

## Self-Review (completed by plan author)

**Spec coverage:**
- Runtime CLI (broad, hybrid, index/all/uuid): Tasks 5-6. ✅
- Both observers via TTL re-read: Tasks 2, 4. ✅
- Reset on DaemonSet pod restart: Task 7 (setup.sh wipe). ✅
- Base config never mutated: Task 1 (`MergeDeviceConfig` copies), Task 4 (`baseConfig` immutable). ✅
- Overlay file location + CDI visibility + atomic rename: Task 7 (directory mount). ✅
- Failure reconcile incl. healthy reset: Tasks 3-4. ✅
- Documentation deliverable with examples: Task 9. ✅
- E2E via ctl on a running consumer: Task 8. ✅

**Placeholder scan:** The `set` command sketch in Task 6 intentionally contains stub helpers (`fsArgs`, `setPairs`, `doSet`) with an explicit engineer note to replace them with a single `fs.Args()`-based path. This is called out, not silent — the final code must compile without the stubs.

**Type consistency:** `OverlayDoc`/`Doc` share the same on-disk schema (`version`/`all`/`devices`), intentionally duplicated across `engine` (CGo-tagged) and `mockctl` (CLI) packages to avoid linking the bridge into the CLI. `MergeDeviceConfig(base, patch)` signature is consistent across Tasks 1, 5, 6. `cfg()`/`failureInjector()`/`refresh()` names are consistent across Tasks 4 and its consumers.
