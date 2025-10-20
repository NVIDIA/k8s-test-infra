// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

package mockdriver

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// WriteAll writes all file specs to disk, creating directories,
// symlinks, regular files, and character devices as specified.
func WriteAll(files []FileSpec) error {
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(f.Path), err)
		}

		if f.IsCharDev {
			if err := mkChar(f.Path, f.DevMajor, f.DevMinor,
				os.FileMode(f.Mode)); err != nil {
				return err
			}
			continue
		}

		if f.SymlinkTo != "" {
			// Remove existing file/symlink first
			_ = os.Remove(f.Path)
			if err := os.Symlink(f.SymlinkTo, f.Path); err != nil {
				return fmt.Errorf("symlink %s->%s: %w",
					f.Path, f.SymlinkTo, err)
			}
			continue
		}

		// Create empty file or file with content
		var content []byte
		if !f.EmptyFile {
			content = []byte(f.Content)
		}
		// EmptyFile: content remains nil/empty

		if err := os.WriteFile(f.Path, content,
			os.FileMode(f.Mode)); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}
	return nil
}

func mkChar(path string, major, minor uint32, perm os.FileMode) error {
	// Remove existing device node if present
	_ = os.Remove(path)

	dev := int(unix.Mkdev(major, minor))
	mode := uint32(unix.S_IFCHR | uint32(perm))
	if err := unix.Mknod(path, mode, dev); err != nil {
		return fmt.Errorf("mknod %s: %w", path, err)
	}
	return nil
}
