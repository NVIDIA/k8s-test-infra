// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// MIG persistence across the C ABI: a mutation an NVML consumer makes has to
// land in the override document before the call returns, because the library's
// engine dies with the process that loaded it.
//
// This is asserted from the document on disk rather than from a second process
// because the document is what a second process would read: the engine treats
// it as authoritative and watches it for changes.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"gopkg.in/yaml.v3"
)

// The slice of the override schema this leg reads back, declared here rather
// than shared with the library: the harness links only go-nvml, so it reads
// the document the way a separate consumer does, with no Go types in common to
// agree with the writer by construction.
type (
	migPersistDoc struct {
		Devices map[string]migPersistDevice `yaml:"devices"`
	}
	migPersistDevice struct {
		MIG *migPersistState `yaml:"mig"`
	}
	migPersistState struct {
		ModeCurrent string `yaml:"mode_current"`
		// A pointer, because an absent list and an empty one mean different
		// things: no recorded layout at all, versus a MIG-enabled board with
		// every instance destroyed.
		Instances *[]migPersistInstance `yaml:"instances"`
	}
	migPersistInstance struct {
		ID               uint32                       `yaml:"id"`
		ComputeInstances *[]migPersistComputeInstance `yaml:"compute_instances"`
	}
	migPersistComputeInstance struct {
		ID uint32 `yaml:"id"`
	}
)

// migOverridePath resolves the document the library records into, the way the
// library itself resolves it.
func migOverridePath() string {
	if path := os.Getenv("MOCK_NVML_OVERRIDES"); path != "" {
		return path
	}
	if config := os.Getenv("MOCK_NVML_CONFIG"); config != "" {
		return filepath.Join(filepath.Dir(config), "overrides.yaml")
	}
	return ""
}

// testMIGPersistence partitions a device through the ABI and reads back the
// record each mutation left behind.
//
// Device 1 rather than device 0: the fixture leaves both with MIG off, and
// leaving device 0 to testMIGRuntimeLifecycle keeps the two legs off the same
// board.
func testMIGPersistence() []testResult {
	const index = 1
	name := "mig/persist/gpu1"

	path := migOverridePath()
	if path == "" {
		return []testResult{skippedResult(name, "no override document configured")}
	}

	device, ret := nvml.DeviceGetHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetHandleByIndex: %v", nvml.ErrorString(ret))}}
	}
	if ret, _ := device.SetMigMode(nvml.DEVICE_MIG_ENABLE); ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("SetMigMode(enable): %v", nvml.ErrorString(ret))}}
	}
	// Leave the device as the fixture had it, so test order cannot matter.
	defer device.SetMigMode(nvml.DEVICE_MIG_DISABLE) //nolint:errcheck // best-effort cleanup

	results := []testResult{checkRecordedMigMode(name+"/mode", path, index)}

	giInfo, ret := device.GetGpuInstanceProfileInfo(nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	if ret != nvml.SUCCESS {
		return append(results, testResult{name, false,
			fmt.Sprintf("GetGpuInstanceProfileInfo: %v", nvml.ErrorString(ret))})
	}
	gi, ret := device.CreateGpuInstance(&giInfo)
	if ret != nvml.SUCCESS {
		return append(results, testResult{name, false, fmt.Sprintf("CreateGpuInstance: %v", nvml.ErrorString(ret))})
	}
	info, ret := gi.GetInfo()
	if ret != nvml.SUCCESS {
		return append(results, testResult{name, false, fmt.Sprintf("GpuInstance.GetInfo: %v", nvml.ErrorString(ret))})
	}
	results = append(results, checkRecordedInstance(name+"/create", path, index, info.Id))

	if ret := gi.Destroy(); ret != nvml.SUCCESS {
		return append(results, testResult{name, false, fmt.Sprintf("GpuInstance.Destroy: %v", nvml.ErrorString(ret))})
	}
	return append(results, checkEmptyLayout(name+"/destroy", path, index))
}

func checkRecordedMigMode(name, path string, index int) testResult {
	mig, err := recordedMIGState(path, index)
	if err != nil {
		return testResult{name, false, err.Error()}
	}
	if mig.ModeCurrent != "enabled" {
		return testResult{name, false,
			fmt.Sprintf("recorded mode_current=%q; want \"enabled\"", mig.ModeCurrent)}
	}
	return testResult{name, true, ""}
}

// checkRecordedInstance asserts the created instance is on record under the ID
// NVML handed back, with an explicit empty compute-instance list: a silent one
// asks for the spanning default, so the next process to read the document
// would list a compute instance nobody created.
func checkRecordedInstance(name, path string, index int, wantID uint32) testResult {
	mig, err := recordedMIGState(path, index)
	if err != nil {
		return testResult{name, false, err.Error()}
	}
	if mig.Instances == nil {
		return testResult{name, false, "no instances recorded after CreateGpuInstance"}
	}
	for _, rec := range *mig.Instances {
		if rec.ID != wantID {
			continue
		}
		if rec.ComputeInstances == nil {
			return testResult{name, false,
				fmt.Sprintf("instance %d recorded with no compute_instances list; want an empty one", wantID)}
		}
		if len(*rec.ComputeInstances) != 0 {
			return testResult{name, false,
				fmt.Sprintf("instance %d recorded with %d compute instances; want none",
					wantID, len(*rec.ComputeInstances))}
		}
		return testResult{name, true, ""}
	}
	return testResult{name, false, fmt.Sprintf("instance %d is not among the %d recorded",
		wantID, len(*mig.Instances))}
}

// checkEmptyLayout asserts a board whose last instance was destroyed records
// an empty layout rather than an absent one, which would instead mean "take
// the partitioning from the profile" and bring the instance back.
func checkEmptyLayout(name, path string, index int) testResult {
	mig, err := recordedMIGState(path, index)
	if err != nil {
		return testResult{name, false, err.Error()}
	}
	if mig.Instances == nil {
		return testResult{name, false, "the recorded layout is absent after the last destroy; want an empty list"}
	}
	if len(*mig.Instances) != 0 {
		return testResult{name, false,
			fmt.Sprintf("%d instances still recorded after the destroy", len(*mig.Instances))}
	}
	return testResult{name, true, ""}
}

// recordedMIGState reads the device's recorded MIG state straight off disk.
func recordedMIGState(path string, index int) (*migPersistState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc migPersistDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	key := strconv.Itoa(index)
	entry, ok := doc.Devices[key]
	if !ok {
		return nil, fmt.Errorf("%s records no device %s", path, key)
	}
	if entry.MIG == nil {
		return nil, fmt.Errorf("%s records no mig block for device %s", path, key)
	}
	return entry.MIG, nil
}
