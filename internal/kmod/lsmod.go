// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kmod

import _ "embed"

// LsmodRelPath is the staged script path.
const LsmodRelPath = "driver/usr/local/bin/lsmod"

// LsmodContainerPath is the script mount path.
const LsmodContainerPath = "/usr/local/bin/lsmod"

// LsmodScript replaces lsmod in a container that gets the module surface only
// from the CDI mount. runc refuses a bind mount inside /proc, so that container
// reads the node's /proc/modules. The real lsmod lists from that file, so it
// omits nvidia even though /sys/module/nvidia is mounted. This script reads the
// mounted tree instead.
//
//go:embed lsmod.sh
var LsmodScript string
