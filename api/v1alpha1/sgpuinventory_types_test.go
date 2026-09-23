// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRackGroupNodeSelector(t *testing.T) {
	t.Parallel()

	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"pool": "training"}}
	unconstrained := RackGroup{}
	placementWithoutSelector := RackGroup{Placement: &RackPlacement{}}
	constrained := RackGroup{Placement: &RackPlacement{NodeSelector: selector}}

	require.Nil(t, unconstrained.NodeSelector())
	require.Nil(t, placementWithoutSelector.NodeSelector())
	require.Same(t, selector, constrained.NodeSelector())
}
