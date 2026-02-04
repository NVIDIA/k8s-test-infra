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

package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// busIdToString converts a [32]uint8 array to a Go string
func busIdToString(busId [32]uint8) string {
	bytes := make([]byte, 0, 32)
	for _, b := range busId {
		if b == 0 {
			break
		}
		bytes = append(bytes, b)
	}
	return strings.TrimRight(string(bytes), "\x00")
}

func main() {
	log.Println("Starting Mock NVML Test")
	log.Println("=================================")

	// Initialize NVML
	log.Println("Initializing NVML...")
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		log.Fatalf("Failed to initialize NVML: %v", nvml.ErrorString(ret))
	}
	defer func() {
		log.Println("Shutting down NVML...")
		ret := nvml.Shutdown()
		if ret != nvml.SUCCESS {
			log.Printf("Failed to shutdown NVML: %v", nvml.ErrorString(ret))
		}
	}()
	log.Println("✓ NVML initialized successfully")

	// Get driver version
	version, ret := nvml.SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		log.Fatalf("Failed to get driver version: %v", nvml.ErrorString(ret))
	}
	log.Printf("✓ Driver version: %s", version)

	// Get device count
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		log.Fatalf("Failed to get device count: %v", nvml.ErrorString(ret))
	}
	log.Printf("✓ Found %d GPU device(s)", count)

	if count == 0 {
		log.Println("No devices found, exiting")
		os.Exit(0)
	}

	// Enumerate devices (like a device plugin would)
	log.Println("\nEnumerating devices:")
	for i := 0; i < count; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			log.Fatalf("Failed to get device %d: %v", i, nvml.ErrorString(ret))
		}

		// Get device name
		name, ret := device.GetName()
		if ret != nvml.SUCCESS {
			log.Printf("  Warning: Failed to get name for device %d: %v", i, nvml.ErrorString(ret))
			name = "Unknown"
		}

		// Get device UUID
		uuid, ret := device.GetUUID()
		if ret != nvml.SUCCESS {
			log.Printf("  Warning: Failed to get UUID for device %d: %v", i, nvml.ErrorString(ret))
			uuid = "Unknown"
		}

		// Get PCI info
		pciInfo, ret := device.GetPciInfo()
		busId := ""
		if ret == nvml.SUCCESS {
			busId = busIdToString(pciInfo.BusId)
		} else {
			log.Printf("  Warning: Failed to get PCI info for device %d: %v", i, nvml.ErrorString(ret))
		}

		// Get memory info
		memory, ret := device.GetMemoryInfo()
		if ret != nvml.SUCCESS {
			log.Printf("  Warning: Failed to get memory info for device %d: %v", i, nvml.ErrorString(ret))
		}

		// Get compute capability
		major, minor, ret := device.GetCudaComputeCapability()
		if ret != nvml.SUCCESS {
			log.Printf("  Warning: Failed to get compute capability for device %d: %v", i, nvml.ErrorString(ret))
		}

		log.Printf("\nDevice %d:", i)
		log.Printf("  Name: %s", name)
		log.Printf("  UUID: %s", uuid)
		if busId != "" {
			log.Printf("  PCI Bus ID: %s", busId)
		}
		if memory.Total > 0 {
			log.Printf("  Memory: %d MB (Total), %d MB (Free), %d MB (Used)",
				memory.Total/(1024*1024),
				memory.Free/(1024*1024),
				memory.Used/(1024*1024))
		}
		if major > 0 {
			log.Printf("  Compute Capability: %d.%d", major, minor)
		}
	}

	// Test device lookup by UUID
	log.Println("\nTesting device lookup by UUID...")

	// First, collect all UUIDs
	allUUIDs := make([]string, count)
	for i := 0; i < count; i++ {
		dev, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			log.Fatalf("Failed to get device %d: %v", i, nvml.ErrorString(ret))
		}
		u, ret := dev.GetUUID()
		if ret != nvml.SUCCESS {
			log.Fatalf("Failed to get UUID for device %d: %v", i, nvml.ErrorString(ret))
		}
		allUUIDs[i] = u
		log.Printf("  Device %d UUID: %q", i, u)
	}

	// Now try to look up the first device by its UUID
	uuid := allUUIDs[0]
	log.Printf("Looking up device with UUID: %q", uuid)

	deviceByUUID, ret := nvml.DeviceGetHandleByUUID(uuid)
	if ret != nvml.SUCCESS {
		log.Fatalf("Failed to get device by UUID %q: %v", uuid, nvml.ErrorString(ret))
	}

	uuidCheck, _ := deviceByUUID.GetUUID()
	if uuidCheck == uuid {
		log.Printf("✓ Successfully looked up device by UUID: %s", uuid)
	} else {
		log.Fatalf("UUID mismatch: expected %s, got %s", uuid, uuidCheck)
	}

	// Test device lookup by PCI Bus ID
	log.Println("\nTesting device lookup by PCI Bus ID...")
	pciInfo, ret := deviceByUUID.GetPciInfo()
	if ret != nvml.SUCCESS {
		log.Fatalf("Failed to get PCI info: %v", nvml.ErrorString(ret))
	}

	busId := busIdToString(pciInfo.BusId)
	deviceByPCI, ret := nvml.DeviceGetHandleByPciBusId(busId)
	if ret != nvml.SUCCESS {
		log.Fatalf("Failed to get device by PCI Bus ID: %v", nvml.ErrorString(ret))
	}

	pciInfoCheck, _ := deviceByPCI.GetPciInfo()
	busIdCheck := busIdToString(pciInfoCheck.BusId)
	if busIdCheck == busId {
		log.Printf("✓ Successfully looked up device by PCI Bus ID: %s", busId)
	} else {
		log.Fatalf("PCI Bus ID mismatch: expected %s, got %s", busId, busIdCheck)
	}

	log.Println("\n=================================")
	log.Println("✓ All device plugin tests passed!")
	fmt.Println("\nSUCCESS: Mock NVML library is working correctly!")
}
