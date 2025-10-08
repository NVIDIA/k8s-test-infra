// Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
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

package mocktopo

import "fmt"

// NewFallback creates a synthetic Topology with n GPUs of the specified
// model. This is used when a machine type is not supported but
// ALLOW_UNSUPPORTED=true is set.
func NewFallback(n int, model string) *Topology {
	gpus := make([]GPUInfo, 0, n)
	for i := 0; i < n; i++ {
		pci := fmt.Sprintf("0000:%02x:00.0", 0x81+i)
		uuid := fmt.Sprintf(
			"GPU-%08d-%04d-%04d-%04d-%012d",
			i+1, i+1, i+1, i+1, i+1,
		)
		gpus = append(gpus, GPUInfo{
			PCI:   pci,
			UUID:  uuid,
			Model: model,
		})
	}
	return &Topology{GPUs: gpus}
}
