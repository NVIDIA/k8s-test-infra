// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package host

import (
	"context"
	"errors"
	"net"
)

// DialContext requires Linux node namespaces.
func (h *Host) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("node network namespace operations require Linux")
}
