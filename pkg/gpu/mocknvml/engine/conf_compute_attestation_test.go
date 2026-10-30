// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"bytes"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestAttestationReport_EchoesNonceAndIsDeterministic(t *testing.T) {
	t.Parallel()
	dev, other := ccDevices(t, &ConfidentialComputeConfig{Mode: "on"})

	var nonce [32]byte
	for i := range nonce {
		nonce[i] = 0xA5
	}
	report, ret := dev.GetMockConfComputeGpuAttestationReport(nonce)
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, ccAttestationReportSize, report.AttestationReportSize)
	require.EqualValues(t, nvml.CC_CEC_ATTESTATION_REPORT_NOT_PRESENT, report.IsCecAttestationReportPresent)
	require.Zero(t, report.CecAttestationReportSize)
	require.Equal(t, nonce[:], report.AttestationReport[ccAttestationNonceOffset:ccAttestationNonceOffset+32],
		"the caller's nonce is echoed so a verifier can bind the report to its request")
	require.Equal(t, nonce, report.Nonce)
	require.True(t, bytes.Equal(make([]byte, len(report.AttestationReport)-ccAttestationReportSize),
		report.AttestationReport[ccAttestationReportSize:]), "bytes past the reported size are zero")

	again, _ := dev.GetMockConfComputeGpuAttestationReport(nonce)
	require.Equal(t, report, again, "the same nonce gives the same report")

	nonce[0] = 0x01
	changed, _ := dev.GetMockConfComputeGpuAttestationReport(nonce)
	require.NotEqual(t, report.AttestationReport, changed.AttestationReport, "a new nonce gives a new report")

	nonce[0] = 0xA5
	sibling, _ := other.GetMockConfComputeGpuAttestationReport(nonce)
	require.NotEqual(t, report.AttestationReport, sibling.AttestationReport, "each GPU reports its own")
}

func TestCertificate_StablePerGPU(t *testing.T) {
	t.Parallel()
	dev, other := ccDevices(t, &ConfidentialComputeConfig{Mode: "on"})

	cert, ret := dev.GetMockConfComputeGpuCertificate()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, 0x800, cert.CertChainSize)
	require.EqualValues(t, 0xC00, cert.AttestationCertChainSize)
	require.NotEqual(t, make([]byte, 0x800), cert.CertChain[:0x800], "the chain carries bytes")
	require.Equal(t, make([]byte, len(cert.CertChain)-0x800), cert.CertChain[0x800:])
	require.Equal(t, make([]byte, len(cert.AttestationCertChain)-0xC00), cert.AttestationCertChain[0xC00:])
	require.NotEqual(t, cert.CertChain[:0x800], cert.AttestationCertChain[:0x800], "the two chains differ")

	again, _ := dev.GetMockConfComputeGpuCertificate()
	require.Equal(t, cert, again)

	sibling, _ := other.GetMockConfComputeGpuCertificate()
	require.NotEqual(t, cert.CertChain, sibling.CertChain, "each GPU has its own chain")
}

func TestAttestation_ProtectionModes(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		arch   string
		config ConfidentialComputeConfig
		want   nvml.Return
	}{
		{"CC on", "hopper", ConfidentialComputeConfig{Mode: "on"}, nvml.SUCCESS},
		{"devtools", "hopper", ConfidentialComputeConfig{Mode: "devtools"}, nvml.SUCCESS},
		{"Protected PCIe", "hopper", ConfidentialComputeConfig{Mode: "off", MultiGPUMode: "protected_pcie"}, nvml.SUCCESS},
		{"NVLE only", "blackwell", ConfidentialComputeConfig{Mode: "off", MultiGPUMode: "nvle"}, nvml.ERROR_NOT_SUPPORTED},
		{"CC with NVLE", "blackwell", ConfidentialComputeConfig{Mode: "on", MultiGPUMode: "nvle"}, nvml.SUCCESS},
		{"off", "hopper", ConfidentialComputeConfig{Mode: "off"}, nvml.ERROR_NOT_SUPPORTED},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, validateConfCompute(ccProfile(tt.arch, tt.config)))
			e := ccEngine(t, tt.arch, &tt.config)
			require.Equal(t, nvml.SUCCESS, e.Init())
			t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
			handle, ret := e.DeviceGetHandleByIndex(0)
			require.Equal(t, nvml.SUCCESS, ret)
			dev := e.LookupConfigurableDevice(handle)
			report, ret := dev.GetMockConfComputeGpuAttestationReport([32]byte{1})
			require.Equal(t, tt.want, ret)
			cert, ret := dev.GetMockConfComputeGpuCertificate()
			require.Equal(t, tt.want, ret)
			if tt.want == nvml.SUCCESS {
				require.NotZero(t, report.AttestationReportSize)
				require.NotZero(t, cert.CertChainSize)
			}
		})
	}
}

func TestAttestation_GatedOnCC(t *testing.T) {
	t.Parallel()

	off, _ := ccDevices(t, nil)
	_, ret := off.GetMockConfComputeGpuAttestationReport([32]byte{})
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = off.GetMockConfComputeGpuCertificate()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)

	lost := newTestDeviceWithConfig(t, &DeviceConfig{
		Failure: &FailureInjectionConfig{Mode: FailureModeLost},
	})
	_, ret = lost.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "trip the device")
	_, ret = lost.GetMockConfComputeGpuAttestationReport([32]byte{})
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
	_, ret = lost.GetMockConfComputeGpuCertificate()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
}

// The per-GPU CC calls address a physical GPU. A MIG device handle is refused
// the way the other physical-only calls refuse one, rather than answering
// with — or, for the setter, writing — its parent's state. MIG devices are not
// handed the node's CC state, so they read as CC off; this pins that should
// they ever be.
func TestConfComputeDeviceCalls_RefuseMigDeviceHandle(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{
		NumDevices:    1,
		DriverVersion: "580.65.06",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			DeviceDefaults: DeviceConfig{
				Architecture: "hopper",
				Memory:       &MemoryConfig{TotalBytes: a100_40GiB},
				MIG: &MIGConfig{
					ModeCurrent:       "enabled",
					ModePending:       "enabled",
					MaxGPUInstances:   7,
					SupportedProfiles: a100SupportedProfiles(),
					GPUInstances:      []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
				},
			},
			ConfidentialCompute: &ConfidentialComputeConfig{Mode: "on"},
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	parent, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	mig := e.LookupConfigurableDevice(mustMigDeviceHandle(t, e, parent, 0))

	_, ret = mig.GetMockConfComputeGpuAttestationReport([32]byte{})
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "attestation report")
	_, ret = mig.GetMockConfComputeGpuCertificate()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "certificate")
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, mig.SetMockConfComputeUnprotectedMemSize(1024), "unprotected size")
}
