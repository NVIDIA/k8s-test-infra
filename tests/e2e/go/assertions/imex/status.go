// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package imex parses the machine-readable status emitted by nvidia-imex-ctl.
package imex

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Status is one domain-wide status response from nvidia-imex-ctl -N -j.
type Status struct {
	State string          `json:"status"`
	Nodes map[string]Node `json:"nodes"`
}

// Node is one IMEX peer in a domain status response.
type Node struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// ParseStatus decodes an IMEX domain status and rejects responses that do not
// carry the domain-wide state used by lifecycle assertions.
func ParseStatus(raw string) (Status, error) {
	var status Status
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return Status{}, fmt.Errorf("decode IMEX status: %w", err)
	}
	if status.State == "" {
		return Status{}, errors.New("decode IMEX status: missing status")
	}
	return status, nil
}

// ReadyNodes returns the number of peers that report READY.
func (s Status) ReadyNodes() int {
	return countNodes(s.Nodes, func(node Node) bool { return node.Status == "READY" })
}

// NoGPUNodes returns the number of peers running IMEX's no-GPU protocol mode.
func (s Status) NoGPUNodes() int {
	return countNodes(s.Nodes, func(node Node) bool { return node.Version == "NO_GPU" })
}

func countNodes(nodes map[string]Node, match func(Node) bool) int {
	count := 0
	for _, node := range nodes {
		if match(node) {
			count++
		}
	}
	return count
}
