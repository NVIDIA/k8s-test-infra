//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	podresources "k8s.io/kubelet/pkg/apis/podresources/v1"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/assertions"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/cluster"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/config"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/harness"
	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

// Kind mounts /tmp with noexec, so stage the temporary reader in a runnable directory.
const numaPodResourcesProbe = "/usr/local/bin/mokka-numa-podresources-e2e"

func swappedGPUProfile(raw string) (string, error) {
	var profile map[string]any
	if err := yaml.Unmarshal([]byte(raw), &profile); err != nil {
		return "", err
	}
	topology, ok := profile["pcie_topology"].(map[string]any)
	if !ok {
		return "", errors.New("profile has no PCI topology")
	}
	roots, ok := topology["root_complexes"].([]any)
	if !ok || len(roots) != 2 {
		return "", errors.New("GPU swap requires two PCI root complexes")
	}
	for _, rawRoot := range roots {
		root, ok := rawRoot.(map[string]any)
		if !ok {
			return "", errors.New("invalid PCI root complex")
		}
		node, ok := root["numa_node"].(float64)
		if !ok || (node != 0 && node != 1) {
			return "", errors.New("GPU swap requires NUMA nodes zero and one")
		}
		root["numa_node"] = 1 - node
	}
	data, err := yaml.Marshal(profile)
	return string(data), err
}

func installNUMAPodResourcesProbe(ctx SpecContext, workers []cluster.Node) {
	GinkgoHelper()
	binary := filepath.Join(GinkgoT().TempDir(), "podresources")
	_, err := runner.Run(ctx, "env", "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0", "go", "build", "-tags=e2e", "-o", binary,
		filepath.Join(repoRoot(), "tests/e2e/go/tools/podresources"))
	Expect(err).NotTo(HaveOccurred())
	for _, worker := range workers {
		_, err := runner.Run(ctx, "docker", "cp", binary, worker.Container+":"+numaPodResourcesProbe)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			_, err := runner.Run(ctx, "docker", "exec", worker.Container, "rm", "-f", numaPodResourcesProbe)
			Expect(err).NotTo(HaveOccurred())
		})
	}
}

func readNUMAPodResources(ctx context.Context, worker cluster.Node, out any, args ...string) error {
	command := append([]string{"exec", worker.Container, numaPodResourcesProbe}, args...)
	result, err := runner.RunQuiet(ctx, "docker", command...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(result.Stdout), out)
}

func numaDeviceZones(devices []*podresources.ContainerDevices) map[string][]int64 {
	zones := map[string][]int64{}
	for _, device := range devices {
		if device.ResourceName != "nvidia.com/gpu" {
			continue
		}
		nodes := make([]int64, 0, len(device.GetTopology().GetNodes()))
		for _, node := range device.GetTopology().GetNodes() {
			nodes = append(nodes, node.ID)
		}
		for _, id := range device.DeviceIds {
			zones[id] = nodes
		}
	}
	return zones
}

func a100GPUIdentities(ctx context.Context, h *harness.Harness, worker cluster.Node) map[string]string {
	GinkgoHelper()
	result, err := h.Kube.ExecQuiet(ctx, nvmlPodOnNode(ctx, h, worker.Name), "nvidia-smi", "--query-gpu=uuid,pci.bus_id", "--format=csv,noheader,nounits")
	Expect(err).NotTo(HaveOccurred())
	rows, err := csv.NewReader(strings.NewReader(result.Stdout)).ReadAll()
	Expect(err).NotTo(HaveOccurred())
	Expect(rows).To(HaveLen(8))
	identities := map[string]string{}
	for _, row := range rows {
		Expect(row).To(HaveLen(2))
		bdf := strings.ToLower(strings.TrimSpace(row[1]))
		if len(bdf) == 16 {
			bdf = bdf[4:] // NVML may report an eight-digit PCI domain.
		}
		identities[strings.TrimSpace(row[0])] = bdf
	}
	Expect(identities).To(HaveLen(8))
	return identities
}

func a100GPUZones(identities map[string]string, swapped bool) map[string][]int64 {
	// This is the A100 fixture's original GPU placement, independent of the
	// profile edit, the shared plan, and the observed NRT resource counts.
	placement := map[string]int64{
		"0000:07:00.0": 0, "0000:0f:00.0": 0, "0000:47:00.0": 0, "0000:4e:00.0": 0,
		"0000:87:00.0": 1, "0000:90:00.0": 1, "0000:b7:00.0": 1, "0000:bd:00.0": 1,
	}
	want := map[string][]int64{}
	for uuid, bdf := range identities {
		node, ok := placement[bdf]
		Expect(ok).To(BeTrue(), "unexpected GPU PCI address %s", bdf)
		if swapped {
			node = 1 - node
		}
		want[uuid] = []int64{node}
	}
	return want
}

func numaCPUAndMemory(ctx context.Context, worker cluster.Node) map[string]string {
	GinkgoHelper()
	out := map[string]string{}
	for _, node := range []string{"node0", "node1"} {
		for _, name := range []string{"cpulist", "meminfo"} {
			path := "/sys/devices/system/node/" + node + "/" + name
			data, err := numaHostFile(ctx, worker, path)
			Expect(err).NotTo(HaveOccurred())
			out[path] = data
		}
	}
	return out
}

