// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSGPURackProfileReferencePinsObservedInstance(t *testing.T) {
	t.Parallel()

	profile := &SGPURackProfile{ObjectMeta: metav1.ObjectMeta{Name: "gb200", UID: "profile-uid", Generation: 3}}

	require.Equal(t, SGPURackProfileReference{
		Name: "gb200", UID: "profile-uid", Generation: 3, Revision: "content-revision",
	}, profile.Reference("content-revision"))
}
