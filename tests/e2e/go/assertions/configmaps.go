//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"context"
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/kube"
)

// FGOProfileConfigMaps asserts that every named profile is published in the
// shape the fake-gpu-operator's loader reads: a ConfigMap named
// `gpu-profile-<profile>`, carrying `fake-gpu-operator/gpu-profile: "true"`,
// with the profile body under the `profile.yaml` data key.
//
// Each ConfigMap is fetched by exact name, which is what makes the name half
// of the contract testable: FGO does a Get, not a List by label, so a rename
// is a NotFound rather than a smaller result set. The previous version of this
// assertion counted ConfigMaps carrying a label we control and required only
// 6 of the 7 — it could not fail on any of the three fields that govern
// interop, and stayed green throughout the period the integration was broken.
func FGOProfileConfigMaps(ctx context.Context, k *kube.Client, ns string, profiles []string) {
	ginkgo.GinkgoHelper()

	gomega.Expect(profiles).NotTo(gomega.BeEmpty(),
		"no profiles to assert; the contract check would be vacuous")

	for _, p := range profiles {
		name := FGOProfileConfigMapName(p)
		ginkgo.By(fmt.Sprintf("%s/%s matches the fake-gpu-operator discovery contract", ns, name))

		cm, err := k.GetConfigMap(ctx, ns, name)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(),
			"fake-gpu-operator loads profile %q with a Get on %q; that name must exist in %s", p, name, ns)

		problems := DiffFGOProfileConfigMap(p, name, cm.Labels, cm.Data)
		gomega.Expect(problems).To(gomega.BeEmpty(),
			"ConfigMap %s/%s departs from the fake-gpu-operator contract:\n  %s",
			ns, name, strings.Join(problems, "\n  "))
	}
}
