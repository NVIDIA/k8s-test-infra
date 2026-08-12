// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToSchemeRegistersMokkaKinds(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))

	for _, kind := range []string{"SGPUProfile", "SGPUProfileList", "SGPUInventory", "SGPUInventoryList", "SGPURack", "SGPURackList"} {
		gvk := SchemeGroupVersion.WithKind(kind)
		object, err := scheme.New(gvk)
		require.NoError(t, err)
		objectKinds, _, err := scheme.ObjectKinds(object)
		require.NoError(t, err)
		require.Contains(t, objectKinds, gvk)
	}
}

func TestDeepCopyDoesNotAliasNestedFields(t *testing.T) {
	t.Parallel()

	profile := &SGPUProfile{
		Spec: SGPUProfileSpec{
			Node: SGPUProfileNode{
				GPUs: SGPUHardware{
					Capabilities: GPUCapabilities{
						Attributes: map[string]GPUCapabilityAttribute{
							"nvidia.com/example": {Strings: []string{"one"}},
						},
					},
				},
			},
		},
	}
	copy := profile.DeepCopy()
	attribute := copy.Spec.Node.GPUs.Capabilities.Attributes["nvidia.com/example"]
	attribute.Strings[0] = "two"
	copy.Spec.Node.GPUs.Capabilities.Attributes["nvidia.com/example"] = attribute

	require.Equal(t, "one", profile.Spec.Node.GPUs.Capabilities.Attributes["nvidia.com/example"].Strings[0])

	inventory := &SGPUInventory{
		Status: SGPUInventoryStatus{
			Conditions: []metav1.Condition{{Type: "Accepted", Message: "original"}},
		},
	}
	inventoryCopy := inventory.DeepCopy()
	inventoryCopy.Status.Conditions[0].Message = "changed"
	require.Equal(t, "original", inventory.Status.Conditions[0].Message)
}
