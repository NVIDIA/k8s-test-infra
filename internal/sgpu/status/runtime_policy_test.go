// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package status

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	sgpupolicy "github.com/NVIDIA/k8s-test-infra/internal/sgpu/policy"
)

func TestComputeRuntimePolicyReportsTheOutcomeAsTheAcceptedCondition(t *testing.T) {
	t.Parallel()

	now := metav1.NewTime(time.Unix(200, 0))
	tests := []struct {
		outcome sgpupolicy.Outcome
		want    metav1.ConditionStatus
	}{
		{outcome: sgpupolicy.Accepted, want: metav1.ConditionTrue},
		{outcome: sgpupolicy.TargetNotFound, want: metav1.ConditionFalse},
		{outcome: sgpupolicy.InvalidTarget, want: metav1.ConditionFalse},
		{outcome: sgpupolicy.Conflicted, want: metav1.ConditionFalse},
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			t.Parallel()
			decision := runtimePolicyDecision(tt.outcome)

			status := ComputeRuntimePolicy(decision, nil, now)

			require.Equal(t, []metav1.Condition{{
				Type:               mokkav1alpha1.RuntimePolicyConditionAccepted,
				Status:             tt.want,
				Reason:             string(tt.outcome),
				Message:            decision.Message,
				ObservedGeneration: 3,
				LastTransitionTime: now,
			}}, status.Conditions)
		})
	}
}

func TestComputeRuntimePolicySummarizesTheTargetInOrder(t *testing.T) {
	t.Parallel()

	decision := runtimePolicyDecision(sgpupolicy.Accepted)
	decision.Policy.Spec.TargetRef.RackGroups = []string{"training", "inference"}
	decision.Policy.Spec.TargetRef.RackIndexes = []int32{10, 2}
	decision.Policy.Spec.TargetRef.GPUIndexes = []int32{0}

	status := ComputeRuntimePolicy(decision, nil, metav1.NewTime(time.Unix(200, 0)))

	require.Equal(t, "inference,training", status.RackGroupsSummary)
	require.Equal(t, "2,10", status.RackIndexesSummary)
	require.Empty(t, status.NodeIndexesSummary, "an omitted axis has no summary")
	require.Equal(t, "0", status.GPUIndexesSummary)
	require.Equal(t, []string{"training", "inference"}, decision.Policy.Spec.TargetRef.RackGroups,
		"the cached target keeps its order")
}

func TestComputeRuntimePolicyKeepsTheTransitionTimeWhileTheOutcomeHolds(t *testing.T) {
	t.Parallel()

	earlier := metav1.NewTime(time.Unix(100, 0))
	now := metav1.NewTime(time.Unix(200, 0))
	previous := ComputeRuntimePolicy(runtimePolicyDecision(sgpupolicy.Conflicted), nil, earlier).Conditions

	rewritten := runtimePolicyDecision(sgpupolicy.Conflicted)
	rewritten.Message = `Policy "newer-winner" takes precedence at GPU scope and also sets deviceState for some of the same GPUs.`
	held := ComputeRuntimePolicy(rewritten, previous, now).Conditions[0]
	require.Equal(t, earlier, held.LastTransitionTime, "a new message alone is not a transition")
	require.Equal(t, rewritten.Message, held.Message)

	flipped := ComputeRuntimePolicy(runtimePolicyDecision(sgpupolicy.Accepted), previous, now).Conditions[0]
	require.Equal(t, now, flipped.LastTransitionTime)
}

func TestConvergedRuntimePolicyStatusAvoidsLiveRequests(t *testing.T) {
	t.Parallel()

	now := metav1.NewTime(time.Unix(200, 0))
	decision := runtimePolicyDecision(sgpupolicy.Accepted)
	decision.Policy.Status = ComputeRuntimePolicy(decision, nil, now)
	writer := &fakeRuntimePolicyWriter{getErr: errors.New("a converged cached status must not issue a live GET")}
	reconciler := NewRuntimePolicyReconciler(writer, func() metav1.Time { return now })

	changed, err := reconciler.Reconcile(context.Background(), decision)

	require.NoError(t, err)
	require.False(t, changed)
	require.Zero(t, writer.gets)
	require.Zero(t, writer.updates)
}

