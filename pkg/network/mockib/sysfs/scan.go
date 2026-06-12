// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package sysfs scans a mock InfiniBand sysfs tree under MOCK_IB_ROOT.
package sysfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

// Scan walks root/sys/class/infiniband/mlx5_*/ports/1 for port_guid and lid.
func Scan(root string) ([]protocol.PortAdvert, error) {
	pattern := filepath.Join(root, "sys/class/infiniband", "mlx5_*", "ports", "1")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob ports: %w", err)
	}
	var out []protocol.PortAdvert
	for _, portDir := range matches {
		caDir := filepath.Dir(filepath.Dir(portDir))
		caName := filepath.Base(caDir)
		guidBytes, err := os.ReadFile(filepath.Join(portDir, "port_guid"))
		if err != nil {
			return nil, fmt.Errorf("read %s port_guid: %w", caName, err)
		}
		lidBytes, err := os.ReadFile(filepath.Join(portDir, "lid"))
		if err != nil {
			return nil, fmt.Errorf("read %s lid: %w", caName, err)
		}
		lid, err := parseLID(string(lidBytes))
		if err != nil {
			return nil, fmt.Errorf("parse %s lid: %w", caName, err)
		}
		// node_guid is optional in the tree. Normalize only a non-empty value:
		// NormalizePortGUID("") would yield the non-empty
		// "0000:0000:0000:0000", defeating fabric.coalesceGUID's nodeGUID==""
		// fallback and advertising a zero NodeGUID for every local port.
		nodeGUID := ""
		nodeGUIDBytes, _ := os.ReadFile(filepath.Join(caDir, "node_guid"))
		if raw := strings.TrimSpace(string(nodeGUIDBytes)); raw != "" {
			nodeGUID = registry.NormalizePortGUID(raw)
		}
		defaultGID := ""
		if rawGID, err := os.ReadFile(filepath.Join(portDir, "gids/0")); err == nil {
			defaultGID = gid.Normalize(strings.TrimSpace(string(rawGID)))
		}
		out = append(out, protocol.PortAdvert{
			PortGUID:   registry.NormalizePortGUID(strings.TrimSpace(string(guidBytes))),
			NodeGUID:   nodeGUID,
			DefaultGID: defaultGID,
			CAName:     caName,
			Port:       1,
			LID:        lid,
		})
	}
	return out, nil
}

func parseLID(s string) (uint16, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty lid")
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err := strconv.ParseUint(s, 0, 16)
		if err != nil {
			return 0, err
		}
		return uint16(v), nil
	}
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}
