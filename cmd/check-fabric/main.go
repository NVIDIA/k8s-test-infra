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

// check-fabric prints the NVLink fabric attributes of every visible GPU
// by calling nvmlDeviceGetGpuFabricInfo / nvmlDeviceGetGpuFabricInfoV
// through go-nvml. It is intended as the demo-time consumer for the
// ComputeDomain simulation work (NVIDIA/k8s-test-infra#304): with the
// mock NVML library on LD_LIBRARY_PATH (or via the standard
// /usr/local/lib install), the binary reports the cluster UUID, clique
// ID, and registration state that the topology overlay assigned to
// this Kubernetes node.
//
// Exit codes:
//
//	0  - every visible GPU reports fabric info
//	2  - at least one GPU returned ERROR_NOT_SUPPORTED (no fabric)
//	1  - any other NVML failure
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

func main() {
	if ret := nvml.Init(); ret != nvml.SUCCESS {
		fmt.Fprintf(os.Stderr, "nvmlInit: %v\n", ret)
		os.Exit(1)
	}
	defer func() { _ = nvml.Shutdown() }()

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		fmt.Fprintf(os.Stderr, "DeviceGetCount: %v\n", ret)
		os.Exit(1)
	}

	notSupported := 0
	fmt.Printf("Discovered %d GPU(s)\n", count)
	for i := 0; i < count; i++ {
		dev, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			fmt.Fprintf(os.Stderr, "handle[%d]: %v\n", i, ret)
			os.Exit(1)
		}
		uuid, _ := dev.GetUUID()

		info, ret := dev.GetGpuFabricInfo()
		switch ret {
		case nvml.SUCCESS:
			fmt.Printf("GPU %d (%s)\n", i, uuid)
			fmt.Printf("  clusterUuid : %s\n", formatUUID(info.ClusterUuid[:]))
			fmt.Printf("  cliqueId    : %d\n", info.CliqueId)
			fmt.Printf("  state       : %s (%d)\n", stateName(uint8(info.State)), info.State)
		case nvml.ERROR_NOT_SUPPORTED:
			fmt.Printf("GPU %d (%s): fabric NOT SUPPORTED\n", i, uuid)
			notSupported++
		default:
			fmt.Fprintf(os.Stderr, "GetGpuFabricInfo[%d]: %v\n", i, ret)
			os.Exit(1)
		}
	}

	if notSupported > 0 {
		os.Exit(2)
	}
}

func formatUUID(b []byte) string {
	if len(b) != 16 {
		return hex.EncodeToString(b)
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func stateName(s uint8) string {
	switch s {
	case 0:
		return "not_supported"
	case 1:
		return "not_started"
	case 2:
		return "in_progress"
	case 3:
		return "completed"
	default:
		return "unknown"
	}
}
