// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

import (
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	dgxa100 "github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
)

// nvmlWrapper wraps the dgxa100.Server and adds MIG-related methods
// that are required by nvcdi but not implemented in the base mock.
type nvmlWrapper struct {
	*dgxa100.Server
}

// newNVMLWrapper creates an NVML interface wrapper that adds missing
// methods required by nvidia-container-toolkit's nvcdi library.
func newNVMLWrapper() nvml.Interface {
	srv := dgxa100.New()

	// Add GetMaxMigDeviceCount implementation to all devices
	// (return 0 to indicate no MIG support)
	for i := range srv.Devices {
		if dev, ok := srv.Devices[i].(*dgxa100.Device); ok {
			dev.GetMaxMigDeviceCountFunc = func() (int, nvml.Return) {
				return 0, nvml.SUCCESS
			}
			dev.GetMigModeFunc = func() (int, int, nvml.Return) {
				return 0, 0, nvml.ERROR_NOT_SUPPORTED
			}
			dev.GetGpuInstancesFunc = func(gpuInstanceProfileInfo *nvml.GpuInstanceProfileInfo) ([]nvml.GpuInstance, nvml.Return) {
				return nil, nvml.ERROR_NOT_SUPPORTED
			}
		}
	}

	return &nvmlWrapper{Server: srv}
}
