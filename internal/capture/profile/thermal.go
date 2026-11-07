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

package profile

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const (
	ruleFromTLimit = "from the T.Limit reference and offsets"
	ruleGPU0       = "GPU 0's reading"
)

// The thermal values are GPU 0's readings, so they add up in the mock. The
// T.Limit view is read, and the absolute thresholds follow from it:
//
//	tlimit.reference_c     = GetTemperatureThreshold(SLOWDOWN)
//	tlimit.shutdown_c      = field 193, SHUTDOWN_TLIMIT
//	tlimit.slowdown_c      = field 194, SLOWDOWN_TLIMIT
//	tlimit.max_operating_c = field 196, GPU_MAX_TLIMIT
//	shutdown_threshold_c   = tlimit.reference_c - tlimit.shutdown_c
//	slowdown_threshold_c   = tlimit.reference_c
//	max_operating_c        = tlimit.reference_c - tlimit.slowdown_c
//	temperature_gpu_c      = GetTemperature(GPU)
//
// The mock answers GetMarginTemperature as tlimit.reference_c minus the
// temperature. The pairing is the driver's: it answers SLOWDOWN with the
// T.Limit reference, where field 196 reads 0, and GPU_MAX with the
// hardware-slowdown point. A board without the T.Limit fields keeps the
// driver's GetTemperatureThreshold answers.

var (
	gpuTemperature    = getter(call("GetTemperature", nvml.TEMPERATURE_GPU))
	slowdownThreshold = getter(call("GetTemperatureThreshold", nvml.TEMPERATURE_THRESHOLD_SLOWDOWN))
	shutdownTLimit    = field(nvml.FI_DEV_TEMPERATURE_SHUTDOWN_TLIMIT)
	slowdownTLimit    = field(nvml.FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT)
	maxOperatingTLim  = field(nvml.FI_DEV_TEMPERATURE_GPU_MAX_TLIMIT)
)

// thermalLimits writes the absolute thresholds and the T.Limit block. temp is
// the temperature_gpu_c written, "" when there is none.
func (g *generator) thermalLimits(t *section, temp string) {
	ref, refWhy := g.gpu0Int(slowdownThreshold)
	shutdown, shutdownWhy := g.gpu0Int(shutdownTLimit)
	slowdown, slowdownWhy := g.gpu0Int(slowdownTLimit)
	why := strings.Join(nonEmpty(refWhy, shutdownWhy, slowdownWhy), "; ")

	for _, th := range []struct {
		key       string
		threshold nvml.TemperatureThresholds
		offsetKey string // "" when the threshold is the reference itself
		offset    int64
	}{
		{"shutdown_threshold_c", nvml.TEMPERATURE_THRESHOLD_SHUTDOWN, "shutdown_c", shutdown},
		{"slowdown_threshold_c", nvml.TEMPERATURE_THRESHOLD_SLOWDOWN, "", 0},
		{"max_operating_c", nvml.TEMPERATURE_THRESHOLD_GPU_MAX, "slowdown_c", slowdown},
	} {
		driver := getter(call("GetTemperatureThreshold", th.threshold))
		if why != "" {
			t.prop(th.key, driver)
			continue
		}
		v, formula := ref, fmt.Sprintf("tlimit.reference_c = %d", ref)
		if th.offsetKey != "" {
			v = ref - th.offset
			formula = fmt.Sprintf("tlimit.reference_c - tlimit.%s = %d - (%d)", th.offsetKey, ref, th.offset)
		}
		t.value(th.key, integer(v), formula+"; "+driver.from+" reads "+onEveryGPU(raws(g.answers(driver))), ruleFromTLimit)
	}

	if why != "" {
		t.m.note("the thresholds above are the driver's, not derived from T.Limit: " + why)
	}
	tlimit := t.sub("tlimit")
	if why != "" {
		tlimit.m.note("reference_c: left out, " + why)
		tlimit.prop("shutdown_c", shutdownTLimit)
		tlimit.prop("slowdown_c", slowdownTLimit)
		tlimit.prop("max_operating_c", maxOperatingTLim)
		return
	}
	tlimit.gpu0("reference_c", slowdownThreshold, false)
	tlimit.gpu0("shutdown_c", shutdownTLimit, false)
	tlimit.gpu0("slowdown_c", slowdownTLimit, false)
	tlimit.gpu0("max_operating_c", maxOperatingTLim, false)
	if temp == "" {
		return
	}
	if n, err := strconv.ParseInt(temp, 10, 64); err == nil {
		note := fmt.Sprintf("margin: the mock answers GetMarginTemperature as tlimit.reference_c - temperature_gpu_c = %d - %d = %d", ref, n, ref-n)
		if m, ok := g.gpu0Raw(getter("GetMarginTemperature", "MarginTemperature")); ok {
			note += "; GPU 0 reads " + m
		}
		tlimit.m.note(note)
	}
}

// gpu0Int reads GPU 0's answer as an integer, or says why there is none.
func (g *generator) gpu0Int(src source) (int64, string) {
	a := g.answers(src)
	if a[0].missing != "" {
		return 0, src.from + " on GPU 0 = " + a[0].missing
	}
	n, err := strconv.ParseInt(a[0].yaml, 10, 64)
	if err != nil {
		return 0, src.from + " on GPU 0 is not an integer: " + a[0].raw
	}
	return n, ""
}

// gpu0Raw is GPU 0's answer as the capture recorded it.
func (g *generator) gpu0Raw(src source) (string, bool) {
	a := g.answers(src)
	return a[0].raw, a[0].missing == ""
}

// onEveryGPU says a value once when every GPU has it, else lists them.
func onEveryGPU(values []string) string {
	if allEqual(values) {
		return values[0] + " on every GPU"
	}
	return strings.Join(values, " ")
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, x := range s {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
