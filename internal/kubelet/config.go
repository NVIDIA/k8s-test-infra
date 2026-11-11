// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kubelet

import (
	"encoding/json"
	"fmt"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"sigs.k8s.io/yaml"
)

// Settings models the supported kubelet configuration fields. Document keeps
// all other fields, including unknown nested keys, unchanged.
type Settings struct {
	TopologyManagerPolicy        string              `json:"topologyManagerPolicy,omitempty"`
	TopologyManagerScope         string              `json:"topologyManagerScope,omitempty"`
	CPUManagerPolicy             string              `json:"cpuManagerPolicy,omitempty"`
	MemoryManagerPolicy          string              `json:"memoryManagerPolicy,omitempty"`
	ReservedSystemCPUs           string              `json:"reservedSystemCPUs,omitempty"`
	SystemReserved               Reservation         `json:"systemReserved,omitzero"`
	KubeReserved                 Reservation         `json:"kubeReserved,omitzero"`
	EvictionHard                 map[string]string   `json:"evictionHard,omitempty"`
	MergeDefaultEvictionSettings bool                `json:"mergeDefaultEvictionSettings,omitempty"`
	ReservedMemory               []MemoryReservation `json:"reservedMemory,omitempty"`
}

// Reservation describes CPU and memory reserved for system or Kubernetes processes.
type Reservation struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// MemoryReservation describes resources reserved on one NUMA node.
type MemoryReservation struct {
	NUMANode int               `json:"numaNode"`
	Limits   map[string]string `json:"limits"`
}

// Document contains the unstructured input. A patch between the typed
// snapshots changes only owned fields, preserving other root and nested fields.
type Document struct {
	Settings Settings
	original []byte
	initial  []byte
}

// DecodeConfig reads kubelet YAML while retaining fields outside Settings.
func DecodeConfig(data []byte) (*Document, error) {
	original, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, err
	}
	// Keep the full document to preserve fields outside Settings when
	// patching owned fields. The recovery backup is stored separately.
	doc := &Document{original: original}
	if err := json.Unmarshal(original, &doc.Settings); err != nil {
		return nil, err
	}
	doc.initial, err = json.Marshal(doc.Settings)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// Bytes applies changed settings to the original document and returns YAML.
func (d *Document) Bytes() ([]byte, error) {
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
