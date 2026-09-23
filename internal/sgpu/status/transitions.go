// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package status

import (
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// conditionTransitions returns the conditions whose status or reason differs
// from the previous status, including new ones. A changed message alone is not
// a transition: messages carry counts that move with every write.
func conditionTransitions(before, after []metav1.Condition) []metav1.Condition {
	transitions := make([]metav1.Condition, 0, len(after))

	for _, condition := range after {
		previous := meta.FindStatusCondition(before, condition.Type)
		if previous == nil || previous.Status != condition.Status || previous.Reason != condition.Reason {
			transitions = append(transitions, condition)
		}
	}

	return transitions
}

// conditionFields describes a condition in log entries.
func conditionFields(condition metav1.Condition) []zap.Field {
	return []zap.Field{
		zap.String("type", condition.Type),
		zap.String("status", string(condition.Status)),
		zap.String("reason", condition.Reason),
		zap.String("message", condition.Message),
	}
}
