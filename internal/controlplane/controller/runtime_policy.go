// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controller

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpupolicy "github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
	sgpustatus "github.com/NVIDIA/k8s-test-infra/internal/sgpu/status"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
)

// runtimePolicyByTargetIndex groups runtime policies by the inventory they
// target, because conflicts are settled among the policies of one inventory.
const runtimePolicyByTargetIndex = "mokkaRuntimePolicyByTarget"

// ErrStaleRuntimeInputs indicates that the cached inventory or profile is not
// the revision a rack was rendered from. It lasts until the rack re-renders,
// so callers retry instead of serving state that mixes revisions.
var ErrStaleRuntimeInputs = errors.New("cached runtime inputs do not match the rendered rack")

// GPURuntime is the effective runtime state of one rendered GPU.
type GPURuntime struct {
	Index   int32
	Runtime mokkav1alpha1.RuntimeState
}

func runtimePolicyIndexers() cache.Indexers {
	return cache.Indexers{runtimePolicyByTargetIndex: runtimePolicyByTarget}
}

func runtimePolicyByTarget(object any) ([]string, error) {
	policy, ok := object.(*mokkav1alpha1.SGPURuntimePolicy)
	if !ok {
		return nil, fmt.Errorf("runtime policy index received %T", object)
	}
	return []string{policy.Spec.TargetRef.Name}, nil
}

// runtimePolicyView evaluates the runtime policies in a replica's informer
// caches. It needs no event handlers, so a standby replica serves the same
// view as the leader.
type runtimePolicyView struct {
	inventories mokkalisters.SGPUInventoryLister
	profiles    mokkalisters.SGPURackProfileLister
	policies    cache.Indexer
}

// evaluate decides every runtime policy that targets the named inventory.
func (v *runtimePolicyView) evaluate(inventoryName string) (*sgpupolicy.Evaluation, error) {
	inventory, err := v.inventories.Get(inventoryName)
	if apierrors.IsNotFound(err) {
		return v.evaluateInventory(inventoryName, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("get inventory %q from cache: %w", inventoryName, err)
	}
	return v.evaluateInventory(inventoryName, inventory)
}

func (v *runtimePolicyView) evaluateInventory(
	inventoryName string,
	inventory *mokkav1alpha1.SGPUInventory,
) (*sgpupolicy.Evaluation, error) {
	profiles, err := v.referencedProfiles(inventory)
	if err != nil {
		return nil, err
	}

	objects, err := v.policies.ByIndex(runtimePolicyByTargetIndex, inventoryName)
	if err != nil {
		return nil, fmt.Errorf("look up runtime policies for inventory %q: %w", inventoryName, err)
	}

	policies := make([]*mokkav1alpha1.SGPURuntimePolicy, 0, len(objects))

	for _, object := range objects {
		policy, ok := object.(*mokkav1alpha1.SGPURuntimePolicy)

		if !ok {
			return nil, fmt.Errorf("runtime policy cache contained %T", object)
		}

		policies = append(policies, policy)
	}

	evaluation, err := sgpupolicy.Evaluate(inventoryName, inventory, profiles, policies)
	if err != nil {
		return nil, fmt.Errorf("evaluate runtime policies for inventory %q: %w", inventoryName, err)
	}

	return evaluation, nil
}

// referencedProfiles returns the cached profiles an inventory's rack groups
// reference. A missing profile is left out: its rack groups have no Node or
// GPU indexes until it appears.
func (v *runtimePolicyView) referencedProfiles(
	inventory *mokkav1alpha1.SGPUInventory,
) (map[string]*mokkav1alpha1.SGPURackProfile, error) {
	if inventory == nil {
		return nil, nil
	}
	profiles := make(map[string]*mokkav1alpha1.SGPURackProfile, len(inventory.Spec.RackGroups))
	for _, group := range inventory.Spec.RackGroups {
		profile, err := v.profiles.Get(group.ProfileRef.Name)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get profile %q from cache: %w", group.ProfileRef.Name, err)
		}
		profiles[profile.Name] = profile
	}
	return profiles, nil
}

