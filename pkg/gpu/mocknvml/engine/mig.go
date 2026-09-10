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

package engine

import (
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/gpus"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
)

// migSliceGridWidth is how many slots a board's GPU-instance placement grid
// spans. It is 8 rather than 7 because NVML reports the whole-GPU placement as
// {Start: 0, Size: 8} even on a seven-slice A100.
const migSliceGridWidth = 8

// migModeEnabled is the profile spelling of MIG being on.
const migModeEnabled = "enabled"

// migInstanceKey identifies a MIG device by the pair of instances backing it.
type migInstanceKey struct {
	gi uint32
	ci uint32
}

// migState is the mutable MIG partitioning of one physical GPU.
//
// The GPU instances themselves live in the embedded go-nvml mock device, which
// already implements their lifecycle; this holds only what that mock has no
// concept of: whether the board is MIG-capable, whether MIG is currently on,
// and the MIG devices derived from the instance tree.
type migState struct {
	mu sync.Mutex

	// profiles are the board's GPU- and compute-instance profile tables.
	profiles gpus.MIGProfileConfig
	// supported distinguishes "MIG off" from "not a MIG board". Consumers
	// read ERROR_NOT_SUPPORTED from GetMigMode as "no MIG here at all", so
	// collapsing the two would make a T4 look partitionable.
	supported bool
	// maxGPUInstances caps creation, mirroring the board limit.
	maxGPUInstances int

	mode    int
	pending int

	// devices caches one MIG device per live (GPU instance, compute instance)
	// pair. The cache is not an optimisation: the bridge's handle table keys
	// on the device pointer, so returning a fresh object per enumeration would
	// mint a new C handle every time a consumer walks the device list.
	devices map[migInstanceKey]*ConfigurableDevice
}

// migIdentity marks a ConfigurableDevice as a MIG device rather than a physical
// GPU and carries everything that differs from its parent.
//
// A MIG device is modelled as a ConfigurableDevice with this field set, instead
// of as its own type wrapping go-nvml's bare mock device, for two reasons: the
// bare mock panics on any method whose Func field is unset, and a panic inside
// libnvidia-ml.so is a segfault in the consumer. Reusing the parent's type
// means a query nobody anticipated degrades to the parent GPU's answer.
type migIdentity struct {
	parent *ConfigurableDevice
	gi     nvml.GpuInstance
	ci     nvml.ComputeInstance
	giID   uint32
	ciID   uint32
	uuid   string
	name   string
	attrs  nvml.DeviceAttributes
}

// resolveMaxGPUInstances returns the board's MIG device ceiling from config,
// defaulting to the narrowest GPU-instance placement count when the config
// names no ceiling. Real NVML reports this as a static capability independent
// of whether MIG is currently enabled, so a disabled board still exposes a
// non-zero ceiling.
func resolveMaxGPUInstances(migCfg *MIGConfig, supported bool, profiles gpus.MIGProfileConfig) int {
	ceiling := 0
	if migCfg != nil {
		ceiling = migCfg.MaxGPUInstances
	}
	if ceiling == 0 && supported {
		ceiling = len(profiles.GpuInstancePlacements[nvml.GPU_INSTANCE_PROFILE_1_SLICE])
	}
	return ceiling
}

// initMIG resolves the board's MIG tables and seeds MIG state from the YAML
// profile, materializing any declared partitions. Called once per device at
// construction.
func (d *ConfigurableDevice) initMIG(config *DeviceConfig) {
	profiles, supported := resolveMIGProfiles(d.Config.Name, d.memoryInfo().Total)

	st := &migState{
		profiles:  profiles,
		supported: supported,
		devices:   make(map[migInstanceKey]*ConfigurableDevice),
	}

	// The embedded mock device stamps each GPU instance it creates with these
	// tables, so the instance's own profile queries answer from the board the
	// YAML profile describes rather than from the dgxa100 base every device is
	// built from.
	d.Config.MIGProfiles = profiles

	var migCfg *MIGConfig
	if config != nil {
		migCfg = config.MIG
	}
	if migCfg != nil {
		if migCfg.ModeCurrent == migModeEnabled {
			st.mode = nvml.DEVICE_MIG_ENABLE
		}
		if migCfg.ModePending == migModeEnabled {
			st.pending = nvml.DEVICE_MIG_ENABLE
		}
	}
	st.maxGPUInstances = resolveMaxGPUInstances(migCfg, supported, profiles)

	d.mig = nil
	d.migState = st
	// The declared layout below is what reconcileMIG must consider already
	// applied, or the first refresh would tear it down and rebuild it.
	d.appliedMIG = migCfg

	if migCfg != nil && st.mode == nvml.DEVICE_MIG_ENABLE {
		d.applyDeclaredPartitions(migCfg.GPUInstances)
	}
}

// applyDeclaredPartitions creates the GPU and compute instances a profile
// declares, so a device can boot already partitioned. Failures are logged and
// skipped rather than fatal: a malformed partition should cost the operator
// that partition, not the whole mock library's ability to load.
func (d *ConfigurableDevice) applyDeclaredPartitions(declared []MIGGPUInstanceConfig) {
	for _, giCfg := range declared {
		giProfileID, defaultCIProfileID, err := d.resolveDeclaredGpuInstanceProfile(giCfg)
		if err != nil {
			warnLog("[MIG] device %d: %v\n", d.index, err)
			continue
		}

		for range max(giCfg.Count, 1) {
			giProfileInfo, ret := d.GetGpuInstanceProfileInfo(giProfileID)
			if ret != nvml.SUCCESS {
				warnLog("[MIG] device %d: GPU instance profile %d unavailable: %v\n",
					d.index, giProfileID, ret)
				break
			}
			gi, ret := d.CreateGpuInstance(&giProfileInfo)
			if ret != nvml.SUCCESS {
				warnLog("[MIG] device %d: cannot create declared %q instance: %v\n",
					d.index, giCfg.Profile, ret)
				break
			}
			d.applyDeclaredComputeInstances(gi, giProfileID, defaultCIProfileID, giCfg.ComputeInstances)
		}
	}
}

