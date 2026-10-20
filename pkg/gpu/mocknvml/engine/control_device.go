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

package engine

import "os"

// defaultControlDevicePath is the device real NVML opens on init.
const defaultControlDevicePath = "/dev/nvidiactl"

// holdControlDevice opens the control device as real NVML does on init, and
// keeps it open until the last shutdown. The descriptor pins the kernel module
// serving the device, which is how the GPU Operator tells a driver in use from
// an idle one. Its driver container then cannot unload the driver from under a
// running client, and k8s-driver-manager, which evicts the operator's clients
// before unloading the driver itself, also unmounts the old driver root before
// it reschedules them. Without a driver serving the device, as on a node that
// runs no simulated module, the open fails and nothing is held.
func (e *Engine) holdControlDevice() {
	f, err := os.OpenFile(e.controlDevicePath, os.O_RDWR, 0)
	if err != nil {
		debugLog("[ENGINE] Not holding %s: %v\n", e.controlDevicePath, err)
		return
	}
	e.controlDevice = f
}

// releaseControlDevice closes what holdControlDevice opened.
func (e *Engine) releaseControlDevice() {
	if e.controlDevice == nil {
		return
	}
	_ = e.controlDevice.Close()
	e.controlDevice = nil
}
