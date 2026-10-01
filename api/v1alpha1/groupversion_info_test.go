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
		"SGPURackProfile", "SGPURackProfileList",
		"SGPUInventory", "SGPUInventoryList",
		"SGPURuntimePolicy", "SGPURuntimePolicyList",
		"SGPURack", "SGPURackList",
	}
	for _, k := range kinds {
		require.Truef(t, scheme.Recognizes(GroupVersion.WithKind(k)),
			"expected scheme to recognize %s", k)
	}
	require.False(t, scheme.Recognizes(GroupVersion.WithKind("SGPUProfile")))
}

func TestResource(t *testing.T) {
	t.Parallel()

	gr := Resource("sgpurackprofiles")
	require.Equal(t, GroupName, gr.Group)
	require.Equal(t, "sgpurackprofiles", gr.Resource)
}
