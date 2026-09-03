// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"fmt"

	"go.uber.org/zap"
)

func newLogger() *zap.Logger {
	return zap.L().With(zap.String("component", "ib"))
}

// lidHex renders a LID the way IB tooling prints it, so a value logged here can
// be grepped against ibstat or iblinkinfo output directly.
func lidHex(lid uint16) string { return fmt.Sprintf("0x%04x", lid) }
