// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedPCIeGen is the <pcie_gen> block a real board reports through
// `nvidia-smi -q -x`, less max_host_link_gen, which no profile key answers.
type capturedPCIeGen struct {
	max, deviceMax, current, deviceCurrent int
}

// readCapturedPCIeGen parses qx-<sku>.xml and returns the board's PCIe link
// generations, requiring every GPU in the capture to agree on them.
func readCapturedPCIeGen(t *testing.T, sku string) capturedPCIeGen {
	t.Helper()

	type pcieGen struct {
		Max           string `xml:"max_link_gen"`
		DeviceMax     string `xml:"max_device_link_gen"`
		Current       string `xml:"current_link_gen"`
		DeviceCurrent string `xml:"device_current_link_gen"`
	}
	var doc struct {
		GPUs []struct {
			Gen pcieGen `xml:"pci>pci_gpu_link_info>pcie_gen"`
		} `xml:"gpu"`
	}

	raw, err := os.ReadFile(filepath.Join(hardwareCaptureDir(), "qx-"+sku+".xml"))
	require.NoErrorf(t, err, "no hardware capture for profile %q", sku)
	require.NoError(t, xml.Unmarshal(raw, &doc), "parse hardware capture for %s", sku)
	require.NotEmpty(t, doc.GPUs, "capture for %s declares no GPU", sku)

	first := doc.GPUs[0].Gen
	for i, gpu := range doc.GPUs {
		require.Equalf(t, first, gpu.Gen, "capture %s GPU %d disagrees on pcie_gen", sku, i)
	}

	gen := func(field, value string) int {
		t.Helper()
		v, err := strconv.Atoi(value)
		require.NoErrorf(t, err, "capture %s %s %q", sku, field, value)
		return v
	}
	return capturedPCIeGen{
		max:           gen("max_link_gen", first.Max),
		deviceMax:     gen("max_device_link_gen", first.DeviceMax),
		current:       gen("current_link_gen", first.Current),
		deviceCurrent: gen("device_current_link_gen", first.DeviceCurrent),
	}
}

// TestProfilePCIeLinkGenMatchesHardwareCapture holds every shipped profile's
// PCIe link generations, in both copies, to the board it models.
//
// The link maximum, GPU capability and current link generation are independent
// profile values. Older profiles omit max_device_link_gen and retain the link
// maximum as their device capability. GB200 needs Gen5 / Gen6 / Gen5.
func TestProfilePCIeLinkGenMatchesHardwareCapture(t *testing.T) {
	t.Parallel()

	for _, src := range profileSources() {
		profiles := src.profiles(t)
		for _, sku := range sortedKeys(profiles) {
			path := profiles[sku]
			t.Run(src.label+"/"+sku, func(t *testing.T) {
				t.Parallel()

				want := readCapturedPCIeGen(t, sku)

				cfg, err := LoadYAMLConfig(path)
				require.NoError(t, err, "load profile %s", path)
				pcie := cfg.DeviceDefaults.PCIe
				require.NotNil(t, pcie, "%s declares no pcie block", path)

				require.Equal(t, want.max, pcie.MaxLinkGen,
					"%s pcie.max_link_gen must be the captured Max", path)
				deviceMax := pcie.MaxDeviceLinkGen
				if deviceMax == 0 {
					deviceMax = pcie.MaxLinkGen
				}
				require.Equal(t, want.deviceMax, deviceMax,
					"%s configured device maximum must be the captured Device Max", path)
				require.Equal(t, want.current, pcie.CurrentLinkGen,
					"%s pcie.current_link_gen must be the captured Current", path)
				require.Equal(t, want.deviceCurrent, pcie.CurrentLinkGen,
					"%s pcie.current_link_gen also answers Device Current", path)
			})
		}
	}
}