func TestRuntimePolicyStatusWritesAgainstTheLiveObject(t *testing.T) {
	t.Parallel()

	now := metav1.NewTime(time.Unix(200, 0))
	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "sgpuruntimepolicies"}, "hot-gpus")
	converged := func(live *mokkav1alpha1.SGPURuntimePolicy, decision sgpupolicy.Decision) {
		live.Status = ComputeRuntimePolicy(decision, nil, now)
	}
	tests := []struct {
		name        string
		live        func(*mokkav1alpha1.SGPURuntimePolicy, sgpupolicy.Decision)
		writer      fakeRuntimePolicyWriter
		wantChanged bool
		wantGets    int
		wantUpdates int
		wantErr     bool
		wantWritten bool
	}{
		{
			name:     "stale cache and converged live object write nothing",
			live:     converged,
			wantGets: 1,
		},
		{
			name:        "outdated live status is written",
			wantChanged: true,
			wantGets:    1,
			wantUpdates: 1,
			wantWritten: true,
		},
		{
			name:        "conflict is retried against a fresh read",
			writer:      fakeRuntimePolicyWriter{conflictOnce: true},
			wantChanged: true,
			wantGets:    2,
			wantUpdates: 2,
			wantWritten: true,
		},
		{
			name:     "deleted policy is left to its delete event",
			writer:   fakeRuntimePolicyWriter{getErr: notFound},
			wantGets: 1,
		},
		{
			name:     "replacement policy with the same name is left to its own event",
			live:     func(live *mokkav1alpha1.SGPURuntimePolicy, _ sgpupolicy.Decision) { live.UID = "replacement-uid" },
			wantGets: 1,
		},
		{
			name:     "newer spec is left to its own event",
			live:     func(live *mokkav1alpha1.SGPURuntimePolicy, _ sgpupolicy.Decision) { live.Generation++ },
			wantGets: 1,
		},
		{
			name:        "policy deleted during the write is left to its delete event",
			writer:      fakeRuntimePolicyWriter{updateErr: notFound},
			wantGets:    1,
			wantUpdates: 1,
		},
		{
			name:     "other failures are returned",
			writer:   fakeRuntimePolicyWriter{getErr: errors.New("apiserver unavailable")},
			wantGets: 1,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			decision := runtimePolicyDecision(sgpupolicy.Accepted)
			writer := tt.writer
			writer.object = decision.Policy.DeepCopy()
			if tt.live != nil {
				tt.live(writer.object, decision)
			}
			reconciler := NewRuntimePolicyReconciler(&writer, func() metav1.Time { return now })

			changed, err := reconciler.Reconcile(context.Background(), decision)

			if tt.wantErr {
				require.ErrorContains(t, err, `update runtime policy "hot-gpus" status`)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantChanged, changed)
			require.Equal(t, tt.wantGets, writer.gets)
			require.Equal(t, tt.wantUpdates, writer.updates)
			if tt.wantWritten {
				require.Equal(t, ComputeRuntimePolicy(decision, nil, now), writer.object.Status)
			}
		})
	}
}

func runtimePolicyDecision(outcome sgpupolicy.Outcome) sgpupolicy.Decision {
	return sgpupolicy.Decision{
		Policy: &mokkav1alpha1.SGPURuntimePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "hot-gpus", UID: "hot-gpus-uid", Generation: 3},
			Spec: mokkav1alpha1.SGPURuntimePolicySpec{TargetRef: mokkav1alpha1.PolicyTargetRef{
				Group: mokkav1alpha1.GroupName, Kind: "SGPUInventory", Name: "dev", GPUIndexes: []int32{2},
			}},
		},
		Scope:   sgpupolicy.ScopeGPU,
		Outcome: outcome,
		Message: "The policy outcome is " + string(outcome) + ".",
	}
}

type fakeRuntimePolicyWriter struct {
	object       *mokkav1alpha1.SGPURuntimePolicy
	getErr       error
	updateErr    error
	conflictOnce bool
	gets         int
	updates      int
}

func (f *fakeRuntimePolicyWriter) Get(
	context.Context, string, metav1.GetOptions,
) (*mokkav1alpha1.SGPURuntimePolicy, error) {
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.object.DeepCopy(), nil
}

func (f *fakeRuntimePolicyWriter) UpdateStatus(
	_ context.Context, candidate *mokkav1alpha1.SGPURuntimePolicy, _ metav1.UpdateOptions,
) (*mokkav1alpha1.SGPURuntimePolicy, error) {
	f.updates++
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.conflictOnce {
		f.conflictOnce = false
		return nil, apierrors.NewConflict(
			schema.GroupResource{Resource: "sgpuruntimepolicies"}, candidate.Name, errors.New("test conflict"),
		)
	}
	f.object = candidate.DeepCopy()
	return f.object.DeepCopy(), nil
}
