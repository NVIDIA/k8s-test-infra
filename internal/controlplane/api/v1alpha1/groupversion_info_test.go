// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToScheme(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))

	kinds := []string{
		"SGPUProfile", "SGPUProfileList",
		"SGPUInventory", "SGPUInventoryList",
		"SGPURuntimePolicy", "SGPURuntimePolicyList",
	}
	for _, k := range kinds {
		require.Truef(t, scheme.Recognizes(GroupVersion.WithKind(k)),
			"expected scheme to recognize %s", k)
	}
}

func TestResource(t *testing.T) {
	t.Parallel()

	gr := Resource("sgpuprofiles")
	require.Equal(t, GroupName, gr.Group)
	require.Equal(t, "sgpuprofiles", gr.Resource)
}
