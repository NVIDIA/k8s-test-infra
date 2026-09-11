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

package mockctl

// Recording a MIG mutation that came in through NVML, so it outlives the
// process that made it: a second nvidia-smi has to see the instances the first
// one created.
//
// These writers edit the explicit layout under `mig.instances` and nothing
// else. The layout they edit is authoritative, so a caller mutating a device
// whose instances came from the profile's declared counts has to record the
// whole layout before it can add to or delete from it — there is no board here
// to re-derive the counts against.

import (
	"fmt"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// mutateMIG applies fn to the device's recorded MIG state under the override
// lock, re-reading the document inside the lock so a concurrent writer's work
// is never overwritten by a view loaded before it ran. This is the shape
// ResetDevice uses.
func mutateMIG(path string, index int, fn func(*engine.MIGConfig)) error {
	if path == "" {
		// No override file resolved: nothing to persist to, and the in-memory
		// mutation the caller already made stands on its own.
		return nil
	}
	unlock, err := LockOverride(path)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlock()

	doc, err := Load(path)
	if err != nil {
		return err
	}
	bucket := doc.bucket(Target{Index: index})
	block, err := migBlock(bucket)
	if err != nil {
		return err
	}
	mig, err := migFromBlock(block)
	if err != nil {
		return err
	}
	fn(mig)
	if err := writeMIG(block, mig); err != nil {
		return err
	}
	bucket["mig"] = block
	return WriteAtomic(path, doc)
}

// migBlock returns the target's mig block as the generic map the document
// holds, so the keys a mutation does not own stay exactly as written.
func migBlock(bucket map[string]any) (map[string]any, error) {
	raw, ok := bucket["mig"]
	if !ok || raw == nil {
		return map[string]any{}, nil
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mig override is %T, want a block of fields", raw)
	}
	return block, nil
}

// migFromBlock decodes the block into the typed config, so the mutations below
// work on fields rather than untyped maps. Unknown keys are tolerated: they
// belong to whoever wrote them and travel in the generic block.
func migFromBlock(block map[string]any) (*engine.MIGConfig, error) {
	data, err := yaml.Marshal(block)
	if err != nil {
		return nil, fmt.Errorf("encoding mig override: %w", err)
	}
	var mig engine.MIGConfig
	if err := yaml.Unmarshal(data, &mig); err != nil {
		return nil, fmt.Errorf("parsing mig override: %w", err)
	}
	return &mig, nil
}

// writeMIG copies back only the fields a runtime mutation owns. Re-encoding
// the whole typed config instead would drop every key it models as omitempty
// but the block sets to zero, and the engine deep-merges this block over the
// profile — so a dropped `max_gpu_instances: 0` would read as "inherit" and
// silently restore the value the operator overrode.
func writeMIG(block map[string]any, mig *engine.MIGConfig) error {
	setOrClear(block, "mode_current", mig.ModeCurrent)
	setOrClear(block, "mode_pending", mig.ModePending)

	if mig.Instances == nil {
		delete(block, "instances")
		return nil
	}
	raw, err := yaml.Marshal(*mig.Instances)
	if err != nil {
		return fmt.Errorf("encoding mig instances: %w", err)
	}
	// Decoded back to a generic list so the block stays uniformly generic, and
	// so an emptied layout is written as `instances: []` rather than dropped.
	instances := []any{}
	if err := yaml.Unmarshal(raw, &instances); err != nil {
		return fmt.Errorf("re-reading mig instances: %w", err)
	}
	block["instances"] = instances
	return nil
}

// setOrClear writes a mode field, removing the key when the mutation left it
// unset: an empty string would merge over the profile's mode as a value.
func setOrClear(block map[string]any, key, value string) {
	if value == "" {
		delete(block, key)
		return
	}
	block[key] = value
}

// MIGSetMode records a MIG mode change. Disabling clears the recorded layout,
// because disabling MIG on hardware destroys every instance.
func MIGSetMode(path string, index int, enabled bool) error {
	return mutateMIG(path, index, func(mig *engine.MIGConfig) {
		if !enabled {
			mig.ModeCurrent, mig.ModePending = "disabled", "disabled"
			mig.Instances = nil
			return
		}
		mig.ModeCurrent, mig.ModePending = "enabled", "enabled"
	})
}

// MIGAddGpuInstance records a newly created GPU instance. The record's compute
// instances are kept as the caller passed them, empty list included: that is
// how `nvidia-smi mig -cgi` without -C differs from an unspecified layout.
func MIGAddGpuInstance(path string, index int, rec engine.MIGGPUInstanceRecord) error {
	return mutateMIG(path, index, func(mig *engine.MIGConfig) {
		mig.Instances = ptr(append(instancesOf(mig), rec))
	})
}

// MIGRemoveGpuInstance records a destroyed GPU instance. The list stays
// present when it empties: a MIG-enabled board with nothing on it is a real
// state, and an absent list would fall back to the profile's partitions.
func MIGRemoveGpuInstance(path string, index int, giID uint32) error {
	return mutateMIG(path, index, func(mig *engine.MIGConfig) {
		kept := make([]engine.MIGGPUInstanceRecord, 0, len(instancesOf(mig)))
		for _, gi := range instancesOf(mig) {
			if gi.ID != giID {
				kept = append(kept, gi)
			}
		}
		mig.Instances = &kept
	})
}

// MIGAddComputeInstance records a compute instance created inside giID.
func MIGAddComputeInstance(path string, index int, giID uint32, rec engine.MIGComputeInstanceRecord) error {
	return mutateMIG(path, index, func(mig *engine.MIGConfig) {
		list := instancesOf(mig)
		for i, gi := range list {
			if gi.ID == giID {
				list[i].ComputeInstances = ptr(append(computeInstancesOf(gi), rec))
			}
		}
	})
}

// MIGRemoveComputeInstance records a destroyed compute instance.
func MIGRemoveComputeInstance(path string, index int, giID, ciID uint32) error {
	return mutateMIG(path, index, func(mig *engine.MIGConfig) {
		list := instancesOf(mig)
		for i, gi := range list {
			if gi.ID != giID {
				continue
			}
			cis := computeInstancesOf(gi)
			kept := make([]engine.MIGComputeInstanceRecord, 0, len(cis))
			for _, ci := range cis {
				if ci.ID != ciID {
					kept = append(kept, ci)
				}
			}
			// Stays present when it empties, for the same reason the GPU
			// instance list does: an instance with no compute instances is a
			// real state, not an unspecified one.
			list[i].ComputeInstances = &kept
		}
	})
}

// instancesOf reads the recorded layout, treating an absent list as empty. The
// slice aliases the record, so edits to its elements are edits to the layout.
func instancesOf(mig *engine.MIGConfig) []engine.MIGGPUInstanceRecord {
	if mig.Instances == nil {
		return nil
	}
	return *mig.Instances
}

// computeInstancesOf reads one record's compute instances, treating an absent
// list as empty.
func computeInstancesOf(gi engine.MIGGPUInstanceRecord) []engine.MIGComputeInstanceRecord {
	if gi.ComputeInstances == nil {
		return nil
	}
	return *gi.ComputeInstances
}

func ptr[T any](v T) *T { return &v }
