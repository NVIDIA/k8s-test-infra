// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Fans and cooling beyond the duty-cycle reading in device.go.
//
// Two things shape this file. The first is that no nvidia-smi reaches any of
// it: 575 and 580 reference exactly nvmlDeviceGetFanSpeed and
// nvmlUnitGetFanSpeedInfo, and `--query-gpu` offers only fan.speed. So the
// consumer here is a caller holding NVML directly, and the coverage that
// matters is the ABI suite in tests/mocknvml rather than an nvidia-smi
// assertion.
//
// The second is that every profile Mokka ships declares no fans, matching all
// seven hardware captures, which report fan_speed as N/A — including the L40S
// and T4, whose PCIe boards are passively cooled. So the answer on a shipped
// profile is a refusal either way. What changes by implementing this is where
// the refusal comes from: NOT_SUPPORTED derived from the profile's fan count is
// what a real board returns, and it lets a profile that does declare fans be
// modelled at all, which a generated stub could never do.
//
// NVML also gates the family on Maxwell or newer. Every profile in the repo is
// Turing or later, so the architecture gate can never be the operative one and
// is deliberately not modelled; the fan count is.
//
// No getter here advances the failure-injection counter, for the same reason
// the Conf Compute getters do not: they answer from static state, and a
// consumer reading them once per query would otherwise shift the schedule an
// "after_calls" scenario trips on. They still surface a lost GPU.

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const (
	// fanPolicyTemperature and fanPolicyManual are the config spellings of
	// the two policies NVML defines. A board reports the first until
	// something asks for a duty cycle.
	fanPolicyTemperature = "temperature"
	fanPolicyManual      = "manual"

	// defaultMaxFanSpeedPercent is the ceiling a fanned profile that
	// declares no envelope reports. Zero would advertise a window no manual
	// write could land in, so an unset ceiling means the full range.
	defaultMaxFanSpeedPercent = 100
)

// fanConfig returns the device's fan block together with whether the board has
// any fans. Everything in this file refuses when it does not.
func (d *ConfigurableDevice) fanConfig() (*FanConfig, bool) {
	c := d.cfg()
	if c.Fan == nil || c.Fan.Count <= 0 {
		return c.Fan, false
	}
	return c.Fan, true
}

