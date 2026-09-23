// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package status

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpupolicy "github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
)

// RuntimePolicyStatusWriter is the narrow live-client surface for runtime
// policy status.
type RuntimePolicyStatusWriter interface {
	Get(context.Context, string, metav1.GetOptions) (*mokkav1alpha1.SGPURuntimePolicy, error)
	UpdateStatus(
		context.Context, *mokkav1alpha1.SGPURuntimePolicy, metav1.UpdateOptions,
	) (*mokkav1alpha1.SGPURuntimePolicy, error)
}

// RuntimePolicyReconciler writes runtime policy status only when its semantic
// value changes.
type RuntimePolicyReconciler struct {
	policies RuntimePolicyStatusWriter
	now      func() metav1.Time
}

// NewRuntimePolicyReconciler constructs an idempotent runtime policy status writer.
func NewRuntimePolicyReconciler(policies RuntimePolicyStatusWriter, now func() metav1.Time) *RuntimePolicyReconciler {
	if now == nil {
		now = metav1.Now
	}
	return &RuntimePolicyReconciler{policies: policies, now: now}
}

// ComputeRuntimePolicy describes one policy decision: sorted summaries of the
// target axes for print columns, and an Accepted condition whose reason is
// the decision's outcome.
func ComputeRuntimePolicy(
	decision sgpupolicy.Decision,
	previous []metav1.Condition,
	now metav1.Time,
) mokkav1alpha1.SGPURuntimePolicyStatus {
	accepted := metav1.Condition{
		Type:    mokkav1alpha1.RuntimePolicyConditionAccepted,
		Status:  metav1.ConditionFalse,
		Reason:  string(decision.Outcome),
		Message: decision.Message,
	}
	if decision.Outcome.Accepted() {
		accepted.Status = metav1.ConditionTrue
	}
	target := decision.Policy.Spec.TargetRef
	return mokkav1alpha1.SGPURuntimePolicyStatus{
		RackGroupsSummary:  strings.Join(slices.Sorted(slices.Values(target.RackGroups)), ","),
		RackIndexesSummary: indexesSummary(target.RackIndexes),
		NodeIndexesSummary: indexesSummary(target.NodeIndexes),
		GPUIndexesSummary:  indexesSummary(target.GPUIndexes),
		Conditions:         mergeConditions(previous, []metav1.Condition{accepted}, decision.Policy.Generation, now),
	}
}

func indexesSummary(indexes []int32) string {
	formatted := make([]string, 0, len(indexes))
	for _, index := range slices.Sorted(slices.Values(indexes)) {
		formatted = append(formatted, strconv.FormatInt(int64(index), 10))
	}
	return strings.Join(formatted, ",")
}

// Reconcile writes the status of one decided policy. A cached status that
// already matches is trusted without an API call, which keeps many converged
// policies cheap; every status change reaches the controller as an informer
// event, so a cached status that turns out stale is examined again.
func (r *RuntimePolicyReconciler) Reconcile(ctx context.Context, decision sgpupolicy.Decision) (bool, error) {
	cached := decision.Policy
	if cached == nil || cached.Name == "" || cached.UID == "" {
		return false, errors.New("runtime policy status requires exact name and UID")
	}
	now := r.now()
	if equality.Semantic.DeepEqual(cached.Status, ComputeRuntimePolicy(decision, cached.Status.Conditions, now)) {
		return false, nil
	}
	changed := false
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		changed, err = r.writeLive(ctx, decision, now)
		return err
	})
	if err != nil {
		return changed, fmt.Errorf("update runtime policy %q status: %w", cached.Name, err)
	}
	return changed, nil
}

// writeLive updates the live policy when it is still the revision the
// decision describes.
func (r *RuntimePolicyReconciler) writeLive(
	ctx context.Context,
	decision sgpupolicy.Decision,
	now metav1.Time,
) (bool, error) {
	cached := decision.Policy
	latest, err := r.policies.Get(ctx, cached.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// The policy's delete event re-evaluates the policies that remain.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if latest.UID != cached.UID || latest.Generation != cached.Generation {
		// A replacement or a newer spec reaches the controller as its own
		// event and is evaluated then; this decision describes an older one.
		return false, nil
	}
	desired := ComputeRuntimePolicy(decision, latest.Status.Conditions, now)
	if equality.Semantic.DeepEqual(latest.Status, desired) {
		return false, nil
	}
	candidate := latest.DeepCopy()
	candidate.Status = desired
	_, err = r.policies.UpdateStatus(ctx, candidate, metav1.UpdateOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// Deleted since the read; its delete event re-evaluates the rest.
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}
