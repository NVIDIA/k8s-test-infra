// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package v1alpha1

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apixv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func TestGeneratedCRDsAreRetainedOnHelmUninstall(t *testing.T) {
	chartTemplates := filepath.Join(
		"..", "..", "..", "..", "deployments", "mokka-crds", "helm", "mokka-crds", "templates",
	)
	paths, err := filepath.Glob(filepath.Join(chartTemplates, "mokka.nvidia.com_*.yaml"))
	require.NoError(t, err)
	require.Len(t, paths, 4)

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			var crd apixv1.CustomResourceDefinition
			require.NoError(t, yaml.Unmarshal(data, &crd))
			require.Equal(t, "keep", crd.Annotations["helm.sh/resource-policy"])
		})
	}
}
