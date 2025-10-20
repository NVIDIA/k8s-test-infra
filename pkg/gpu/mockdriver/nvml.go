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
	"io"
	"os"
	"path/filepath"
)

// NVMLLibrarySource specifies the source path for the mock NVML library
type NVMLLibrarySource struct {
	// Path to the directory containing the compiled NVML library files
	// Expected to contain: libnvidia-ml.so.550.54.15, libnvidia-ml.so.1, libnvidia-ml.so
	SourcePath string
}

// DeployNVMLLibrary copies the compiled mock NVML library to the target location
func DeployNVMLLibrary(source NVMLLibrarySource, targetRoot string) error {
	if source.SourcePath == "" {
		return fmt.Errorf("NVML library source path is empty")
	}

	// Ensure target directory exists
	targetDir := filepath.Join(targetRoot, "lib64")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// List of NVML library files to copy
	driverVer := "550.54.15"
	files := []string{
		"libnvidia-ml.so." + driverVer,
		"libnvidia-ml.so.1",
		"libnvidia-ml.so",
	}

	// Copy each file
	for _, filename := range files {
		srcPath := filepath.Join(source.SourcePath, filename)
		dstPath := filepath.Join(targetDir, filename)

		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to copy %s: %w", filename, err)
		}
	}

	return nil
}

// copyFile copies a file from src to dst, preserving permissions and handling symlinks
func copyFile(src, dst string) error {
	// Check if source is a symlink
	if info, err := os.Lstat(src); err == nil && info.Mode()&os.ModeSymlink != 0 {
		// Read the symlink target
		target, err := os.Readlink(src)
		if err != nil {
			return fmt.Errorf("failed to read symlink: %w", err)
		}

		// Remove existing file/symlink at destination
		_ = os.Remove(dst)

		// Create new symlink
		if err := os.Symlink(target, dst); err != nil {
			return fmt.Errorf("failed to create symlink: %w", err)
		}
		return nil
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() {
		if cerr := srcFile.Close(); cerr != nil {
			err = fmt.Errorf("failed to close source file: %w", cerr)
		}
	}()

	// Get source file info for permissions
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source file: %w", err)
	}

	// Create destination file
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close destination file: %w", cerr)
		}
	}()

	// Copy content
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	return nil
}

// DefaultFilesWithNVML returns the mock driver tree with real NVML library
// This filters out the empty NVML library files from DefaultFiles
func DefaultFilesWithNVML(root string) []FileSpec {
	allFiles := DefaultFiles(root)
	filtered := make([]FileSpec, 0, len(allFiles))

	// Filter out NVML library files (they will be deployed separately)
	for _, f := range allFiles {
		if filepath.Base(f.Path) == "libnvidia-ml.so.550.54.15" ||
			filepath.Base(f.Path) == "libnvidia-ml.so.1" ||
			filepath.Base(f.Path) == "libnvidia-ml.so" {
			continue
		}
		filtered = append(filtered, f)
	}

	return filtered
}
