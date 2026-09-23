// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package status

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConditionTransitionsIgnoreMessageOnlyChanges(t *testing.T) {
	t.Parallel()

	before := []metav1.Condition{
		{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted", Message: "2 racks"},
		{Type: "Programmed", Status: metav1.ConditionFalse, Reason: "RacksPending"},
		{Type: "ResolvedRefs", Status: metav1.ConditionTrue, Reason: "ProfilesResolved"},
		{Type: "RequestsSatisfied", Status: metav1.ConditionFalse, Reason: "PendingNodes"},
	}
	after := []metav1.Condition{
		{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted", Message: "3 racks"},
		{Type: "Programmed", Status: metav1.ConditionFalse, Reason: "ProjectionIncomplete"},
		{Type: "ResolvedRefs", Status: metav1.ConditionFalse, Reason: "ProfileNotFound"},
		{Type: "RequestsSatisfied", Status: metav1.ConditionFalse, Reason: "PendingNodes"},
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"},
	}

	require.Equal(t, []metav1.Condition{after[1], after[2], after[4]}, conditionTransitions(before, after),
		"a new reason, a new status, and a new condition are transitions; a new message alone is not")
}
