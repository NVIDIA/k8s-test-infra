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

// The MIG mutations, paired with the record that makes them outlive the
// process performing them.
//
// The engine is a per-process singleton built from the config, so a partition
// `nvidia-smi mig -cgi` creates lives exactly as long as that nvidia-smi. Each
// mutation is therefore written into the override document as it happens — the
// same route nvmlDeviceReset already takes, through the same writers
// nvml-mock-ctl uses — and the engine's own watch on that document is what
// carries the change into processes already running.
//
// This file holds no C types so that the pairing can be tested in Go; the
// exports in mig.go are the ABI adapters over it.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// migSetMode switches MIG on or off and records the new mode, reporting the
// call's result and the activation status NVML returns alongside it.
//
// A mode change owns the whole layout rather than editing it — disabling MIG
// destroys every instance — so unlike the instance mutations it seeds no
// baseline: there is nothing for it to take a difference against.
func migSetMode(deviceHandle unsafe.Pointer, mode int) (nvml.Return, nvml.Return) {
	dev := engine.GetEngine().LookupConfigurableDevice(deviceHandle)
	if dev == nil {
		return nvml.ERROR_INVALID_ARGUMENT, nvml.ERROR_INVALID_ARGUMENT
	}
	const what = "MIG mode change"
	path, index := migPersistTarget(dev)
	if !migWritable(path) {
		debugLog("[MIG] device %d: cannot record a %s at %q\n", index, what, path)
		return nvml.ERROR_NO_PERMISSION, nvml.ERROR_NO_PERMISSION
	}

	ret, activation := dev.SetMigMode(mode)
	if ret != nvml.SUCCESS {
		return ret, activation
	}
	if err := mockctl.MIGSetMode(path, index, mode == nvml.DEVICE_MIG_ENABLE); err != nil {
		return migPersistFailed(dev, what, err), activation
	}
	return nvml.SUCCESS, activation
}

// migCreateGpuInstance creates a GPU instance and records it, returning the
// engine's handle for it. A nil placement asks the engine to choose one.
func migCreateGpuInstance(
	deviceHandle unsafe.Pointer, profileID int, placement *nvml.GpuInstancePlacement,
) (unsafe.Pointer, nvml.Return) {
	const what = "GPU instance create"
	dev := engine.GetEngine().LookupConfigurableDevice(deviceHandle)
	if dev == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	path, index, ret := migPersistBaseline(dev, what)
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	handle, ret := engine.GetEngine().DeviceCreateGpuInstance(deviceHandle, profileID, placement)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	info, _, ret := engine.GetEngine().GpuInstanceGetInfo(handle)
	if ret != nvml.SUCCESS {
		return handle, migPersistFailed(dev, what, fmt.Errorf("reading instance info: %w", ret))
	}
	// The profile is recorded by id rather than by name because that is what
	// the caller supplied; re-deriving a name here could pick a different
	// spelling from the one the profile tables use.
	profile := int(info.ProfileId)
	placementStart := int(info.Placement.Start)
	rec := engine.MIGGPUInstanceRecord{
		ID:             info.Id,
		ProfileID:      &profile,
		PlacementStart: &placementStart,
		// NVML's create never brings compute instances with it, so the record
		// has to say "none" rather than stay silent: silence is what asks for
		// the spanning default, and the next process would otherwise list a
		// compute instance nobody created.
		ComputeInstances: &[]engine.MIGComputeInstanceRecord{},
	}
	if err := mockctl.MIGAddGpuInstance(path, index, rec); err != nil {
		return handle, migPersistFailed(dev, what, err)
	}
	return handle, nvml.SUCCESS
}

// migDestroyGpuInstance tears a GPU instance down and unrecords it.
func migDestroyGpuInstance(handle unsafe.Pointer) nvml.Return {
	const what = "GPU instance destroy"
	// The owner and the id are read while the instance still exists: once it
	// is gone, neither the handle nor the instance tree can name them.
	dev, info, ret := migGpuInstanceOwner(handle)
	if ret != nvml.SUCCESS {
		return ret
	}
	path, index, ret := migPersistBaseline(dev, what)
	if ret != nvml.SUCCESS {
		return ret
	}

	if ret := engine.GetEngine().GpuInstanceDestroy(handle); ret != nvml.SUCCESS {
		return ret
	}
	if err := mockctl.MIGRemoveGpuInstance(path, index, info.Id); err != nil {
		return migPersistFailed(dev, what, err)
	}
	return nvml.SUCCESS
}

// migCreateComputeInstance creates a compute instance inside a GPU instance
// and records it under that instance. A nil placement asks the engine to
// choose one.
func migCreateComputeInstance(
	giHandle unsafe.Pointer, profileID int, placement *nvml.ComputeInstancePlacement,
) (unsafe.Pointer, nvml.Return) {
	const what = "compute instance create"
	dev, giInfo, ret := migGpuInstanceOwner(giHandle)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	path, index, ret := migPersistBaseline(dev, what)
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	handle, ret := engine.GetEngine().GpuInstanceCreateComputeInstance(giHandle, profileID, placement)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	info, _, _, ret := engine.GetEngine().ComputeInstanceGetInfo(handle)
	if ret != nvml.SUCCESS {
		return handle, migPersistFailed(dev, what, fmt.Errorf("reading instance info: %w", ret))
	}
	profile := int(info.ProfileId)
	rec := engine.MIGComputeInstanceRecord{ID: info.Id, ProfileID: &profile}
	if err := mockctl.MIGAddComputeInstance(path, index, giInfo.Id, rec); err != nil {
		return handle, migPersistFailed(dev, what, err)
	}
	return handle, nvml.SUCCESS
}

