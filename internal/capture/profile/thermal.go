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

const ruleFromTLimit = "from the T.Limit reference and offsets"

// thermalLimits writes the absolute thresholds and the T.Limit view from one
// reference temperature, so the two agree in the mock.
//
// NVML reports the margin to T.Limit as the reference minus the current
// temperature, so their sum on a GPU is the reference. The two calls are read
// at different moments and rounded to whole degrees, so the sums can differ
// by a degree between GPUs; the highest is the reference. The T.Limit offsets
// are field values, and the absolute thresholds follow from them:
//
//	slowdown_threshold_c = tlimit.reference_c
//	max_operating_c      = tlimit.reference_c - tlimit.slowdown_c
//	shutdown_threshold_c = tlimit.reference_c - tlimit.shutdown_c
//
// A board without a T.Limit view keeps the driver's GetTemperatureThreshold
// answers.
func (g *generator) thermalLimits(t *section) {
	ref, working, refWhy := g.tlimitReferenceMax()
	shutdown, shutdownWhy := g.sameInt(field(nvml.FI_DEV_TEMPERATURE_SHUTDOWN_TLIMIT))
	slowdown, slowdownWhy := g.sameInt(field(nvml.FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT))
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
	if refWhy == "" {
		tlimit.value("reference_c", integer(ref),
			"max of GetTemperature(0)+GetMarginTemperature.MarginTemperature per GPU: "+working, "max of the GPUs")
	} else {
		tlimit.m.note("reference_c: left out, " + refWhy)
	}
	tlimit.prop("shutdown_c", field(nvml.FI_DEV_TEMPERATURE_SHUTDOWN_TLIMIT))
	tlimit.prop("slowdown_c", field(nvml.FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT))
	tlimit.prop("max_operating_c", field(nvml.FI_DEV_TEMPERATURE_GPU_MAX_TLIMIT))
}

// tlimitReferenceMax is the highest per-GPU sum of temperature and margin,
// with each GPU's working ("34+53=87").
func (g *generator) tlimitReferenceMax() (int64, string, string) {
	var best int64
	workings := make([]string, g.c.gpuCount())
	for i := range workings {
		v, w, why := g.tlimitReference(i)
		if why != "" {
			return 0, "", "GPU " + itoa(i) + ": " + why
		}
		workings[i] = w + "=" + strconv.FormatInt(v, 10)
		if i == 0 || v > best {
			best = v
		}
	}
	return best, strings.Join(workings, " "), ""
}

func (g *generator) tlimitReference(gpu int) (int64, string, string) {
	temp, why := recorded(g.c, gpu, call("GetTemperature", nvml.TEMPERATURE_GPU), nil)
	if why != "" {
		return 0, "", "GetTemperature(0) = " + why
	}
	margin, why := recorded(g.c, gpu, "GetMarginTemperature", []any{"MarginTemperature"})
	if why != "" {
		return 0, "", "GetMarginTemperature = " + why
	}
	t, okT := asInt(temp)
	m, okM := asInt(margin)
	if !okT || !okM {
		return 0, "", "unreadable temperature or margin"
	}
	return t + m, fmt.Sprintf("%d+%d", t, m), ""
}

// sameInt reads an integer every GPU answers alike.
func (g *generator) sameInt(src source) (int64, string) {
	a := g.answers(src)
	for _, x := range a {
		if x.missing != "" {
			return 0, src.from + " = " + x.missing
		}
	}
	if !allSame(a) {
		return 0, src.from + " differs between GPUs: " + strings.Join(raws(a), " ")
	}
	n, err := strconv.ParseInt(a[0].yaml, 10, 64)
	if err != nil {
		return 0, src.from + " is not an integer: " + a[0].raw
	}
	return n, ""
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