func checkNUMAGPUPlacement(ctx SpecContext, worker cluster.Node, identities map[string]string, swapped bool) {
	GinkgoHelper()
	want := a100GPUZones(identities, swapped)
	Eventually(func() (map[string][]int64, error) {
		var resources podresources.AllocatableResourcesResponse
		if err := readNUMAPodResources(ctx, worker, &resources); err != nil {
			return nil, err
		}
		return numaDeviceZones(resources.Devices), nil
	}).WithContext(ctx).WithTimeout(config.ReadyTimeout()).WithPolling(config.PollInterval()).Should(Equal(want), "the real kubelet must report the new NUMA node for each GPU UUID")
	for uuid, bdf := range identities {
		got, err := numaHostFile(ctx, worker, "/var/lib/nvml-mock/sys/bus/pci/devices/"+bdf+"/numa_node")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(strconv.FormatInt(want[uuid][0], 10)), bdf)
	}
}

func checkNUMAGPUAllocation(ctx SpecContext, h *harness.Harness, worker cluster.Node, want assertions.NUMAExpectation, identities map[string]string, swapped bool, name string) {
	GinkgoHelper()
	manifest, baseline, allocated := applyNUMAOneGPUWorkload(ctx, h, want, name)
	var resources podresources.ListPodResourcesResponse
	Expect(readNUMAPodResources(ctx, worker, &resources, "--list")).To(Succeed())
	var devices map[string][]int64
	for _, pod := range resources.PodResources {
		if pod.Namespace == "default" && pod.Name == name {
			Expect(pod.Containers).To(HaveLen(1))
			devices = numaDeviceZones(pod.Containers[0].Devices)
		}
	}
	Expect(devices).To(HaveLen(1))
	expected := a100GPUZones(identities, swapped)
	for uuid, nodes := range devices {
		Expect(nodes).To(Equal(expected[uuid]), "the assigned GPU must use its new zone")
		zone := "node-" + strconv.FormatInt(nodes[0], 10)
		Expect(assertions.NUMAAllocationProblems(numaSingleZone(baseline, zone), numaSingleZone(allocated, zone),
			corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(numaWorkloadCPU), corev1.ResourceMemory: resource.MustParse(numaWorkloadMemory),
				"nvidia.com/gpu": resource.MustParse("1"),
			})).To(BeEmpty(), "NFD must account for CPU, memory, and the selected GPU in the GPU's reported zone")
	}
	Expect(h.Kube.Delete(ctx, manifest)).To(Succeed())
	waitNUMATopology(ctx, h, worker.Name, func(nrt assertions.NodeResourceTopology) []string {
		return assertions.NUMAAvailabilityProblems(baseline, nrt)
	})
}

func numaSingleZone(topology assertions.NodeResourceTopology, name string) assertions.NodeResourceTopology {
	for _, zone := range topology.Zones {
		if zone.Name == name {
			return assertions.NodeResourceTopology{Zones: []assertions.NUMAResourceZone{zone}}
		}
	}
	return assertions.NodeResourceTopology{}
}

func checkNUMAGPUSwap(ctx SpecContext, h *harness.Harness, workers []cluster.Node, expected map[string]assertions.NUMAExpectation) {
	GinkgoHelper()
	installNUMAPodResourcesProbe(ctx, workers)
	original, err := h.Kube.ConfigMapData(ctx, nvmlMockNamespace, "nvml-mock-config", "config.yaml")
	Expect(err).NotTo(HaveOccurred())
	swapped, err := swappedGPUProfile(original)
	Expect(err).NotTo(HaveOccurred())
	agents := numaAgentIdentities(ctx, h)
	markers := waitNUMALayout(ctx, h, workers, "0-1", nil)
	identities := map[string]map[string]string{}
	capacity := map[string]map[string]string{}
	for _, worker := range workers {
		identities[worker.Name] = a100GPUIdentities(ctx, h, worker)
		capacity[worker.Name] = numaCPUAndMemory(ctx, worker)
		checkNUMAGPUPlacement(ctx, worker, identities[worker.Name], false)
	}
	restored := false
	DeferCleanup(func(ctx SpecContext) {
		if !restored {
			patchNUMAProfile(ctx, h, original)
			waitNUMALayout(ctx, h, workers, "0-1", nil)
			refreshNUMAConsumers(ctx, h, workers)
		}
	})
	for i, phase := range []struct {
		profile string
		swapped bool
	}{{swapped, true}, {original, false}} {
		By(fmt.Sprintf("updating only GPU placement, swapped=%t", phase.swapped))
		patchNUMAProfile(ctx, h, phase.profile)
		markers = waitNUMALayout(ctx, h, workers, "0-1", markers)
		Expect(numaAgentIdentities(ctx, h)).To(Equal(agents))
		refreshNUMAConsumers(ctx, h, workers)
		for _, worker := range workers {
			Expect(numaCPUAndMemory(ctx, worker)).To(Equal(capacity[worker.Name]), "CPU and memory layout must not change")
			Expect(a100GPUIdentities(ctx, h, worker)).To(Equal(identities[worker.Name]))
			checkNUMAGPUPlacement(ctx, worker, identities[worker.Name], phase.swapped)
			waitNUMATopology(ctx, h, worker.Name, func(nrt assertions.NodeResourceTopology) []string {
				return assertions.NUMATopologyProblems(nrt, expected[worker.Name])
			})
		}
		worker := workers[0]
		checkNUMAGPUAllocation(ctx, h, worker, expected[worker.Name], identities[worker.Name], phase.swapped, fmt.Sprintf("numa-gpu-swap-%d", i))
	}
	restored = true
}