// applyDeclaredComputeInstances fills a GPU instance with its declared compute
// instances, defaulting to a single instance spanning the whole GPU instance —
// the partitioning nvidia-mig-parted creates when a profile names no compute
// slices, and the only one most consumers ask for.
func (d *ConfigurableDevice) applyDeclaredComputeInstances(
	gi nvml.GpuInstance, giProfileID, defaultCIProfileID int, declared []MIGComputeInstanceConfig,
) {
	if len(declared) == 0 {
		declared = []MIGComputeInstanceConfig{{ProfileID: &defaultCIProfileID}}
	}

	for _, ciCfg := range declared {
		ciProfileID, err := d.resolveDeclaredComputeInstanceProfile(giProfileID, ciCfg)
		if err != nil {
			warnLog("[MIG] device %d: %v\n", d.index, err)
			continue
		}
		for range max(ciCfg.Count, 1) {
			ciProfileInfo, ret := gi.GetComputeInstanceProfileInfo(
				ciProfileID, nvml.COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED)
			if ret != nvml.SUCCESS {
				warnLog("[MIG] device %d: compute instance profile %d unavailable: %v\n",
					d.index, ciProfileID, ret)
				break
			}
			if _, ret := gi.CreateComputeInstance(&ciProfileInfo); ret != nvml.SUCCESS {
				warnLog("[MIG] device %d: cannot create declared compute instance: %v\n", d.index, ret)
				break
			}
		}
	}
}

// resolveDeclaredGpuInstanceProfile turns a declared partition's profile name
// or raw ID into a GPU instance profile ID, plus the compute instance profile
// that spans it (which the name may have named explicitly, as in "1c.3g.20gb").
func (d *ConfigurableDevice) resolveDeclaredGpuInstanceProfile(cfg MIGGPUInstanceConfig) (int, int, error) {
	switch {
	case cfg.ProfileID != nil && cfg.Profile != "":
		return 0, 0, fmt.Errorf("MIG partition sets both profile %q and profile_id %d; use one",
			cfg.Profile, *cfg.ProfileID)
	case cfg.ProfileID != nil:
		giProfileID := *cfg.ProfileID
		ciProfileID, err := spanningComputeInstanceProfile(giProfileID)
		if err != nil {
			return 0, 0, fmt.Errorf("MIG partition profile_id %d: %w", giProfileID, err)
		}
		return giProfileID, ciProfileID, nil
	case cfg.Profile != "":
		giProfileID, ciProfileID, err := d.resolveMigProfileByName(cfg.Profile)
		if err != nil {
			return 0, 0, fmt.Errorf("MIG partition %q: %w", cfg.Profile, err)
		}
		return giProfileID, ciProfileID, nil
	}
	return 0, 0, errors.New("MIG partition names neither profile nor profile_id")
}

// ciSliceSpec matches the bare compute-slice spelling, e.g. "2c".
var ciSliceSpec = regexp.MustCompile(`^([0-9]+)c$`)

// resolveDeclaredComputeInstanceProfile accepts a bare slice count ("1c"), a
// full MIG profile name ("1c.3g.20gb") or a raw profile ID.
func (d *ConfigurableDevice) resolveDeclaredComputeInstanceProfile(
	giProfileID int, cfg MIGComputeInstanceConfig,
) (int, error) {
	switch {
	case cfg.ProfileID != nil && cfg.Profile != "":
		return 0, fmt.Errorf("MIG compute instance sets both profile %q and profile_id %d; use one",
			cfg.Profile, *cfg.ProfileID)
	case cfg.ProfileID != nil:
		return *cfg.ProfileID, nil
	case cfg.Profile == "":
		return 0, errors.New("MIG compute instance names neither profile nor profile_id")
	}

	if m := ciSliceSpec.FindStringSubmatch(cfg.Profile); m != nil {
		slices, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("MIG compute instance %q: %w", cfg.Profile, err)
		}
		ciProfileID, ok := computeInstanceProfileForSliceCount(slices)
		if !ok {
			return 0, fmt.Errorf("MIG compute instance %q: no profile spans %d slices", cfg.Profile, slices)
		}
		return ciProfileID, nil
	}

	nameGI, nameCI, err := d.resolveMigProfileByName(cfg.Profile)
	if err != nil {
		return 0, fmt.Errorf("MIG compute instance %q: %w", cfg.Profile, err)
	}
	if nameGI != giProfileID {
		return 0, fmt.Errorf("MIG compute instance %q belongs to a different GPU instance profile", cfg.Profile)
	}
	return nameCI, nil
}

// resolveMigProfileByName finds the (GPU instance, compute instance) profile
// pair whose canonical name is the one given, using this device's own tables.
// It is how a profile's mig.gpu_instances[].profile becomes NVML profile IDs,
// and it fails rather than guessing when the board offers no such partition.
//
// A name can be spelled by more than one profile pair — the base and revision
// compute-instance profiles of a GPU instance share a slice count and so a
// name — so candidates are searched in ascending profile-ID order and the
// lowest match wins. Iterating the tables directly would resolve the same
// config differently from run to run.
func (d *ConfigurableDevice) resolveMigProfileByName(name string) (int, int, error) {
	st := d.migState
	if st == nil || !st.supported {
		return 0, 0, errors.New("device does not support MIG")
	}

	deviceMemory := d.memoryInfo().Total
	for giProfileID := range nvml.GPU_INSTANCE_PROFILE_COUNT {
		giProfile, ok := st.profiles.GpuInstanceProfiles[giProfileID]
		if !ok {
			continue
		}
		ciProfiles, ok := st.profiles.ComputeInstanceProfiles[giProfileID]
		if !ok {
			continue
		}
		for ciProfileID := range nvml.COMPUTE_INSTANCE_PROFILE_COUNT {
			if _, ok := ciProfiles[ciProfileID]; !ok {
				continue
			}
			candidate, err := migProfileName(giProfileID, ciProfileID, giProfile.MemorySizeMB, deviceMemory)
			if err != nil {
				continue
			}
			if candidate == name {
				return giProfileID, ciProfileID, nil
			}
		}
	}
	return 0, 0, errors.New("no such MIG profile on this device")
}

