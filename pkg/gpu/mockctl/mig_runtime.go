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
//
// Because they are delta writers over that layout, they fail rather than guess
// when the layout contradicts the mutation: an unrecorded GPU instance to
// attach a compute instance to, or an ID that is already recorded. The caller
// has already made the change in its own process, so a mutation that cannot be
// recorded faithfully has to say so instead of reporting a success that
// evaporates on the next process start.

import (
	"bytes"
	"fmt"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// MIGTx is one hold of the override lock, spanning a mutation from the layout
// it will edit, through the change the caller makes in its own engine, to the
// record of that change.
//
// The three belong under one hold because the instance id the engine assigns is
// drawn from the layout this document describes. Split across separate holds,
// two processes read the same layout, assign the same id, and the second one's
// record is refused as a duplicate — a creation that already happened locally,
// reported as a failure and then rolled back. Under one hold the second process
// cannot look until the first has finished writing.
//
// Nothing is written until Commit, so a sequence that fails partway leaves the
// document as it was rather than a seeded layout with no delta against it.
type MIGTx struct {
	path    string
	index   int
	unlock  func()
	doc     *Doc
	bucket  map[string]any
	block   map[string]any
	changed bool
}

// BeginMIG takes the override lock and reads the document the mutation will
// edit. The document is read inside the lock, so a concurrent writer's work is
// never overwritten by a view loaded before it ran — the shape ResetDevice
// uses. The caller must Close the result.
func BeginMIG(path string, index int) (*MIGTx, error) {
	if path == "" {
		// No override file resolved: nothing to persist to, and the in-memory
		// mutation the caller already made stands on its own. The transaction
		// still runs, so a caller's change is never conditional on there being
		// somewhere to record it.
		return &MIGTx{}, nil
	}

	unlock, err := LockOverride(path)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	doc, err := Load(path)
	if err != nil {
		unlock()
		return nil, err
	}
	bucket := doc.bucket(Target{Index: index})
	block, err := migBlock(bucket)
	if err != nil {
		unlock()
		return nil, err
	}
	return &MIGTx{path: path, index: index, unlock: unlock, doc: doc, bucket: bucket, block: block}, nil
}

// Close releases the lock. Uncommitted edits are discarded with it, so Close is
// safe to defer alongside a Commit on the success path.
func (tx *MIGTx) Close() {
	if tx.unlock != nil {
		tx.unlock()
		tx.unlock = nil
	}
}

// Commit writes the accumulated edits, if any changed the document.
func (tx *MIGTx) Commit() error {
	if tx.path == "" || !tx.changed {
		// A mutation that changed nothing must not touch a file the engine
		// watches — nor invent an empty `mig:` block for a device that had no
		// overrides.
		return nil
	}
	tx.bucket["mig"] = tx.block
	return WriteAtomic(tx.path, tx.doc)
}

// apply runs one edit against the transaction's view of the device's MIG state.
func (tx *MIGTx) apply(fn func(*engine.MIGConfig) error) error {
	if tx.path == "" {
		return nil
	}
	changed, err := applyMIG(tx.block, fn)
	if err != nil {
		// A rejected mutation must leave no partial edit behind. The device and
		// the document are context the caller cannot recover from the inner
		// message, which names at most the instance it could not record.
		return fmt.Errorf("recording mig mutation for device %d in %s: %w", tx.index, tx.path, err)
	}
	tx.changed = tx.changed || changed
	return nil
}

// mutateMIG applies fn to the device's recorded MIG state as a transaction of
// its own, for the mutations that stand alone.
func mutateMIG(path string, index int, fn func(*engine.MIGConfig) error) error {
	tx, err := BeginMIG(path, index)
	if err != nil {
		return err
	}
	defer tx.Close()

	if err := tx.apply(fn); err != nil {
		return err
	}
	return tx.Commit()
}

// applyMIG runs the mutation against a typed view of the block and copies the
// result back, reporting whether the block ended up different from how it
// arrived. Comparing the encoded block is what makes a no-op detectable: the
// mutation works on the typed view, which says nothing about the keys it does
// not own.
//
// The comparison sees only what writeMIG copies back — mode_current,
// mode_pending and instances — so a writer that mutates some other MIGConfig
// field would have that part of its work dropped: silently discarded if it
// touched nothing else, or written as a partial edit if it did. Widening the
// typed view a mutation may touch means widening writeMIG with it.
func applyMIG(block map[string]any, fn func(*engine.MIGConfig) error) (bool, error) {
	before, err := yaml.Marshal(block)
	if err != nil {
		return false, fmt.Errorf("encoding mig override: %w", err)
	}
	// Unknown keys are tolerated: they belong to whoever wrote them and travel
	// in the generic block rather than in the typed view.
	var mig engine.MIGConfig
	if err := yaml.Unmarshal(before, &mig); err != nil {
		return false, fmt.Errorf("parsing mig override: %w", err)
	}
	if err := fn(&mig); err != nil {
		return false, err
	}
	if err := writeMIG(block, &mig); err != nil {
		return false, err
	}
	after, err := yaml.Marshal(block)
	if err != nil {
		return false, fmt.Errorf("encoding mig override: %w", err)
	}
	return !bytes.Equal(before, after), nil
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

// writeMIG copies back only the fields a runtime mutation owns. Re-encoding
// the whole typed config instead would drop every key it models as omitempty
// but the block sets to zero, and the engine deep-merges this block over the
// profile — so a dropped `max_gpu_instances: 0` would read as "inherit" and
// silently restore the value the operator overrode.
//
// The nested `instances` list is exempt from that hazard because of how its
// records are typed: every field whose absent-versus-zero distinction the
// engine reads is either non-omitempty (`id`) or a pointer (`profile_id`,
// `placement_start`, `compute_instances`), and omitempty does not omit a
// non-nil pointer. `profile` is omitempty and so is already dropped when it is
// empty, which costs only a one-time rewrite to normalize it away, because an
// absent `profile` and `profile: ""` decode to the same record. A new
// omitempty scalar on MIGGPUInstanceRecord or
// MIGComputeInstanceRecord whose zero the engine has to tell from absent would
// break that, and the list would need the same key-by-key treatment as the
// block above.
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
	return mutateMIG(path, index, setMIGMode(enabled))
}

// SetMode is MIGSetMode within a transaction.
func (tx *MIGTx) SetMode(enabled bool) error { return tx.apply(setMIGMode(enabled)) }

func setMIGMode(enabled bool) func(*engine.MIGConfig) error {
	return func(mig *engine.MIGConfig) error {
		if !enabled {
			mig.ModeCurrent, mig.ModePending = "disabled", "disabled"
			mig.Instances = nil
			return nil
		}
		mig.ModeCurrent, mig.ModePending = "enabled", "enabled"
		return nil
	}
}

// MIGSeedLayout records records as the device's explicit layout, but only
// when it has none: a delta writer edits that layout and cannot invent one,
// so the first runtime mutation on a device has to write down the board it is
// about to change before it can describe the change as a difference.
//
// Seeding a device that already carries a layout would overwrite what earlier
// mutations recorded, so it is skipped rather than merged — which is also
// what makes it safe for every mutation to call unconditionally.
//
// A nil records is nothing to seed rather than an empty layout, the same
// distinction the document itself draws: an empty list records a MIG-enabled
// board with every instance destroyed, and writing that for a board that is
// merely switched off would destroy the partitions its profile declares.
func MIGSeedLayout(path string, index int, records []engine.MIGGPUInstanceRecord) error {
	if records == nil {
		// Nothing to seed is not worth a lock.
		return nil
	}
	return mutateMIG(path, index, seedMIGLayout(records))
}

// SeedLayout is MIGSeedLayout within a transaction. Seeding here rather than in
// a hold of its own is what lets the delta be taken against the layout the
// caller's engine actually saw.
func (tx *MIGTx) SeedLayout(records []engine.MIGGPUInstanceRecord) error {
	return tx.apply(seedMIGLayout(records))
}

func seedMIGLayout(records []engine.MIGGPUInstanceRecord) func(*engine.MIGConfig) error {
	return func(mig *engine.MIGConfig) error {
		if records == nil || mig.Instances != nil {
			return nil
		}
		mig.Instances = &records
		return nil
	}
}

// MIGAddGpuInstance records a newly created GPU instance. The record's compute
// instances are kept as the caller passed them, empty list included: that is
// how `nvidia-smi mig -cgi` without -C differs from an unspecified layout.
//
// An ID the layout already carries is rejected: NVML allocates these, so a
// duplicate means a replayed or mis-sequenced mutation, and appending a second
// entry would make a later MIGRemoveGpuInstance remove both copies at once.
func MIGAddGpuInstance(path string, index int, rec engine.MIGGPUInstanceRecord) error {
	return mutateMIG(path, index, addMIGGpuInstance(rec))
}

// AddGpuInstance is MIGAddGpuInstance within a transaction.
func (tx *MIGTx) AddGpuInstance(rec engine.MIGGPUInstanceRecord) error {
	return tx.apply(addMIGGpuInstance(rec))
}

func addMIGGpuInstance(rec engine.MIGGPUInstanceRecord) func(*engine.MIGConfig) error {
	return func(mig *engine.MIGConfig) error {
		list := instancesOf(mig)
		for _, gi := range list {
			if gi.ID == rec.ID {
				return fmt.Errorf("gpu instance %d is already recorded", rec.ID)
			}
		}
		mig.Instances = ptr(append(list, rec))
		return nil
	}
}

// MIGRemoveGpuInstance records a destroyed GPU instance. The list stays
// present when it empties: a MIG-enabled board with nothing on it is a real
// state, and an absent list would fall back to the profile's partitions.
//
// A device with no recorded layout at all is rejected instead. Absent means
// "the profile's declared counts stand", so there is no delta to take one
// instance out of; writing the remainder as an empty list would record the
// destruction of every other instance the profile declares. A caller that
// records the live layout before mutating it always presents one.
func MIGRemoveGpuInstance(path string, index int, giID uint32) error {
	return mutateMIG(path, index, removeMIGGpuInstance(giID))
}

// RemoveGpuInstance is MIGRemoveGpuInstance within a transaction.
func (tx *MIGTx) RemoveGpuInstance(giID uint32) error {
	return tx.apply(removeMIGGpuInstance(giID))
}

func removeMIGGpuInstance(giID uint32) func(*engine.MIGConfig) error {
	return func(mig *engine.MIGConfig) error {
		if mig.Instances == nil {
			return fmt.Errorf("gpu instance %d is not recorded", giID)
		}
		kept := make([]engine.MIGGPUInstanceRecord, 0, len(instancesOf(mig)))
		for _, gi := range instancesOf(mig) {
			if gi.ID != giID {
				kept = append(kept, gi)
			}
		}
		mig.Instances = &kept
		return nil
	}
}

// MIGAddComputeInstance records a compute instance created inside giID. Both
// an unrecorded GPU instance and an ID that instance already carries are
// rejected, for the reasons MIGAddGpuInstance and migRecord give.
func MIGAddComputeInstance(path string, index int, giID uint32, rec engine.MIGComputeInstanceRecord) error {
	return mutateMIG(path, index, addMIGComputeInstance(giID, rec))
}

// AddComputeInstance is MIGAddComputeInstance within a transaction.
func (tx *MIGTx) AddComputeInstance(giID uint32, rec engine.MIGComputeInstanceRecord) error {
	return tx.apply(addMIGComputeInstance(giID, rec))
}

func addMIGComputeInstance(giID uint32, rec engine.MIGComputeInstanceRecord) func(*engine.MIGConfig) error {
	return func(mig *engine.MIGConfig) error {
		gi, err := migRecord(mig, giID)
		if err != nil {
			return err
		}
		cis := computeInstancesOf(*gi)
		for _, ci := range cis {
			if ci.ID == rec.ID {
				return fmt.Errorf("compute instance %d is already recorded in gpu instance %d", rec.ID, giID)
			}
		}
		gi.ComputeInstances = ptr(append(cis, rec))
		return nil
	}
}

// MIGRemoveComputeInstance records a destroyed compute instance.
//
// Unlike MIGRemoveGpuInstance, an absent list is emptied rather than rejected:
// absent here means the engine supplies one compute instance spanning the GPU
// instance, so recording the empty list destroys exactly the instance the
// conventional `ciID == 0` names — a faithful delta, not a guess. That holds
// only for that one id; any other id against an absent list names an instance
// the layout does not hold, and emptying the list would destroy the spanning
// instance instead of the one asked for.
func MIGRemoveComputeInstance(path string, index int, giID, ciID uint32) error {
	return mutateMIG(path, index, removeMIGComputeInstance(giID, ciID))
}

// RemoveComputeInstance is MIGRemoveComputeInstance within a transaction.
func (tx *MIGTx) RemoveComputeInstance(giID, ciID uint32) error {
	return tx.apply(removeMIGComputeInstance(giID, ciID))
}

func removeMIGComputeInstance(giID, ciID uint32) func(*engine.MIGConfig) error {
	return func(mig *engine.MIGConfig) error {
		gi, err := migRecord(mig, giID)
		if err != nil {
			return err
		}
		if gi.ComputeInstances == nil && ciID != 0 {
			return fmt.Errorf("compute instance %d is not recorded in gpu instance %d", ciID, giID)
		}
		cis := computeInstancesOf(*gi)
		kept := make([]engine.MIGComputeInstanceRecord, 0, len(cis))
		for _, ci := range cis {
			if ci.ID != ciID {
				kept = append(kept, ci)
			}
		}
		// Stays present when it empties, for the same reason the GPU instance
		// list does: an instance with no compute instances is a real state, not
		// an unspecified one.
		gi.ComputeInstances = &kept
		return nil
	}
}

// migRecord finds the record for giID, aliasing the layout so edits through it
// are edits to the layout. An absent record is an error rather than a skipped
// write: the caller's compute instance exists in its own process, and dropping
// it here would make it vanish at the next process start.
func migRecord(mig *engine.MIGConfig, giID uint32) (*engine.MIGGPUInstanceRecord, error) {
	list := instancesOf(mig)
	for i := range list {
		if list[i].ID == giID {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("gpu instance %d is not recorded", giID)
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
