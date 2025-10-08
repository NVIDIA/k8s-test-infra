// Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mockfs

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// MkChar creates a character device node at the specified path with the
// given major and minor numbers.
func MkChar(path string, major, minor uint32, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	dev := int(unix.Mkdev(major, minor))
	mode := uint32(unix.S_IFCHR | uint32(perm))
	if err := unix.Mknod(path, mode, dev); err != nil {
		return fmt.Errorf("mknod %s: %w", path, err)
	}
	return nil
}