// migProfileDisplayName is how NVML spells a profile in the name field of the
// versioned profile-info structs and in nvidia-smi output: the bare profile
// with a "MIG " prefix. The bare form is kept separate because that is what a
// YAML profile declares and what the device plugin publishes.
func migProfileDisplayName(profile string) string {
	return "MIG " + profile
}

// spanningComputeInstanceProfile returns the compute instance profile that
// covers a whole GPU instance of the given profile.
func spanningComputeInstanceProfile(giProfileID int) (int, error) {
	slices, ok := gpuInstanceSliceCount(giProfileID)
	if !ok {
		return 0, errors.New("unknown GPU instance profile")
	}
	ciProfileID, ok := computeInstanceProfileForSliceCount(slices)
	if !ok {
		return 0, fmt.Errorf("no compute instance profile spans %d slices", slices)
	}
	return ciProfileID, nil
}

func computeInstanceProfileForSliceCount(slices int) (int, bool) {
	switch slices {
	case 1:
		return nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, true
	case 2:
		return nvml.COMPUTE_INSTANCE_PROFILE_2_SLICE, true
	case 3:
		return nvml.COMPUTE_INSTANCE_PROFILE_3_SLICE, true
	case 4:
		return nvml.COMPUTE_INSTANCE_PROFILE_4_SLICE, true
	case 6:
		return nvml.COMPUTE_INSTANCE_PROFILE_6_SLICE, true
	case 7:
		return nvml.COMPUTE_INSTANCE_PROFILE_7_SLICE, true
	case 8:
		return nvml.COMPUTE_INSTANCE_PROFILE_8_SLICE, true
	}
	return 0, false
}

// =============================================================================
// MIG mode
// =============================================================================

// GetMigMode returns the current and pending MIG modes.
//
// ERROR_NOT_SUPPORTED on a board with no MIG tables is load-bearing: it is how
// go-nvlib's IsMigCapable — and so the device plugin — tells a MIG-capable GPU
// from a T4.
func (d *ConfigurableDevice) GetMigMode() (int, int, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, 0, ret
	}
	// A MIG device is not itself partitionable.
	if d.mig != nil {
		return 0, 0, nvml.ERROR_NOT_SUPPORTED
	}
	st := d.migState
	if st == nil || !st.supported {
		return 0, 0, nvml.ERROR_NOT_SUPPORTED
	}

	st.mu.Lock()
	current, pending := st.mode, st.pending
	st.mu.Unlock()

	debugLog("[NVML] nvmlDeviceGetMigMode -> current=%d pending=%d\n", current, pending)
	return current, pending, nvml.SUCCESS
}

// SetMigMode changes MIG mode. The second return is the activation status,
// which real NVML uses to report that the mode change needs a GPU reset; the
// mock applies it immediately and reports success.
//
// Disabling MIG tears down the partitioning, as it does on hardware.
func (d *ConfigurableDevice) SetMigMode(mode int) (nvml.Return, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return ret, ret
	}
	if d.mig != nil {
		return nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_NOT_SUPPORTED
	}
	st := d.migState
	if st == nil || !st.supported {
		return nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_NOT_SUPPORTED
	}
	if mode != nvml.DEVICE_MIG_ENABLE && mode != nvml.DEVICE_MIG_DISABLE {
		return nvml.ERROR_INVALID_ARGUMENT, nvml.ERROR_INVALID_ARGUMENT
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if mode != st.mode {
		st.destroyAllLocked(d)
	}
	st.mode = mode
	st.pending = mode

	debugLog("[NVML] nvmlDeviceSetMigMode(%d) -> SUCCESS\n", mode)
	return nvml.SUCCESS, nvml.SUCCESS
}

// GetMaxMigDeviceCount returns the board's MIG device ceiling. Real NVML
// reports this as a static capability regardless of whether MIG is on, and
// consumers use it as the upper bound when walking MIG device indices. Here it
// is not static: an override can move it, so the read is guarded.
func (d *ConfigurableDevice) GetMaxMigDeviceCount() (int, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
	if d.mig != nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	count := 0
	if st := d.migState; st != nil {
		st.mu.Lock()
		count = st.maxGPUInstances
		st.mu.Unlock()
	}
	debugLog("[NVML] nvmlDeviceGetMaxMigDeviceCount -> %d\n", count)
	return count, nvml.SUCCESS
}

