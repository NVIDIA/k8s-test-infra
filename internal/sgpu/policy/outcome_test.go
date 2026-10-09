// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOnlyTheAcceptedOutcomeApplies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		outcome Outcome
		want    bool
	}{
		{outcome: Accepted, want: true},
		{outcome: TargetNotFound},
		{outcome: InvalidTarget},
		{outcome: Conflicted},
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.outcome.Accepted())
		})
	}
}