// fanned resolves the guards every per-fan entry point shares: a lost GPU
// first, then a board with no fans, then a fan index the board does not have.
// A zero return means the caller may go on and read cfg.
func (d *ConfigurableDevice) fanned(funcName string, fan int) (*FanConfig, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	cfg, ok := d.fanConfig()
	if !ok {
		debugLog("[NVML] %s(fan=%d) -> NOT_SUPPORTED (board declares no fans)\n", funcName, fan)
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	if fan < 0 || fan >= cfg.Count {
		debugLog("[NVML] %s(fan=%d) -> INVALID_ARGUMENT (board has %d fan(s))\n", funcName, fan, cfg.Count)
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	return cfg, nvml.SUCCESS
}

// fanControl returns the control state recorded for one fan, or nil when
// neither the profile nor a setter has named it. Config order does not matter:
// entries are matched on the fan they name, so one naming a fan the board does
// not have is ignored rather than aliased onto a fan it does.
func fanControl(cfg *FanConfig, fan int) *FanControlConfig {
	return findFanControl(cfg.Controls, fan)
}

func findFanControl(controls []FanControlConfig, fan int) *FanControlConfig {
	for i := range controls {
		if controls[i].Fan == fan {
			return &controls[i]
		}
	}
	return nil
}

// fanSpeedPercent is the duty cycle one fan is running at: the duty cycle a
// manual control asked for, otherwise the board's own reading. A profile that
// reports no reading yields false, which callers turn into NOT_SUPPORTED.
func fanSpeedPercent(cfg *FanConfig, fan int) (uint32, bool) {
	if ctl := fanControl(cfg, fan); ctl != nil && ctl.SpeedPercent != nil {
		return uint32(max(*ctl.SpeedPercent, 0)), true
	}
	return parseFanPercent(cfg.SpeedPercent)
}

// parseFanPercent decodes one of the string percentage fields. They are strings
// because a profile spells an absent reading "N/A", which is also what
// nvidia-smi renders; anything unparseable is treated the same way rather than
// silently becoming zero.
func parseFanPercent(raw string) (uint32, bool) {
	if raw == "" || raw == "N/A" {
		return 0, false
	}
	var pct uint32
	if _, err := fmt.Sscanf(raw, "%d", &pct); err != nil {
		return 0, false
	}
	return pct, true
}

// FanSpeedRPM reports one fan's tachometer reading, backing
// nvmlDeviceGetFanSpeedRPM. The RPM is scaled from the duty cycle by the
// profile's ceiling, and a profile that declares no ceiling reports no RPM:
// driving a fan and measuring it are separate capabilities on real hardware,
// so the percentage can answer where this does not.
//
// The fan index is a parameter here but rides in the caller's struct across the
// C ABI, which is why GetFanSpeedRPM below cannot take it and this is the entry
// point the bridge uses.
func (d *ConfigurableDevice) FanSpeedRPM(fan int) (uint32, nvml.Return) {
	const name = "nvmlDeviceGetFanSpeedRPM"
	cfg, ret := d.fanned(name, fan)
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	if cfg.MaxSpeedRPM <= 0 {
		debugLog("[NVML] %s(fan=%d) -> NOT_SUPPORTED (no tachometer)\n", name, fan)
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	pct, ok := fanSpeedPercent(cfg, fan)
	if !ok {
		debugLog("[NVML] %s(fan=%d) -> NOT_SUPPORTED (no speed reading)\n", name, fan)
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	rpm := pct * uint32(cfg.MaxSpeedRPM) / 100
	debugLog("[NVML] %s(fan=%d) -> %d RPM (%d%% of %d)\n", name, fan, rpm, pct, cfg.MaxSpeedRPM)
	return rpm, nvml.SUCCESS
}

// GetFanSpeedRPM satisfies the go-nvml Device contract, whose binding leaves
// the fan index at its zero value and so can only ever ask about the first fan.
// Version is left for the caller to stamp, as go-nvml's own binding does.
func (d *ConfigurableDevice) GetFanSpeedRPM() (nvml.FanSpeedInfo, nvml.Return) {
	rpm, ret := d.FanSpeedRPM(0)
	if ret != nvml.SUCCESS {
		return nvml.FanSpeedInfo{}, ret
	}
	return nvml.FanSpeedInfo{Fan: 0, Speed: rpm}, nvml.SUCCESS
}

// GetTargetFanSpeed reports the duty cycle one fan is aiming for, backing
// nvmlDeviceGetTargetFanSpeed. Under manual control that is what was asked
// for; otherwise it is the profile's target_speed_percent, which every config
// in the repo declares and nothing read until this getter existed.
func (d *ConfigurableDevice) GetTargetFanSpeed(fan int) (int, nvml.Return) {
	const name = "nvmlDeviceGetTargetFanSpeed"
	cfg, ret := d.fanned(name, fan)
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	if ctl := fanControl(cfg, fan); ctl != nil && ctl.SpeedPercent != nil {
		debugLog("[NVML] %s(fan=%d) -> %d%% (manual)\n", name, fan, *ctl.SpeedPercent)
		return *ctl.SpeedPercent, nvml.SUCCESS
	}
	target, ok := parseFanPercent(cfg.TargetSpeedPercent)
	if !ok {
		debugLog("[NVML] %s(fan=%d) -> NOT_SUPPORTED (profile declares no target)\n", name, fan)
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] %s(fan=%d) -> %d%%\n", name, fan, target)
	return int(target), nvml.SUCCESS
}

// GetMinMaxFanSpeed reports the duty-cycle window a manual write may land in,
// backing nvmlDeviceGetMinMaxFanSpeed. It is board-wide rather than per-fan, so
// it takes no fan index, and SetFanSpeed_v2 refuses anything outside it.
func (d *ConfigurableDevice) GetMinMaxFanSpeed() (int, int, nvml.Return) {
	const name = "nvmlDeviceGetMinMaxFanSpeed"
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, 0, ret
	}
	cfg, ok := d.fanConfig()
	if !ok {
		debugLog("[NVML] %s -> NOT_SUPPORTED (board declares no fans)\n", name)
		return 0, 0, nvml.ERROR_NOT_SUPPORTED
	}
	minSpeed, maxSpeed := fanSpeedEnvelope(cfg)
	debugLog("[NVML] %s -> %d-%d%%\n", name, minSpeed, maxSpeed)
	return minSpeed, maxSpeed, nvml.SUCCESS
}

// fanSpeedEnvelope is the window both the getter and the write bound share, so
// the two can never disagree on what the board advertises.
func fanSpeedEnvelope(cfg *FanConfig) (minSpeed, maxSpeed int) {
	maxSpeed = cfg.MaxSpeedPercent
	if maxSpeed <= 0 {
		maxSpeed = defaultMaxFanSpeedPercent
	}
	return max(cfg.MinSpeedPercent, 0), maxSpeed
}

// CoolerInfo describes what one cooler drives and how, backing
// nvmlDeviceGetCoolerInfo. Every discrete board with dedicated fans reports a
// variable signal cooling the GPU, so that is what a fanned profile reports;
// there is no config for it because nothing in the Kubernetes GPU stack
// distinguishes a toggle cooler from a variable one. Version is left for the
// bridge to stamp, since the tag belongs to the C struct rather than to the
// reading.
//
// Like FanSpeedRPM the index crosses the C ABI inside the caller's struct, so
// this rather than GetCoolerInfo is what the bridge calls.
func (d *ConfigurableDevice) CoolerInfo(index int) (nvml.CoolerInfo, nvml.Return) {
	const name = "nvmlDeviceGetCoolerInfo"
	if _, ret := d.fanned(name, index); ret != nvml.SUCCESS {
		return nvml.CoolerInfo{}, ret
	}
	info := nvml.CoolerInfo{
		Index:      uint32(index),
		SignalType: uint32(nvml.THERMAL_COOLER_SIGNAL_VARIABLE),
		Target:     uint32(nvml.THERMAL_COOLER_TARGET_GPU),
	}
	debugLog("[NVML] %s(index=%d) -> signal=variable target=gpu\n", name, index)
	return info, nvml.SUCCESS
}

// GetCoolerInfo satisfies the go-nvml Device contract, whose binding leaves the
// cooler index at its zero value and so can only ever ask about the first
// cooler.
func (d *ConfigurableDevice) GetCoolerInfo() (nvml.CoolerInfo, nvml.Return) {
	return d.CoolerInfo(0)
}

// GetFanControlPolicy_v2 reports whether one fan follows the driver's
// temperature curve or a duty cycle a consumer asked for, backing
// nvmlDeviceGetFanControlPolicy_v2.
func (d *ConfigurableDevice) GetFanControlPolicy_v2(fan int) (nvml.FanControlPolicy, nvml.Return) {
	const name = "nvmlDeviceGetFanControlPolicy_v2"
	cfg, ret := d.fanned(name, fan)
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	policy := nvml.FanControlPolicy(nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW)
	if ctl := fanControl(cfg, fan); ctl != nil && ctl.Policy == fanPolicyManual {
		policy = nvml.FAN_POLICY_MANUAL
	}
	debugLog("[NVML] %s(fan=%d) -> %d\n", name, fan, policy)
	return policy, nvml.SUCCESS
}

// GetFanSpeed_v2 reports one fan's duty cycle, backing
// nvmlDeviceGetFanSpeed_v2. Unlike the board-wide GetFanSpeed it addresses a
// single fan, so it bounds the index and honours a manual duty cycle written
// for that fan alone.
func (d *ConfigurableDevice) GetFanSpeed_v2(fan int) (uint32, nvml.Return) {
	const name = "nvmlDeviceGetFanSpeed_v2"
	cfg, ret := d.fanned(name, fan)
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	speed, ok := fanSpeedPercent(cfg, fan)
	if !ok {
		debugLog("[NVML] %s(fan=%d) -> NOT_SUPPORTED (no speed reading)\n", name, fan)
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] %s(fan=%d) -> %d%%\n", name, fan, speed)
	return speed, nvml.SUCCESS
}

// SetFanSpeed_v2 holds one fan at a duty cycle, backing
// nvmlDeviceSetFanSpeed_v2. It implies manual control, because a fan the driver
// is still steering by temperature would not stay where it was put.
//
// The write is refused outside the window GetMinMaxFanSpeed advertises: a board
// that accepts a duty cycle it just said it could not reach is lying in one of
// the two places.
func (d *ConfigurableDevice) SetFanSpeed_v2(fan, speed int) nvml.Return {
	const name = "nvmlDeviceSetFanSpeed_v2"
	cfg, ret := d.fanned(name, fan)
	if ret != nvml.SUCCESS {
		return ret
	}
	minSpeed, maxSpeed := fanSpeedEnvelope(cfg)
	if speed < minSpeed || speed > maxSpeed {
		debugLog("[NVML] %s(fan=%d, speed=%d) -> INVALID_ARGUMENT (envelope %d-%d%%)\n",
			name, fan, speed, minSpeed, maxSpeed)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return d.writeFanControl(name, fan, FanControlConfig{
		Fan: fan, Policy: fanPolicyManual, SpeedPercent: &speed,
	})
}

// SetDefaultFanSpeed_v2 returns one fan to the driver's temperature curve,
// backing nvmlDeviceSetDefaultFanSpeed_v2. The recorded duty cycle goes with
// the policy: a fan no longer being held anywhere must report the board's own
// reading, not the last value something asked for.
func (d *ConfigurableDevice) SetDefaultFanSpeed_v2(fan int) nvml.Return {
	const name = "nvmlDeviceSetDefaultFanSpeed_v2"
	if _, ret := d.fanned(name, fan); ret != nvml.SUCCESS {
		return ret
	}
	return d.writeFanControl(name, fan, FanControlConfig{Fan: fan, Policy: fanPolicyTemperature})
}

// SetFanControlPolicy switches one fan between the driver's curve and manual
// control, backing nvmlDeviceSetFanControlPolicy. Switching to manual does not
// invent a duty cycle — the fan keeps running at what it was doing until one is
// asked for — and switching back to the curve drops any that was.
func (d *ConfigurableDevice) SetFanControlPolicy(fan int, policy nvml.FanControlPolicy) nvml.Return {
	const name = "nvmlDeviceSetFanControlPolicy"
	if _, ret := d.fanned(name, fan); ret != nvml.SUCCESS {
		return ret
	}
	next := FanControlConfig{Fan: fan}
	switch policy {
	case nvml.FAN_POLICY_MANUAL:
		next.Policy = fanPolicyManual
	case nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW:
		next.Policy = fanPolicyTemperature
	default:
		debugLog("[NVML] %s(fan=%d, policy=%d) -> INVALID_ARGUMENT (NVML defines two policies)\n",
			name, fan, policy)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return d.writeFanControl(name, fan, next)
}

// writeFanControl records one fan's control state in the override document, so
// a consumer in another process reads back what was written — which is how the
// same write behaves against a real driver, where fan control is node state
// rather than state belonging to whoever set it.
//
// The fold runs inside the writer's lock because the recorded list covers every
// fan: two callers writing different fans would otherwise each rebuild the list
// from the same base and one write would be lost. For the same reason a manual
// write that names no duty cycle takes the one already recorded from that base
// rather than from this process's view, which may predate another's write.
func (d *ConfigurableDevice) writeFanControl(funcName string, fan int, next FanControlConfig) nvml.Return {
	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] %s(fan=%d) -> NO_PERMISSION (no override writer)\n", funcName, fan)
		return nvml.ERROR_NO_PERMISSION
	}
	err := w.UpdateFanControl(d.PhysicalIndex(),
		func(base []FanControlConfig, present bool) ([]FanControlConfig, error) {
			if !present {
				base = d.configuredFanControls()
			}
			folded := next
			if folded.Policy == fanPolicyManual && folded.SpeedPercent == nil {
				if prev := findFanControl(base, fan); prev != nil && prev.SpeedPercent != nil {
					speed := *prev.SpeedPercent
					folded.SpeedPercent = &speed
				}
			}
			return replaceFanControl(base, folded), nil
		})
	if err != nil {
		warnLog("[NVML] %s(fan=%d) -> NO_PERMISSION: %v\n", funcName, fan, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] %s(fan=%d) -> written (policy=%s)\n", funcName, fan, next.Policy)
	return nvml.SUCCESS
}

// configuredFanControls is the control state the profile YAML declares, read
// from the pristine config rather than the effective one: the effective config
// already folds in whatever the last setter wrote, and using it as the base for
// the next write would apply that write twice.
func (d *ConfigurableDevice) configuredFanControls() []FanControlConfig {
	if d.baseConfig == nil || d.baseConfig.Fan == nil {
		return nil
	}
	return d.baseConfig.Fan.Controls
}

// replaceFanControl folds one fan's new state into the recorded list, replacing
// the entry for that fan and leaving every other fan as it was.
func replaceFanControl(base []FanControlConfig, next FanControlConfig) []FanControlConfig {
	out := make([]FanControlConfig, 0, len(base)+1)
	for _, ctl := range base {
		if ctl.Fan != next.Fan {
			out = append(out, ctl)
		}
	}
	return append(out, next)
}