// migEnabled reports whether the device can currently be partitioned. Callers
// must not hold st.mu.
func (d *ConfigurableDevice) migEnabled() (*migState, nvml.Return) {
	if d.mig != nil {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	st := d.migState
	if st == nil || !st.supported {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	st.mu.Lock()
	mode := st.mode
	st.mu.Unlock()
	if mode != nvml.DEVICE_MIG_ENABLE {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	return st, nvml.SUCCESS
}

// =============================================================================
// MIG devices
// =============================================================================

// GetMigDeviceHandleByIndex returns the MIG device at the given index.
//
// ERROR_NOT_FOUND (rather than NOT_SUPPORTED) is what consumers treat as
// end-of-iteration: go-nvlib walks indices up to GetMaxMigDeviceCount and skips
// the ones that report NOT_FOUND, so a partly-populated board enumerates
// cleanly.
func (d *ConfigurableDevice) GetMigDeviceHandleByIndex(index int) (nvml.Device, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	if d.mig != nil {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	st := d.migState
	if st == nil || !st.supported {
		return nil, nvml.ERROR_NOT_FOUND
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.mode != nvml.DEVICE_MIG_ENABLE {
		return nil, nvml.ERROR_NOT_FOUND
	}
	devices := st.migDevicesLocked(d)
	if index < 0 || index >= len(devices) {
		debugLog("[NVML] nvmlDeviceGetMigDeviceHandleByIndex(%d) -> NOT_FOUND\n", index)
		return nil, nvml.ERROR_NOT_FOUND
	}
	return devices[index], nvml.SUCCESS
}

// migDevicesLocked returns this GPU's MIG devices ordered by GPU instance then
// compute instance ID, so an index identifies the same partition across calls.
// Requires st.mu.
func (st *migState) migDevicesLocked(parent *ConfigurableDevice) []*ConfigurableDevice {
	var devices []*ConfigurableDevice

	for _, gi := range st.liveGpuInstances(parent) {
		for _, ci := range liveComputeInstances(gi) {
			key := migInstanceKey{gi: gi.Info.Id, ci: ci.Info.Id}
			dev, ok := st.devices[key]
			if !ok {
				dev = st.newMigDeviceLocked(parent, gi, ci)
				st.devices[key] = dev
			}
			devices = append(devices, dev)
		}
	}
	return devices
}

// liveGpuInstances snapshots the GPU instances of a device, ID-ordered.
func (st *migState) liveGpuInstances(parent *ConfigurableDevice) []*mockserver.GpuInstance {
	parent.Device.RLock()
	gis := make([]*mockserver.GpuInstance, 0, len(parent.Device.GpuInstances))
	for gi := range parent.Device.GpuInstances {
		gis = append(gis, gi)
	}
	parent.Device.RUnlock()

	sort.Slice(gis, func(i, j int) bool { return gis[i].Info.Id < gis[j].Info.Id })
	return gis
}

// liveComputeInstances snapshots the compute instances of a GPU instance, ID-ordered.
func liveComputeInstances(gi *mockserver.GpuInstance) []*mockserver.ComputeInstance {
	gi.RLock()
	cis := make([]*mockserver.ComputeInstance, 0, len(gi.ComputeInstances))
	for ci := range gi.ComputeInstances {
		cis = append(cis, ci)
	}
	gi.RUnlock()

	sort.Slice(cis, func(i, j int) bool { return cis[i].Info.Id < cis[j].Info.Id })
	return cis
}

// devicesDerivedFrom returns the MIG devices this state has already minted for
// a GPU instance, narrowed to a single compute instance when ciID is non-nil.
//
// It reports what was cached, not what exists: only a device a caller has
// actually enumerated can have a handle to invalidate.
func (st *migState) devicesDerivedFrom(giID uint32, ciID *uint32) []*ConfigurableDevice {
	st.mu.Lock()
	defer st.mu.Unlock()

	var devices []*ConfigurableDevice
	for key, dev := range st.devices {
		if key.gi != giID {
			continue
		}
		if ciID != nil && key.ci != *ciID {
			continue
		}
		devices = append(devices, dev)
	}
	return devices
}

// migDeviceByUUID finds the MIG device carrying a UUID, across every GPU on
// the node, or nil if no partition has it.
//
// The walk enumerates rather than consulting a cache, so a partition that has
// never been handed out by index is still findable: a consumer that learned a
// UUID from a pod's environment has no reason to have walked the indices first.
func (e *Engine) migDeviceByUUID(uuid string) *ConfigurableDevice {
	for _, parent := range e.server.configurableDevices {
		if parent == nil {
			continue
		}
		if dev := parent.migDeviceByUUID(uuid); dev != nil {
			return dev
		}
	}
	return nil
}

// migDeviceByUUID finds one of this GPU's partitions by UUID.
func (d *ConfigurableDevice) migDeviceByUUID(uuid string) *ConfigurableDevice {
	// This is the one MIG read that no handle guard precedes, so it has to
	// refresh for itself: the set of partitions a UUID can name is exactly
	// what an override changes.
	d.refresh()

	st := d.migState
	if st == nil || !st.supported {
		return nil
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.mode != nvml.DEVICE_MIG_ENABLE {
		return nil
	}
	for _, dev := range st.migDevicesLocked(d) {
		if dev.mig != nil && dev.mig.uuid == uuid {
			return dev
		}
	}
	return nil
}

// newMigDeviceLocked builds the MIG device backing one (GPU instance, compute
// instance) pair. Requires st.mu.
func (st *migState) newMigDeviceLocked(
	parent *ConfigurableDevice, gi *mockserver.GpuInstance, ci *mockserver.ComputeInstance,
) *ConfigurableDevice {
	giProfile := st.profiles.GpuInstanceProfiles[int(gi.Info.ProfileId)]
	ciProfile := st.profiles.ComputeInstanceProfiles[int(gi.Info.ProfileId)][int(ci.Info.ProfileId)]

	name := parent.Config.Name
	if profileName, err := migProfileName(
		int(gi.Info.ProfileId), int(ci.Info.ProfileId), giProfile.MemorySizeMB, parent.effectiveMemoryInfo().Total,
	); err == nil {
		// Real NVML spells a MIG device's name as the board followed by its
		// partition, e.g. "NVIDIA A100-SXM4-40GB MIG 1g.5gb".
		name = parent.Config.Name + " MIG " + profileName
	}

	id := &migIdentity{
		parent: parent,
		gi:     gi,
		ci:     ci,
		giID:   gi.Info.Id,
		ciID:   ci.Info.Id,
		uuid:   migDeviceUUID(parent.UUID, gi.Info.Id, ci.Info.Id),
		name:   name,
		attrs: nvml.DeviceAttributes{
			MultiprocessorCount:       ciProfile.MultiprocessorCount,
			SharedCopyEngineCount:     ciProfile.SharedCopyEngineCount,
			SharedDecoderCount:        ciProfile.SharedDecoderCount,
			SharedEncoderCount:        ciProfile.SharedEncoderCount,
			SharedJpegCount:           ciProfile.SharedJpegCount,
			SharedOfaCount:            ciProfile.SharedOfaCount,
			GpuInstanceSliceCount:     giProfile.SliceCount,
			ComputeInstanceSliceCount: ciProfile.SliceCount,
			MemorySizeMB:              giProfile.MemorySizeMB,
		},
	}

	// Share the parent's immutable state and give the MIG device its own
	// locks. A MIG device deliberately gets no failure injector and no dynamic
	// metrics: those model the physical GPU, and the parent already reports them.
	dev := &ConfigurableDevice{
		Device:      parent.Device,
		fabric:      parent.fabric,
		index:       parent.index,
		minorNumber: parent.minorNumber,
		baseConfig:  parent.baseConfig,
		bar1Memory:  parent.bar1Memory,
		pciInfo:     parent.pciInfo,
		boardID:     parent.boardID,
		mig:         id,
	}
	dev.effective.Store(parent.effective.Load())

	debugLog("[MIG] device %d: MIG device gi=%d ci=%d uuid=%s\n", parent.index, id.giID, id.ciID, id.uuid)
	return dev
}

// migDeviceUUID derives a MIG device's UUID from its parent and instance IDs.
//
// It must be deterministic: two processes load the mock independently and have
// to agree on device identity, the same constraint that already makes full-GPU
// UUIDs deterministic. Consumers parse the text after "MIG-" as a UUID, so the
// result keeps the canonical 8-4-4-4-12 shape.
//
// The derivation splices the instance IDs into the parent's UUID rather than
// hashing it, so a MIG device's UUID is legible as "partition (gi, ci) of that
// GPU" when it turns up in a device-plugin allocation or a pod's environment.
// Two bytes are enough: no board offers more than eight of either instance.
func migDeviceUUID(parentUUID string, giID, ciID uint32) string {
	base := trimUUIDPrefix(parentUUID)

	groups := strings.Split(base, "-")
	if len(groups) != 5 || len(groups[3]) != 4 {
		// An operator-supplied UUID need not be UUID-shaped at all. Fall back
		// to a shape we control, keyed on the parent text so distinct parents
		// stay distinct.
		h := fnv.New64a()
		_, _ = h.Write([]byte(base))
		sum := h.Sum64()
		return fmt.Sprintf("MIG-%08x-%04x-%04x-%02x%02x-%012x",
			uint32(sum>>32), uint16(sum>>16), uint16(sum), byte(giID), byte(ciID), sum&0xffffffffffff)
	}

	groups[3] = fmt.Sprintf("%02x%02x", byte(giID), byte(ciID))
	return "MIG-" + strings.Join(groups, "-")
}

// trimUUIDPrefix strips NVML's device-class prefix, so a parent UUID reads the
// same whether or not it carries one.
func trimUUIDPrefix(deviceUUID string) string {
	for _, prefix := range []string{"GPU-", "MIG-"} {
		if trimmed, ok := strings.CutPrefix(deviceUUID, prefix); ok {
			return trimmed
		}
	}
	return deviceUUID
}

// IsMigDeviceHandle reports whether this handle refers to a MIG device.
func (d *ConfigurableDevice) IsMigDeviceHandle() (bool, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return false, ret
	}
	return d.mig != nil, nvml.SUCCESS
}

// GetDeviceHandleFromMigDeviceHandle returns the physical GPU behind a MIG
// device. ERROR_INVALID_ARGUMENT on a full GPU matches real NVML: the handle is
// simply the wrong kind for this query.
func (d *ConfigurableDevice) GetDeviceHandleFromMigDeviceHandle() (nvml.Device, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	if d.mig == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	return d.mig.parent, nvml.SUCCESS
}

// GetGpuInstanceId returns the ID of the GPU instance backing a MIG device.
func (d *ConfigurableDevice) GetGpuInstanceId() (int, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
	if d.mig == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return int(d.mig.giID), nvml.SUCCESS
}

// GetComputeInstanceId returns the ID of the compute instance backing a MIG device.
func (d *ConfigurableDevice) GetComputeInstanceId() (int, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
	if d.mig == nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return int(d.mig.ciID), nvml.SUCCESS
}

// GetAttributes returns a MIG device's engine and memory allocation. This is a
// MIG-only query, and the one the device plugin compares across devices before
// it will accept a node with migStrategy=single.
func (d *ConfigurableDevice) GetAttributes() (nvml.DeviceAttributes, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.DeviceAttributes{}, ret
	}
	if d.mig == nil {
		return nvml.DeviceAttributes{}, nvml.ERROR_NOT_SUPPORTED
	}
	return d.mig.attrs, nvml.SUCCESS
}

// =============================================================================
// GPU instance lifecycle
// =============================================================================

// GetGpuInstanceProfileInfo returns a GPU instance profile the board supports.
func (d *ConfigurableDevice) GetGpuInstanceProfileInfo(profileID int) (nvml.GpuInstanceProfileInfo, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.GpuInstanceProfileInfo{}, ret
	}
	st, ret := d.migEnabled()
	if ret != nvml.SUCCESS {
		return nvml.GpuInstanceProfileInfo{}, ret
	}
	if profileID < 0 || profileID >= nvml.GPU_INSTANCE_PROFILE_COUNT {
		return nvml.GpuInstanceProfileInfo{}, nvml.ERROR_INVALID_ARGUMENT
	}
	info, ok := st.profiles.GpuInstanceProfiles[profileID]
	if !ok {
		return nvml.GpuInstanceProfileInfo{}, nvml.ERROR_NOT_SUPPORTED
	}
	return info, nvml.SUCCESS
}

// GetGpuInstancePossiblePlacements returns the slice offsets a profile can occupy.
func (d *ConfigurableDevice) GetGpuInstancePossiblePlacements(
	info *nvml.GpuInstanceProfileInfo,
) ([]nvml.GpuInstancePlacement, nvml.Return) {
	if info == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	st, ret := d.migEnabled()
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	placements, ok := st.profiles.GpuInstancePlacements[int(info.Id)]
	if !ok {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	return placements, nvml.SUCCESS
}

// CreateGpuInstance partitions the GPU, choosing the first placement that still
// fits. Real NVML picks a placement for the caller in the same way.
func (d *ConfigurableDevice) CreateGpuInstance(
	info *nvml.GpuInstanceProfileInfo,
) (nvml.GpuInstance, nvml.Return) {
	if info == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	st, ret := d.migEnabled()
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	placement, ok := st.freePlacementLocked(d, int(info.Id))
	if !ok {
		return nil, nvml.ERROR_INSUFFICIENT_RESOURCES
	}
	return st.createGpuInstanceLocked(d, info, &placement)
}

// CreateGpuInstanceWithPlacement partitions the GPU at a caller-chosen offset.
func (d *ConfigurableDevice) CreateGpuInstanceWithPlacement(
	info *nvml.GpuInstanceProfileInfo, placement *nvml.GpuInstancePlacement,
) (nvml.GpuInstance, nvml.Return) {
	if info == nil || placement == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	st, ret := d.migEnabled()
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if !st.placementOfferedLocked(int(info.Id), *placement) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	if st.occupiedSlicesLocked(d)&placementMask(*placement) != 0 {
		return nil, nvml.ERROR_INSUFFICIENT_RESOURCES
	}
	return st.createGpuInstanceLocked(d, info, placement)
}

// createGpuInstanceLocked creates an instance with a counter-assigned ID.
// Requires st.mu.
func (st *migState) createGpuInstanceLocked(
	parent *ConfigurableDevice, info *nvml.GpuInstanceProfileInfo, placement *nvml.GpuInstancePlacement,
) (nvml.GpuInstance, nvml.Return) {
	return st.createGpuInstancePinnedLocked(parent, info, placement, nil)
}

// createGpuInstancePinnedLocked delegates creation to the embedded go-nvml
// mock, which already implements the instance tree, and then fills in the
// instance methods that mock leaves unset. A non-nil id overrides the ID the
// mock assigned, which is how an explicit layout comes back with the
// identities it was written down under.
//
// Stamping after the fact is safe because the mock keys its instance set on
// the pointer, never on the ID it handed out. The counter is then pushed past
// the pinned value so a later auto-assigned instance cannot collide with it.
// Requires st.mu.
func (st *migState) createGpuInstancePinnedLocked(
	parent *ConfigurableDevice, info *nvml.GpuInstanceProfileInfo,
	placement *nvml.GpuInstancePlacement, id *uint32,
) (nvml.GpuInstance, nvml.Return) {
	if st.maxGPUInstances > 0 && len(st.liveGpuInstances(parent)) >= st.maxGPUInstances {
		return nil, nvml.ERROR_INSUFFICIENT_RESOURCES
	}

	created, ret := parent.Device.CreateGpuInstanceWithPlacement(info, placement)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	gi, ok := created.(*mockserver.GpuInstance)
	if !ok {
		return nil, nvml.ERROR_UNKNOWN
	}
	if id != nil {
		parent.Device.Lock()
		gi.Info.Id = *id
		if parent.Device.GpuInstanceCounter <= *id {
			parent.Device.GpuInstanceCounter = *id + 1
		}
		parent.Device.Unlock()
	}
	st.extendGpuInstance(parent, gi)

	debugLog("[MIG] device %d: created GPU instance id=%d profile=%d placement=%d+%d\n",
		parent.index, gi.Info.Id, info.Id, placement.Start, placement.Size)
	return gi, nvml.SUCCESS
}

// extendGpuInstance fills in the GPU instance methods go-nvml's mock leaves
// unset, and wraps Destroy so tearing an instance down also retires the MIG
// devices derived from it.
func (st *migState) extendGpuInstance(parent *ConfigurableDevice, gi *mockserver.GpuInstance) {
	// go-nvml's mock stamps the instance with the bare inner device it was
	// created on. Repoint it at the ConfigurableDevice: this is the object a
	// caller gets back from nvmlGpuInstanceGetInfo and can turn into a device
	// handle, and the bare mock panics on any method whose Func field is
	// unset — a panic inside libnvidia-ml.so is a segfault in the consumer.
	gi.Info.Device = parent

	gi.GetComputeInstanceByIdFunc = func(id int) (nvml.ComputeInstance, nvml.Return) {
		for _, ci := range liveComputeInstances(gi) {
			if int(ci.Info.Id) == id {
				return ci, nvml.SUCCESS
			}
		}
		return nil, nvml.ERROR_NOT_FOUND
	}

	gi.CreateComputeInstanceFunc = func(info *nvml.ComputeInstanceProfileInfo) (nvml.ComputeInstance, nvml.Return) {
		return st.createComputeInstance(parent, gi, info, nil)
	}
	gi.CreateComputeInstanceWithPlacementFunc = func(
		info *nvml.ComputeInstanceProfileInfo, placement *nvml.ComputeInstancePlacement,
	) (nvml.ComputeInstance, nvml.Return) {
		if placement == nil {
			return nil, nvml.ERROR_INVALID_ARGUMENT
		}
		return st.createComputeInstance(parent, gi, info, placement)
	}

	gi.GetComputeInstanceRemainingCapacityFunc = func(info *nvml.ComputeInstanceProfileInfo) (int, nvml.Return) {
		if info == nil {
			return 0, nvml.ERROR_INVALID_ARGUMENT
		}
		remaining := int(info.InstanceCount) - countComputeInstancesWithProfile(gi, info.Id)
		return max(remaining, 0), nvml.SUCCESS
	}

	gi.DestroyFunc = func() nvml.Return {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.destroyGpuInstanceLocked(parent, gi)
		return nvml.SUCCESS
	}
}

// createComputeInstance partitions a GPU instance further. A nil placement
// lets the mock pick the first free offset, as nvmlGpuInstanceCreateComputeInstance
// does; a non-nil one must be a placement the profile actually offers and must
// not overlap a live compute instance.
//
// Capacity is per compute-instance profile: the profile's InstanceCount is how
// many of that shape fit in the GPU instance, so a full instance rejects
// another "1c" while still admitting a differently shaped one.
func (st *migState) createComputeInstance(
	parent *ConfigurableDevice, gi *mockserver.GpuInstance,
	info *nvml.ComputeInstanceProfileInfo, placement *nvml.ComputeInstancePlacement,
) (nvml.ComputeInstance, nvml.Return) {
	if info == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if info.InstanceCount > 0 &&
		countComputeInstancesWithProfile(gi, info.Id) >= int(info.InstanceCount) {
		return nil, nvml.ERROR_INSUFFICIENT_RESOURCES
	}

	offered, ret := computeInstancePlacements(gi, info)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	chosen, ret := chooseComputeInstancePlacement(gi, offered, placement)
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	gi.Lock()
	ciInfo := nvml.ComputeInstanceInfo{
		Device:      parent,
		GpuInstance: gi,
		Id:          gi.ComputeInstanceCounter,
		ProfileId:   info.Id,
		Placement:   chosen,
	}
	gi.ComputeInstanceCounter++
	ci := mockserver.NewComputeInstanceFromInfo(ciInfo)
	gi.ComputeInstances[ci] = struct{}{}
	gi.Unlock()

	st.extendComputeInstance(gi, ci)
	debugLog("[MIG] device %d: created compute instance gi=%d ci=%d profile=%d placement=%d+%d\n",
		parent.index, gi.Info.Id, ci.Info.Id, info.Id, chosen.Start, chosen.Size)
	return ci, nvml.SUCCESS
}

// computeInstancePlacements returns the compute-slice offsets a profile may
// occupy within a GPU instance.
//
// go-nvml's profile tables are inconsistent here: the H100, B200 and A30
// tables carry real offsets, while the A100's list every valid compute
// profile with an empty offset list. An empty list cannot be right — a
// consumer reads it as "this profile fits nowhere" — so it is filled in from
// the profile's own shape: InstanceCount instances of SliceCount slices laid
// end to end, which is the layout the populated tables all follow.
func computeInstancePlacements(
	gi *mockserver.GpuInstance, info *nvml.ComputeInstanceProfileInfo,
) ([]nvml.ComputeInstancePlacement, nvml.Return) {
	offered, ret := gi.GetComputeInstancePossiblePlacements(info)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	if len(offered) > 0 {
		return offered, nvml.SUCCESS
	}
	if info.SliceCount == 0 {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}

	synthesized := make([]nvml.ComputeInstancePlacement, 0, info.InstanceCount)
	for i := range info.InstanceCount {
		synthesized = append(synthesized, nvml.ComputeInstancePlacement{
			Start: i * info.SliceCount,
			Size:  info.SliceCount,
		})
	}
	return synthesized, nvml.SUCCESS
}

// chooseComputeInstancePlacement validates a requested compute-slice placement
// or, when none was requested, picks the first offered one that is still free.
func chooseComputeInstancePlacement(
	gi *mockserver.GpuInstance, offered []nvml.ComputeInstancePlacement,
	requested *nvml.ComputeInstancePlacement,
) (nvml.ComputeInstancePlacement, nvml.Return) {
	occupied := occupiedComputeSlices(gi)

	if requested != nil {
		if !slices.Contains(offered, *requested) {
			return nvml.ComputeInstancePlacement{}, nvml.ERROR_INVALID_ARGUMENT
		}
		if occupied&computeSliceMask(*requested) != 0 {
			return nvml.ComputeInstancePlacement{}, nvml.ERROR_INSUFFICIENT_RESOURCES
		}
		return *requested, nvml.SUCCESS
	}

	for _, candidate := range offered {
		if occupied&computeSliceMask(candidate) == 0 {
			return candidate, nvml.SUCCESS
		}
	}
	return nvml.ComputeInstancePlacement{}, nvml.ERROR_INSUFFICIENT_RESOURCES
}

// occupiedComputeSlices returns a bitmask of the compute slices the live
// compute instances of a GPU instance hold.
func occupiedComputeSlices(gi *mockserver.GpuInstance) uint64 {
	var occupied uint64
	for _, ci := range liveComputeInstances(gi) {
		occupied |= computeSliceMask(ci.Info.Placement)
	}
	return occupied
}

func computeSliceMask(placement nvml.ComputeInstancePlacement) uint64 {
	if placement.Size == 0 || placement.Start >= migSliceGridWidth {
		return 0
	}
	size := min(placement.Size, migSliceGridWidth-placement.Start)
	return ((uint64(1) << size) - 1) << placement.Start
}

func countComputeInstancesWithProfile(gi *mockserver.GpuInstance, profileID uint32) int {
	count := 0
	for _, ci := range liveComputeInstances(gi) {
		if ci.Info.ProfileId == profileID {
			count++
		}
	}
	return count
}

// extendComputeInstance wraps a compute instance's Destroy so that retiring it
// also retires the MIG device it backed.
func (st *migState) extendComputeInstance(gi *mockserver.GpuInstance, ci *mockserver.ComputeInstance) {
	ci.DestroyFunc = func() nvml.Return {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.destroyComputeInstanceLocked(gi, ci)
		return nvml.SUCCESS
	}
}

// destroyGpuInstanceLocked removes a GPU instance and every MIG device derived
// from it. Requires st.mu.
func (st *migState) destroyGpuInstanceLocked(parent *ConfigurableDevice, gi *mockserver.GpuInstance) {
	parent.Device.Lock()
	delete(parent.Device.GpuInstances, gi)
	parent.Device.Unlock()

	for key := range st.devices {
		if key.gi == gi.Info.Id {
			delete(st.devices, key)
		}
	}
	debugLog("[MIG] device %d: destroyed GPU instance id=%d\n", parent.index, gi.Info.Id)
}

// destroyComputeInstanceLocked removes a compute instance and the MIG device it
// backed. Requires st.mu.
func (st *migState) destroyComputeInstanceLocked(gi *mockserver.GpuInstance, ci *mockserver.ComputeInstance) {
	gi.Lock()
	delete(gi.ComputeInstances, ci)
	gi.Unlock()

	delete(st.devices, migInstanceKey{gi: gi.Info.Id, ci: ci.Info.Id})
}

// destroyAllLocked tears down the whole partitioning, which is what disabling
// MIG does on hardware. Requires st.mu.
func (st *migState) destroyAllLocked(parent *ConfigurableDevice) {
	parent.Device.Lock()
	parent.Device.GpuInstances = make(map[*mockserver.GpuInstance]struct{})
	parent.Device.Unlock()

	st.devices = make(map[migInstanceKey]*ConfigurableDevice)
}

// GetGpuInstances returns the live GPU instances matching a profile.
func (d *ConfigurableDevice) GetGpuInstances(
	info *nvml.GpuInstanceProfileInfo,
) ([]nvml.GpuInstance, nvml.Return) {
	if info == nil {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	st, ret := d.migEnabled()
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	var matching []nvml.GpuInstance
	for _, gi := range st.liveGpuInstances(d) {
		if gi.Info.ProfileId == info.Id {
			matching = append(matching, gi)
		}
	}
	return matching, nvml.SUCCESS
}

// GetGpuInstanceById looks a GPU instance up by ID. go-nvml's mock leaves this
// unset, but it is on go-nvlib's path from a MIG device back to its profile.
func (d *ConfigurableDevice) GetGpuInstanceById(id int) (nvml.GpuInstance, nvml.Return) {
	st, ret := d.migEnabled()
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	for _, gi := range st.liveGpuInstances(d) {
		if int(gi.Info.Id) == id {
			return gi, nvml.SUCCESS
		}
	}
	return nil, nvml.ERROR_NOT_FOUND
}

// GetGpuInstanceRemainingCapacity reports how many more instances of a profile
// still fit. nvidia-mig-parted asks this before partitioning.
func (d *ConfigurableDevice) GetGpuInstanceRemainingCapacity(
	info *nvml.GpuInstanceProfileInfo,
) (int, nvml.Return) {
	if info == nil {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	st, ret := d.migEnabled()
	if ret != nvml.SUCCESS {
		return 0, ret
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	occupied := st.occupiedSlicesLocked(d)
	remaining := 0
	for _, placement := range st.profiles.GpuInstancePlacements[int(info.Id)] {
		mask := placementMask(placement)
		if occupied&mask == 0 {
			// Count this placement as taken so overlapping candidates are not
			// double-counted.
			occupied |= mask
			remaining++
		}
	}
	if st.maxGPUInstances > 0 {
		remaining = min(remaining, st.maxGPUInstances-len(st.liveGpuInstances(d)))
	}
	return max(remaining, 0), nvml.SUCCESS
}

// =============================================================================
// Slice occupancy
// =============================================================================

// occupiedSlicesLocked returns a bitmask of the placement grid slots the live
// GPU instances hold. Requires st.mu.
func (st *migState) occupiedSlicesLocked(parent *ConfigurableDevice) uint64 {
	var occupied uint64
	for _, gi := range st.liveGpuInstances(parent) {
		occupied |= placementMask(gi.Info.Placement)
	}
	return occupied
}

// placementMask renders a placement as the grid slots it covers. Size, not the
// profile's slice count, is the span: NVML pads placements to power-of-two
// boundaries, so a 3-slice instance occupies four slots.
func placementMask(placement nvml.GpuInstancePlacement) uint64 {
	if placement.Size == 0 || placement.Start >= migSliceGridWidth {
		return 0
	}
	size := min(placement.Size, migSliceGridWidth-placement.Start)
	return ((uint64(1) << size) - 1) << placement.Start
}

// freePlacementLocked returns the first offered placement for a profile that
// does not overlap a live instance. Requires st.mu.
func (st *migState) freePlacementLocked(
	parent *ConfigurableDevice, giProfileID int,
) (nvml.GpuInstancePlacement, bool) {
	occupied := st.occupiedSlicesLocked(parent)
	for _, placement := range st.profiles.GpuInstancePlacements[giProfileID] {
		if occupied&placementMask(placement) == 0 {
			return placement, true
		}
	}
	return nvml.GpuInstancePlacement{}, false
}

// placementOfferedLocked reports whether a profile can sit at a given offset at
// all, so a caller-chosen placement is rejected as invalid rather than
// silently accepted. Requires st.mu.
func (st *migState) placementOfferedLocked(giProfileID int, placement nvml.GpuInstancePlacement) bool {
	for _, offered := range st.profiles.GpuInstancePlacements[giProfileID] {
		if offered.Start == placement.Start && offered.Size == placement.Size {
			return true
		}
	}
	return false
}
