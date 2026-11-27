// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package assignment

import (
	"testing"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPublicationFloorSurvivesLaterDelivery(t *testing.T) {
	rack := &api.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "rack", UID: "rack-id", Generation: 3}}
	rack.Spec.Identity.RackGroup = "train"
	slot := api.SGPURackNode{NodeRef: &api.SGPUNodeReference{Name: "worker", UID: "worker-id", AssignmentRevision: 1}}
	rack.Status.Delivery = &api.RackDelivery{Generation: 7, RackSpecGeneration: 3}
	raw, err := EncodeAssignment(rack, &slot)
	require.NoError(t, err)
	a, err := DecodeAssignment(raw)
	require.NoError(t, err)
	require.EqualValues(t, 7, a.RackDeliveryGenerationFloor)
	rack.Status.Delivery.Generation++
	require.True(t, a.CoversBinding(rack, &slot))
	rack.Status.Delivery.Generation = 6
	require.False(t, a.CoversBinding(rack, &slot))
}
