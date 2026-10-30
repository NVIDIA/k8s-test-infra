// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Confidential Compute attestation.
//
// A real GPU signs an SPDM measurement report with a key whose certificate
// chain roots in NVIDIA's CA, so nothing the mock produces could pass a real
// verifier. What a consumer under test needs instead is the shape of the
// exchange: a report of plausible size that is bound to the nonce it sent,
// stable for a given nonce, and different from GPU to GPU. The bytes are
// therefore a SHA-256 stream keyed by the GPU's UUID — opaque, deterministic
// across calls and processes, and never parseable as X.509 or SPDM.

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const (
	// ccAttestationReportSize is the length the report claims, well inside
	// the 8 KiB buffer NVML hands back.
	ccAttestationReportSize = 0x1000
	// ccAttestationNonceOffset is where the caller's nonce sits inside the
	// report: right after the 4-byte header of the SPDM 1.1 GET_MEASUREMENTS
	// request the report opens with, as a verifier parses it.
	ccAttestationNonceOffset = 4
	// ccSpdmRequestSize is that request's length: the header, the nonce and
	// the slot byte. The signed MEASUREMENTS response follows it.
	ccSpdmRequestSize = ccAttestationNonceOffset + 32 + 1
	// The certificate chain sizes, each inside its buffer (4 KiB and 5 KiB).
	ccCertChainSize            = 0x800
	ccAttestationCertChainSize = 0xC00
)

// ccSpdmGetMeasurementsHeader is the SPDM 1.1 GET_MEASUREMENTS request header:
// SPDMVersion 0x11, RequestResponseCode 0xE0, Param1 0x01 (signature
// requested) and Param2 0xFF (all measurements).
var ccSpdmGetMeasurementsHeader = [ccAttestationNonceOffset]byte{0x11, 0xE0, 0x01, 0xFF}

// GetMockConfComputeGpuAttestationReport backs
// nvmlDeviceGetConfComputeGpuAttestationReport. The report is only produced
// while CC or Protected PCIe is on; no CEC report is attached.
//
// Named GetMock* to avoid shadowing the embedded mock Device's method.
func (d *ConfigurableDevice) GetMockConfComputeGpuAttestationReport(
	nonce [32]byte,
) (nvml.ConfComputeGpuAttestationReport, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.ConfComputeGpuAttestationReport{}, ret
	}
	if !d.coco.protected() {
		debugLog("[NVML] nvmlDeviceGetConfComputeGpuAttestationReport -> NOT_SUPPORTED (CC and Protected PCIe off)\n")
		return nvml.ConfComputeGpuAttestationReport{}, nvml.ERROR_NOT_SUPPORTED
	}
	report := nvml.ConfComputeGpuAttestationReport{
		IsCecAttestationReportPresent: nvml.CC_CEC_ATTESTATION_REPORT_NOT_PRESENT,
		AttestationReportSize:         ccAttestationReportSize,
		Nonce:                         nonce,
	}
	// The report opens with the SPDM 1.1 GET_MEASUREMENTS request it
	// answers: version 1.1, the request code, a signature requested over all
	// measurements, then the caller's nonce and slot 0. The placeholder bytes
	// stand in for the signed response after it.
	copy(report.AttestationReport[:], ccSpdmGetMeasurementsHeader[:])
	copy(report.AttestationReport[ccAttestationNonceOffset:], nonce[:])
	ccStream(report.AttestationReport[ccSpdmRequestSize:ccAttestationReportSize], "attestation-report", d.UUID, nonce[:])
	debugLog("[NVML] nvmlDeviceGetConfComputeGpuAttestationReport -> size=%d\n", report.AttestationReportSize)
	return report, nvml.SUCCESS
}

// GetMockConfComputeGpuCertificate backs
// nvmlDeviceGetConfComputeGpuCertificate: the GPU's device certificate chain
// and the chain of the key that signs its attestation reports.
//
// Named GetMock* to avoid shadowing the embedded mock Device's method.
func (d *ConfigurableDevice) GetMockConfComputeGpuCertificate() (nvml.ConfComputeGpuCertificate, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.ConfComputeGpuCertificate{}, ret
	}
	if !d.coco.protected() {
		debugLog("[NVML] nvmlDeviceGetConfComputeGpuCertificate -> NOT_SUPPORTED (CC and Protected PCIe off)\n")
		return nvml.ConfComputeGpuCertificate{}, nvml.ERROR_NOT_SUPPORTED
	}
	cert := nvml.ConfComputeGpuCertificate{
		CertChainSize:            ccCertChainSize,
		AttestationCertChainSize: ccAttestationCertChainSize,
	}
	ccStream(cert.CertChain[:ccCertChainSize], "cert-chain", d.UUID, nil)
	ccStream(cert.AttestationCertChain[:ccAttestationCertChainSize], "attestation-cert-chain", d.UUID, nil)
	debugLog("[NVML] nvmlDeviceGetConfComputeGpuCertificate -> certChain=%d attestationCertChain=%d\n",
		cert.CertChainSize, cert.AttestationCertChainSize)
	return cert, nvml.SUCCESS
}

// ccStream fills buf with SHA-256(label || 0 || uuid || 0 || extra || counter)
// blocks, counter a big-endian uint32 from zero. The label keeps the report and
// the two chains apart for the same GPU. hash.Hash documents that Write never
// returns an error, so its results are not checked.
func ccStream(buf []byte, label, uuid string, extra []byte) {
	var counter [4]byte
	for i := uint32(0); len(buf) > 0; i++ {
		h := sha256.New()
		h.Write([]byte(label))
		h.Write([]byte{0})
		h.Write([]byte(uuid))
		h.Write([]byte{0})
		h.Write(extra)
		binary.BigEndian.PutUint32(counter[:], i)
		h.Write(counter[:])
		buf = buf[copy(buf, h.Sum(nil)):]
	}
}
