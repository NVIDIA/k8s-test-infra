// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"encoding/json"
	"fmt"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"sigs.k8s.io/yaml"
)

// kubeletSettings models only the settings the NUMA simulator reads or writes.
// The original document stays in kubeletDocument, including unknown nested keys.
type kubeletSettings struct {
	TopologyManagerPolicy        string              `json:"topologyManagerPolicy,omitempty"`
	TopologyManagerScope         string              `json:"topologyManagerScope,omitempty"`
	CPUManagerPolicy             string              `json:"cpuManagerPolicy,omitempty"`
	MemoryManagerPolicy          string              `json:"memoryManagerPolicy,omitempty"`
	ReservedSystemCPUs           string              `json:"reservedSystemCPUs,omitempty"`
	SystemReserved               kubeletReservation  `json:"systemReserved,omitzero"`
	KubeReserved                 kubeletReservation  `json:"kubeReserved,omitzero"`
	EvictionHard                 map[string]string   `json:"evictionHard,omitempty"`
	MergeDefaultEvictionSettings bool                `json:"mergeDefaultEvictionSettings,omitempty"`
	ReservedMemory               []memoryReservation `json:"reservedMemory,omitempty"`
}

type kubeletReservation struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

type memoryReservation struct {
	NUMANode int               `json:"numaNode"`
	Limits   map[string]string `json:"limits"`
}

// kubeletDocument contains the unstructured input. A patch between the typed
// snapshots changes only owned fields, preserving other root and nested fields.
type kubeletDocument struct {
	Settings kubeletSettings
	original []byte
	initial  []byte
}

func decodeKubeletConfig(data []byte) (*kubeletDocument, error) {
	original, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, err
	}
	doc := &kubeletDocument{original: original}
	if err := json.Unmarshal(original, &doc.Settings); err != nil {
		return nil, err
	}
	doc.initial, err = json.Marshal(doc.Settings)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

func (d *kubeletDocument) Bytes() ([]byte, error) {
	updated, err := json.Marshal(d.Settings)
	if err != nil {
		return nil, err
	}
	patch, err := jsonpatch.CreateMergePatch(d.initial, updated)
	if err != nil {
		return nil, fmt.Errorf("create kubelet settings patch: %w", err)
	}
	merged, err := jsonpatch.MergePatch(d.original, patch)
	if err != nil {
		return nil, fmt.Errorf("merge kubelet settings: %w", err)
	}
	return yaml.JSONToYAML(merged)
}
