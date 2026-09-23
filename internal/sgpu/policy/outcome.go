// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

// Outcome is the result of evaluating a policy: accepted, or the reason it is
// not. Each outcome is also the reason of the policy's Accepted condition.
type Outcome string

// Outcomes form one closed vocabulary: Accepted classifies every one of them.
const (
	// Accepted means the policy is valid and no older policy of the same scope
	// sets any of its fields for the GPUs it selects.
	Accepted Outcome = "Accepted"
	// TargetNotFound means the target inventory does not exist.
	TargetNotFound Outcome = "TargetNotFound"
	// InvalidTarget means the target lists a rack group the inventory does not
	// declare or an index that selects no GPU.
	InvalidTarget Outcome = "InvalidTarget"
	// Conflicted means an older policy of the same scope sets one of the
	// policy's fields for some of the same GPUs.
	Conflicted Outcome = "Conflicted"
)

// Accepted reports whether the policy applies to its target.
func (o Outcome) Accepted() bool {
	switch o {
	case Accepted:
		return true
	case TargetNotFound, InvalidTarget, Conflicted:
		return false
	}
	return false
}
