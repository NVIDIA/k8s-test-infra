// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package tier_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/k8s-test-infra/cmd/nvml-mock-injector/tier"
)

func TestResolve(t *testing.T) {
	cfg := tier.Config{
		GPUOperatorComponents: map[string]struct{}{
			"nvidia-device-plugin":        {},
			"gpu-feature-discovery":       {},
			"nvidia-operator-validator":   {},
		},
	}

	tests := []struct {
		name    string
		pod     *corev1.Pod
		want    string
		wantErr string
	}{
		{
			name: "nvml annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{tier.AnnotationTier: "nvml"},
				},
			},
			want: tier.TierNVML,
		},
		{
			name: "ib annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{tier.AnnotationTier: "IB"},
				},
			},
			want: tier.TierIB,
		},
		{
			name: "full annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{tier.AnnotationTier: "full"},
				},
			},
			want: tier.TierFull,
		},
		{
			name: "gpu via resource limit",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "workload",
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceName(tier.GPUResource): resource.MustParse("1"),
							},
						},
					}},
				},
			},
			want: tier.TierGPU,
		},
		{
			name: "gpu via resource request",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "workload",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceName(tier.GPUResource): resource.MustParse("1"),
							},
						},
					}},
				},
			},
			want: tier.TierGPU,
		},
		{
			name: "gpu via operator component label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/component": "nvidia-device-plugin",
					},
				},
			},
			want: tier.TierGPU,
		},
		{
			name: "reject tier=gpu annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{tier.AnnotationTier: "gpu"},
				},
			},
			wantErr: `tier "gpu" cannot be set via annotation`,
		},
		{
			name: "reject unknown tier",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{tier.AnnotationTier: "bogus"},
				},
			},
			wantErr: `unknown tier "bogus"`,
		},
		{
			name: "no-op for plain pod",
			pod:  &corev1.Pod{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.Resolve(tt.pod)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCDIDeviceForTier(t *testing.T) {
	tests := []struct {
		tier string
		want string
	}{
		{tier: tier.TierNVML, want: tier.TierNVML},
		{tier: tier.TierIB, want: tier.TierIB},
		{tier: tier.TierFull, want: tier.TierFull},
		{tier: tier.TierGPU, want: tier.TierFull},
		{tier: "unknown", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			require.Equal(t, tt.want, tier.CDIDeviceForTier(tt.tier))
		})
	}
}