// nodeRuntime compiles the effective runtime state of every GPU on one
// logical Node of a rack, in the order the rack lists them.
func (v *runtimePolicyView) nodeRuntime(
	rack *mokkav1alpha1.SGPURack,
	slot *mokkav1alpha1.SGPURackNode,
) ([]GPURuntime, error) {
	inventory, err := v.renderedInventory(rack.Spec.InventoryRef)
	if err != nil {
		return nil, err
	}
	profile, err := v.renderedProfile(rack.Spec.ProfileRef)
	if err != nil {
		return nil, err
	}
	evaluation, err := v.evaluateInventory(inventory.Name, inventory)
	if err != nil {
		return nil, err
	}
	var defaults *mokkav1alpha1.RuntimeState
	if profile.Spec.Defaults != nil {
		defaults = profile.Spec.Defaults.Runtime
	}
	runtimes := make([]GPURuntime, 0, len(slot.GPUs))
	for _, gpu := range slot.GPUs {
		at := sgpupolicy.Coordinate{
			RackGroup: rack.Spec.Identity.RackGroup,
			RackIndex: rack.Spec.Identity.RackIndex,
			NodeIndex: slot.Index,
			GPUIndex:  gpu.Index,
		}
		runtime, err := evaluation.Runtime(defaults, at)
		if err != nil {
			return nil, fmt.Errorf("compile the runtime of GPU %d on Node %d of rack %q: %w",
				gpu.Index, slot.Index, rack.Name, err)
		}
		runtimes = append(runtimes, GPURuntime{Index: gpu.Index, Runtime: runtime})
		zap.L().Debug("Compiled GPU runtime", zap.String("rack", rack.Name), zap.Int32("nodeIndex", slot.Index),
			zap.Int32("gpuIndex", gpu.Index), zap.Strings("policies", policyNames(evaluation.Applied(at))))
	}
	return runtimes, nil
}

// renderedInventory returns the cached inventory when it is the instance the
// rack belongs to.
func (v *runtimePolicyView) renderedInventory(
	ref mokkav1alpha1.SGPURackInventoryReference,
) (*mokkav1alpha1.SGPUInventory, error) {
	inventory, err := v.inventories.Get(ref.Name)
	if apierrors.IsNotFound(err) || (err == nil && inventory.UID != ref.UID) {
		return nil, fmt.Errorf("%w: SGPUInventory %q is no longer UID %q", ErrStaleRuntimeInputs, ref.Name, ref.UID)
	}
	if err != nil {
		return nil, fmt.Errorf("get inventory %q from cache: %w", ref.Name, err)
	}
	return inventory, nil
}

// renderedProfile returns the cached profile when it is the exact generation
// the rack was rendered from. Profile revisions cover the runtime defaults, so
// a newer generation means the rack has yet to re-render.
func (v *runtimePolicyView) renderedProfile(
	ref mokkav1alpha1.SGPURackProfileReference,
) (*mokkav1alpha1.SGPURackProfile, error) {
	profile, err := v.profiles.Get(ref.Name)
	if apierrors.IsNotFound(err) || (err == nil && (profile.UID != ref.UID || profile.Generation != ref.Generation)) {
		return nil, fmt.Errorf("%w: SGPURackProfile %q is no longer generation %d of UID %q",
			ErrStaleRuntimeInputs, ref.Name, ref.Generation, ref.UID)
	}
	if err != nil {
		return nil, fmt.Errorf("get profile %q from cache: %w", ref.Name, err)
	}
	return profile, nil
}

// policyNames lists policy names in order, for log entries.
func policyNames(policies []*mokkav1alpha1.SGPURuntimePolicy) []string {
	names := make([]string, 0, len(policies))
	for _, policy := range policies {
		names = append(names, policy.Name)
	}

	return names
}

// EffectiveRuntime compiles the effective runtime state of every GPU on an
// assignment's logical Node: the profile defaults overridden by each accepted
// runtime policy that selects the GPU. Any replica with synchronized caches
// can serve it. It returns ErrCacheNotReady before the caches synchronize and
// an error wrapping ErrStaleRuntimeInputs while the rack's inventory or
// profile is changing.
func (c *Controller) EffectiveRuntime(assignment AssignmentSnapshot) ([]GPURuntime, error) {
	if assignment.Rack == nil || assignment.Node == nil {
		return nil, errors.New("effective runtime requires a rack and one of its logical Nodes")
	}
	if c == nil || !c.CacheReady() || c.runtimePolicies == nil {
		return nil, ErrCacheNotReady
	}
	return c.runtimePolicies.nodeRuntime(assignment.Rack, assignment.Node)
}

// reconcileRuntimePolicyStatus publishes the decision for every runtime
// policy that targets one inventory. A failed write does not hold back the
// other policies; the joined error retries the inventory, and policies whose
// status already converged cost no API calls on the retry.
func reconcileRuntimePolicyStatus(
	ctx context.Context,
	view *runtimePolicyView,
	reconciler *sgpustatus.RuntimePolicyReconciler,
	inventoryName string,
) error {
	evaluation, err := view.evaluate(inventoryName)
	if err != nil {
		return err
	}

	var errs []error
	accepted := 0

	for _, decision := range evaluation.Decisions {
		zap.L().Debug("Runtime policy decided", zap.String("policy", decision.Policy.Name),
			zap.String("inventory", inventoryName), zap.Stringer("scope", decision.Scope),
			zap.String("outcome", string(decision.Outcome)), zap.String("message", decision.Message))
		if decision.Outcome.Accepted() {
			accepted++
		}
		if _, err := reconciler.Reconcile(ctx, decision); err != nil {
			errs = append(errs, err)
		}
	}

	zap.L().Debug("Evaluated runtime policies", zap.String("inventory", inventoryName),
		zap.Int("policies", len(evaluation.Decisions)), zap.Int("accepted", accepted))

	return errors.Join(errs...)
}