// migDestroyComputeInstance tears a compute instance down and unrecords it.
func migDestroyComputeInstance(handle unsafe.Pointer) nvml.Return {
	const what = "compute instance destroy"
	// Both ids are read before the teardown, as in migDestroyGpuInstance.
	dev, giID, ciID, ret := migComputeInstanceOwner(handle)
	if ret != nvml.SUCCESS {
		return ret
	}
	path, index, ret := migPersistBaseline(dev, what)
	if ret != nvml.SUCCESS {
		return ret
	}

	if ret := engine.GetEngine().ComputeInstanceDestroy(handle); ret != nvml.SUCCESS {
		return ret
	}
	if err := mockctl.MIGRemoveComputeInstance(path, index, giID, ciID); err != nil {
		return migPersistFailed(dev, what, err)
	}
	return nvml.SUCCESS
}

// migPersistTarget resolves where a MIG mutation on dev must be recorded.
//
// The path comes from the engine so the reader and this writer can never
// disagree about which file is authoritative, and the device is named by its
// physical index rather than the container-visible ordinal, because that is
// what the document is keyed by.
func migPersistTarget(dev *engine.ConfigurableDevice) (string, int) {
	return engine.ConfigOverridePath(), dev.PhysicalIndex()
}

// migWritable reports whether this process can record a MIG mutation.
//
// It is checked before the engine mutates rather than after, because the
// engine is what assigns the instance id: a mutation that cannot be recorded
// has to be refused before it exists, or the process ends up holding an
// instance no other process will ever see. On hardware the same call needs
// root and nvidia-smi reports insufficient permissions, so NO_PERMISSION is
// the faithful answer.
func migWritable(path string) bool {
	if path == "" {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		debugLog("[MIG] cannot create the directory holding %q: %v\n", path, err)
		return false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		debugLog("[MIG] cannot open %q for writing: %v\n", path, err)
		return false
	}
	if err := f.Close(); err != nil {
		// The probe asked a question and has its answer; a close that fails
		// afterwards costs this process a descriptor and nothing else.
		debugLog("[MIG] closing %q after the writability probe: %v\n", path, err)
	}
	return true
}

// migPersistBaseline is the pre-flight every delta mutation runs before it
// touches the engine: it resolves where the mutation is to be recorded and
// makes sure the device has an explicit layout for the delta to edit. The
// result is meaningful only when it is not SUCCESS.
//
// Both halves have to run before the engine mutates. The writability probe
// because the engine assigns the instance id; the seed for a sharper reason
// still — it records the layout as it stands, so run afterwards it would
// record the mutation itself, and the delta would then collide with its own
// baseline as a duplicate or an already-removed id.
func migPersistBaseline(dev *engine.ConfigurableDevice, what string) (string, int, nvml.Return) {
	path, index := migPersistTarget(dev)
	if !migWritable(path) {
		debugLog("[MIG] device %d: cannot record a %s at %q\n", index, what, path)
		return "", 0, nvml.ERROR_NO_PERMISSION
	}
	if err := mockctl.MIGSeedLayout(path, index, dev.MIGLayoutRecords()); err != nil {
		// Refused, not failed: the engine has not run yet, so the board still
		// matches the document and there is no drift to pull back.
		warnLog("[MIG] device %d: %s refused, the layout it would edit could not be recorded: %v\n",
			index, what, err)
		return "", 0, nvml.ERROR_UNKNOWN
	}
	return path, index, nvml.SUCCESS
}

// migPersistFailed reports that a mutation the engine already applied could
// not be recorded, and marks the device so the next refresh pulls it back to
// the document, which is authoritative.
//
// The return is not a permissions statement — the pre-flight has already
// passed — so it is the generic failure rather than the NO_PERMISSION a
// refused mutation answers with.
func migPersistFailed(dev *engine.ConfigurableDevice, what string, err error) nvml.Return {
	warnLog("[MIG] device %d: %s not recorded: %v\n", dev.PhysicalIndex(), what, err)
	dev.MarkMIGDirty()
	return nvml.ERROR_UNKNOWN
}

// migGpuInstanceOwner resolves the physical GPU a GPU instance belongs to,
// together with the instance's own info. Both are read before a mutation: the
// device says where the change is recorded, and the info carries the id the
// record is keyed by, which a destroy cannot ask for afterwards.
func migGpuInstanceOwner(handle unsafe.Pointer) (*engine.ConfigurableDevice, nvml.GpuInstanceInfo, nvml.Return) {
	e := engine.GetEngine()
	info, deviceHandle, ret := e.GpuInstanceGetInfo(handle)
	if ret != nvml.SUCCESS {
		return nil, info, ret
	}
	dev := e.LookupConfigurableDevice(deviceHandle)
	if dev == nil {
		return nil, info, nvml.ERROR_INVALID_ARGUMENT
	}
	return dev, info, nvml.SUCCESS
}

// migComputeInstanceOwner resolves the physical GPU a compute instance belongs
// to, plus the pair of ids the document records it under.
func migComputeInstanceOwner(handle unsafe.Pointer) (*engine.ConfigurableDevice, uint32, uint32, nvml.Return) {
	e := engine.GetEngine()
	info, deviceHandle, giHandle, ret := e.ComputeInstanceGetInfo(handle)
	if ret != nvml.SUCCESS {
		return nil, 0, 0, ret
	}
	dev := e.LookupConfigurableDevice(deviceHandle)
	if dev == nil {
		return nil, 0, 0, nvml.ERROR_INVALID_ARGUMENT
	}
	giInfo, _, ret := e.GpuInstanceGetInfo(giHandle)
	if ret != nvml.SUCCESS {
		return nil, 0, 0, ret
	}
	return dev, giInfo.Id, info.Id, nvml.SUCCESS
}
