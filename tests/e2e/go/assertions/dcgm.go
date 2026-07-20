//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// dcgm-exporter field names asserted here.
const (
	dcgmExporterSelector  = "app=nvidia-dcgm-exporter"
	dcgmExporterDaemonSet = "nvidia-dcgm-exporter"
	dcgmExporterPort      = 9400

	fiDevGPUTemp    = "DCGM_FI_DEV_GPU_TEMP"
	fiDevPowerUsage = "DCGM_FI_DEV_POWER_USAGE"
	fiDevXidErrors  = "DCGM_FI_DEV_XID_ERRORS"
	fiProfPCIeTx    = "DCGM_FI_PROF_PCIE_TX_BYTES"
)

// DCGMDeviceMetrics asserts dcgm-exporter telemetry from the mock NVML:
//   - one DCGM_FI_DEV_GPU_TEMP sample per GPU, each in a plausible range;
//   - positive DCGM_FI_DEV_POWER_USAGE for every GPU;
//   - a modelName label matching the profile's display name;
//   - on GPM (Hopper+) profiles, one positive DCGM_FI_PROF_PCIE_TX_BYTES per GPU;
//   - power that varies over time.
func DCGMDeviceMetrics(ctx context.Context, k *kube.Client, ns, gpuName string, gpuCount int, gpm bool, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()

	WaitDaemonSetReady(ctx, k, ns, dcgmExporterDaemonSet, timeout, poll)
	pod := dcgmExporterPod(ctx, k, ns)

	// Poll until the first DEV temperature samples appear.
	var metrics string
	ginkgo.By("scraping dcgm-exporter /metrics")
	gomega.Eventually(func() (int, error) {
		var err error
		metrics, err = scrapeDCGM(ctx, k, ns, pod)
		if err != nil {
			return 0, err
		}
		return len(promValues(metrics, fiDevGPUTemp)), nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeNumerically(">", 0), "no %s samples from dcgm-exporter", fiDevGPUTemp)

	temps := promValues(metrics, fiDevGPUTemp)
	gomega.Expect(temps).To(gomega.HaveLen(gpuCount), "%s sample count", fiDevGPUTemp)
	for _, t := range temps {
		gomega.Expect(t).To(gomega.And(gomega.BeNumerically(">=", 20), gomega.BeNumerically("<=", 100)),
			"%s value %.1f outside plausible range [20,100]", fiDevGPUTemp, t)
	}

	powers := promValues(metrics, fiDevPowerUsage)
	gomega.Expect(powers).NotTo(gomega.BeEmpty(), "%s missing", fiDevPowerUsage)
	for _, p := range powers {
		gomega.Expect(p).To(gomega.BeNumerically(">", 0), "%s not positive", fiDevPowerUsage)
	}

	gomega.Expect(metrics).To(gomega.ContainSubstring(fmt.Sprintf("modelName=%q", gpuName)),
		"modelName label %q not found in dcgm-exporter metrics", gpuName)

	// GPM profiling metrics; present only on Hopper+ profiles.
	if gpm {
		ginkgo.By("checking GPM profiling metrics (DCGM_FI_PROF_*)")
		gomega.Eventually(func() (int, error) {
			var err error
			metrics, err = scrapeDCGM(ctx, k, ns, pod)
			if err != nil {
				return 0, err
			}
			return len(promValues(metrics, fiProfPCIeTx)), nil
		}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
			Should(gomega.Equal(gpuCount), "%s sample count (GPM path)", fiProfPCIeTx)
		for _, v := range promValues(metrics, fiProfPCIeTx) {
			gomega.Expect(v).To(gomega.BeNumerically(">", 0), "%s not positive", fiProfPCIeTx)
		}
	}

	// Power must differ across two scrapes (dynamic metrics).
	ginkgo.By("checking DCGM_FI_DEV_POWER_USAGE varies over time")
	baseline := promRaw(metrics, fiDevPowerUsage)
	gomega.Expect(baseline).NotTo(gomega.BeEmpty(), "%s not present for time-varying check", fiDevPowerUsage)
	gomega.Eventually(func() (bool, error) {
		cur, err := scrapeDCGM(ctx, k, ns, pod)
		if err != nil {
			return false, err
		}
		return !slices.Equal(promRaw(cur, fiDevPowerUsage), baseline), nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeTrue(), "%s did not vary over time", fiDevPowerUsage)
}

// DCGMXidReported polls until at least one GPU reports xid as
// DCGM_FI_DEV_XID_ERRORS (healthy default is 0).
func DCGMXidReported(ctx context.Context, k *kube.Client, ns string, xid int, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()

	WaitDaemonSetReady(ctx, k, ns, dcgmExporterDaemonSet, timeout, poll)
	pod := dcgmExporterPod(ctx, k, ns)

	ginkgo.By(fmt.Sprintf("waiting for %s == %d", fiDevXidErrors, xid))
	gomega.Eventually(func() (bool, error) {
		metrics, err := scrapeDCGM(ctx, k, ns, pod)
		if err != nil {
			return false, err
		}
		for _, v := range promValues(metrics, fiDevXidErrors) {
			if int(v) == xid {
				return true, nil
			}
		}
		return false, nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeTrue(), "%s did not report injected Xid %d", fiDevXidErrors, xid)
}

// DCGMXidReportedForGPU polls until dcgm-exporter reports the injected xid for
// the target GPU index via DCGM_FI_DEV_XID_ERRORS, then asserts every other GPU
// stays at xid 0. It never restarts dcgm-exporter, so it validates that a
// runtime, single-GPU failure injection (via nvml-mock-ctl) propagates to an
// already-running consumer through the bind-mounted overlay.
func DCGMXidReportedForGPU(ctx context.Context, k *kube.Client, ns string, targetGPU, xid int, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()

	WaitDaemonSetReady(ctx, k, ns, dcgmExporterDaemonSet, timeout, poll)
	pod := dcgmExporterPod(ctx, k, ns)

	ginkgo.By(fmt.Sprintf("waiting for %s == %d on GPU %d (runtime injection, no restart)", fiDevXidErrors, xid, targetGPU))
	var byGPU map[int]float64
	gomega.Eventually(func() (bool, error) {
		metrics, err := scrapeDCGM(ctx, k, ns, pod)
		if err != nil {
			return false, err
		}
		byGPU = promValuesByGPU(metrics, fiDevXidErrors)
		v, ok := byGPU[targetGPU]
		return ok && int(v) == xid, nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeTrue(), "%s did not report xid %d on GPU %d (last scrape: %v)", fiDevXidErrors, xid, targetGPU, byGPU)

	ginkgo.By("confirming the failure is scoped to the target GPU")
	for gpu, v := range byGPU {
		if gpu == targetGPU {
			continue
		}
		gomega.Expect(int(v)).To(gomega.Equal(0),
			"GPU %d reported xid %d but only GPU %d was targeted", gpu, int(v), targetGPU)
	}
}

// DCGMTempReportedForGPU polls until dcgm-exporter reports wantC as
// DCGM_FI_DEV_GPU_TEMP for the target GPU, then asserts every other GPU keeps a
// different (simulator-driven) reading. It never restarts dcgm-exporter, so it
// validates that a runtime, single-GPU temperature pin (via nvml-mock-ctl)
// propagates to an already-running consumer through the bind-mounted overlay.
func DCGMTempReportedForGPU(ctx context.Context, k *kube.Client, ns string, targetGPU, wantC int, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	// GPU temperature is a whole-degree integer in the mock; a 0.5 tolerance
	// asserts an exact match while staying float-formatting agnostic.
	dcgmGaugeReportedForGPU(ctx, k, ns, fiDevGPUTemp, targetGPU, float64(wantC), 0.5, timeout, poll)
}

// DCGMPowerReportedForGPU polls until dcgm-exporter reports ~wantW as
// DCGM_FI_DEV_POWER_USAGE (watts) for the target GPU, then asserts every other
// GPU keeps a different (simulator-driven) reading. Like the temperature
// variant it never restarts dcgm-exporter, validating that a runtime power pin
// (via nvml-mock-ctl) reaches an already-running consumer through the overlay.
func DCGMPowerReportedForGPU(ctx context.Context, k *kube.Client, ns string, targetGPU, wantW int, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()
	// dcgm-exporter reports power in watts; the mock has zero variance under a
	// pin, so a 1W tolerance only absorbs float formatting.
	dcgmGaugeReportedForGPU(ctx, k, ns, fiDevPowerUsage, targetGPU, float64(wantW), 1, timeout, poll)
}

// dcgmGaugeReportedForGPU polls until dcgm-exporter reports a value within tol
// of want for the target GPU via metric, then asserts every other GPU differs
// from want by more than tol (i.e. the change is scoped to the target). It
// never restarts dcgm-exporter, validating runtime-overlay propagation to an
// already-running consumer.
func dcgmGaugeReportedForGPU(ctx context.Context, k *kube.Client, ns, metric string, targetGPU int, want, tol float64, timeout, poll time.Duration) {
	ginkgo.GinkgoHelper()

	WaitDaemonSetReady(ctx, k, ns, dcgmExporterDaemonSet, timeout, poll)
	pod := dcgmExporterPod(ctx, k, ns)

	ginkgo.By(fmt.Sprintf("waiting for %s ~= %g on GPU %d (runtime change, no restart)", metric, want, targetGPU))
	var byGPU map[int]float64
	gomega.Eventually(func() (bool, error) {
		metrics, err := scrapeDCGM(ctx, k, ns, pod)
		if err != nil {
			return false, err
		}
		byGPU = promValuesByGPU(metrics, metric)
		v, ok := byGPU[targetGPU]
		return ok && math.Abs(v-want) <= tol, nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(poll).
		Should(gomega.BeTrue(), "%s did not report ~%g on GPU %d (last scrape: %v)", metric, want, targetGPU, byGPU)

	ginkgo.By("confirming the change is scoped to the target GPU")
	for gpu, v := range byGPU {
		if gpu == targetGPU {
			continue
		}
		gomega.Expect(math.Abs(v-want)).To(gomega.BeNumerically(">", tol),
			"GPU %d also reported ~%g but only GPU %d was targeted (value %g)", gpu, want, targetGPU, v)
	}
}

func dcgmExporterPod(ctx context.Context, k *kube.Client, ns string) string {
	ginkgo.GinkgoHelper()
	pod, err := k.FirstPodName(ctx, ns, dcgmExporterSelector)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "find dcgm-exporter pod")
	gomega.Expect(pod).NotTo(gomega.BeEmpty(), "dcgm-exporter pod not found in %s", ns)
	return pod
}

// scrapeDCGM fetches the exporter's Prometheus text via the API-server pod proxy.
func scrapeDCGM(ctx context.Context, k *kube.Client, ns, pod string) (string, error) {
	raw := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy/metrics", ns, pod, dcgmExporterPort)
	return k.GetRawQuiet(ctx, raw)
}

// promRaw returns the value token (last field) of every `metric{...}` series.
func promRaw(metrics, metric string) []string {
	prefix := metric + "{"
	var out []string
	for _, line := range strings.Split(metrics, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if fields := strings.Fields(line); len(fields) >= 2 {
			out = append(out, fields[len(fields)-1])
		}
	}
	return out
}

// promValues is promRaw parsed to float64 (unparseable tokens dropped).
func promValues(metrics, metric string) []float64 {
	raw := promRaw(metrics, metric)
	out := make([]float64, 0, len(raw))
	for _, s := range raw {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// promValuesByGPU returns the value of every `metric{...}` series keyed by the
// dcgm-exporter `gpu="N"` index label. Series without a parseable gpu label or
// value are skipped.
func promValuesByGPU(metrics, metric string) map[int]float64 {
	prefix := metric + "{"
	out := map[int]float64{}
	for _, line := range strings.Split(metrics, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		gpu, ok := labelInt(line, "gpu")
		if !ok {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			out[gpu] = v
		}
	}
	return out
}

// labelInt extracts an integer Prometheus label value (key="N"), matching only
// at a label boundary ({ or ,) so e.g. "gpu" never matches inside another key.
func labelInt(line, key string) (int, bool) {
	for _, sep := range []string{"{", ","} {
		needle := sep + key + `="`
		i := strings.Index(line, needle)
		if i < 0 {
			continue
		}
		rest := line[i+len(needle):]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			continue
		}
		if n, err := strconv.Atoi(rest[:j]); err == nil {
			return n, true
		}
	}
	return 0, false
}
