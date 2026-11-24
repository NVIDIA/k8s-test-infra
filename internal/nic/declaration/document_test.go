// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestDecodeDocument(t *testing.T) {
	t.Parallel()

	doc, err := DecodeDocument([]byte("version: \"1.0\"\nsystem: {driver_version: x}\nsoftware: {other: true}\n"))
	require.NoError(t, err, "the NVML engine's keys, and software keys other than rdma, are not the NIC schema's")
	require.False(t, doc.Declares())

	data, err := os.ReadFile(filepath.Join(chartNICs, "cx9-vr.yaml"))
	require.NoError(t, err)
	profile, err := yaml.YAMLToJSON(data)
	require.NoError(t, err)
	doc, err = DecodeDocument([]byte(`software: {rdma: {umad: true}}
nics:
  slots:
    - {slot: nic0, profile: cx9-vr, devices: {network: {rootComplex: pci0001:00, address: "0001:01:00", numaNode: 0}}}
nic_overrides: [{slots: [nic0], firmwareVersion: "1"}]
nic_profiles:
  - ` + string(profile) + "\n"))
	require.NoError(t, err)
	require.True(t, doc.Declares())
	require.True(t, doc.RDMA.UMAD)
	require.Len(t, doc.Slots, 1)
	require.Equal(t, "1", doc.Overrides[0].FirmwareVersion)
	require.Contains(t, doc.Profiles, "cx9-vr")

	for _, tc := range []struct{ doc, want string }{
		{"software: {rdma: {umadMod: \"666\"}}\n", `software.rdma: error unmarshaling JSON: while decoding JSON: json: unknown field "umadMod"`},
		{"nics: {slot: []}\n", `nics: error unmarshaling JSON: while decoding JSON: json: unknown field "slot"`},
		{"nic_profiles:\n  - " + string(profile) + "\n  - " + string(profile) + "\n", "nic_profiles: cx9-vr is listed twice"},
		{"nic_profiles:\n  - {kind: SNICProfile}\n", `nic_profiles: apiVersion "" and kind "SNICProfile"`},
	} {
		_, err := DecodeDocument([]byte(tc.doc))
		require.ErrorContains(t, err, tc.want)
	}
}

// TestDocumentRepeatedNICKeys checks that a key repeated in the NIC part of a
// config document is an error, where converting it to JSON would keep the
// last one silently, while a key repeated in the NVML engine's part is left
// to the engine.
func TestDocumentRepeatedNICKeys(t *testing.T) {
	t.Parallel()
	const slot = "  - slot: nic0\n    profile: cx9-vr\n    devices: {network: {rootComplex: pci0001:00, address: \"0001:00:01\", numaNode: 0}}\n"
	for doc, want := range map[string]string{
		"nics:\n  slots:\n" + slot + "nics:\n  slots: []\n":             `line 6: key "nics" is repeated`,
		"nics:\n  slots:\n" + slot + "    profile: cx8-c8280z\n":        `nics.slots[0]: line 6: key "profile" is repeated`,
		"software:\n  rdma: {umad: true}\n  rdma: {umad: false}\n":      `software: line 3: key "rdma" is repeated`,
		"system: {driver_version: x}\nsystem: {driver_version: y}\n":    "",
		"nics:\n  slots:\n" + slot + "system: {a: 1}\nsystem: {a: 2}\n": "",
	} {
		_, err := DecodeDocument([]byte(doc))
		if want == "" {
			require.NotContains(t, errString(err), "repeated", doc)
			continue
		}
		require.ErrorContains(t, err, want, doc)
	}
}

// TestDocumentRawRules checks the NIC parts of a config document against the
// CRD rules Go's types can't keep: a required key left out, rather than
// read as zero, and an item repeated in a set.
func TestDocumentRawRules(t *testing.T) {
	t.Parallel()
	for doc, want := range map[string]string{
		"nics:\n  slots:\n  - slot: nic0\n    profile: cx9-vr\n    devices: {network: {rootComplex: pci0001:00, address: \"0001:00:01\"}}\n": `nics.slots[0].devices.network: missing required field "numaNode"`,
		"nic_overrides:\n- slots: [nic0, nic0]\n  configuration: eth\n":                                                                      "nic_overrides[0].slots[1]: repeats nic_overrides[0].slots[0]",
		"nics:\n  rootPorts:\n  - {address: \"0001:00:00.0\"}\n":                                                                             `nics.rootPorts[0]: missing required field "rootComplex"`,
	} {
		_, err := DecodeDocument([]byte(doc))
		require.ErrorContains(t, err, want, doc)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
