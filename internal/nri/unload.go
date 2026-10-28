// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"errors"
	"syscall"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/NVIDIA/k8s-test-infra/internal/kmod"
	"github.com/NVIDIA/k8s-test-infra/internal/seccompnotify"
)

// moduleNameLen is the kernel's MODULE_NAME_LEN.
const moduleNameLen = 56

// moduleNameReader reads delete_module's name argument from the caller.
type moduleNameReader interface {
	String(addr uint64, limit int) (string, error)
}

// UnloadModule answers delete_module by unloading from the simulated tree at root.
func UnloadModule(root string) seccompnotify.Handler {
	return func(req seccompnotify.Request) (int64, syscall.Errno) {
		return unload(root, req.Container, req.Args[0], req)
	}
}

func unload(root, container string, nameAddr uint64, caller moduleNameReader) (int64, syscall.Errno) {
	// The kernel copies at most MODULE_NAME_LEN-1 bytes of the name.
	name, err := caller.String(nameAddr, moduleNameLen-1)
	if err != nil {
		// As the kernel answers an unreadable name.
		zap.L().Warn("delete_module: name unreadable", zap.String("container", container), zap.Error(err))
		return -1, unix.EFAULT
	}

	err = kmod.Unload(root, name)
	var refused *kmod.RefusedError
	switch {
	case err == nil:
		zap.L().Info("delete_module: unloaded", zap.String("container", container), zap.String("module", name))
		return 0, 0
	case errors.As(err, &refused):
		zap.L().Info("delete_module: refused", zap.String("container", container), zap.String("module", name), zap.Error(err))
		return -1, refused.Errno
	case errors.Is(err, kmod.ErrIncomplete):
		// The module is gone, so the caller is told so, as by the kernel.
		zap.L().Error("delete_module: unloaded, but the simulated tree is inconsistent",
			zap.String("container", container), zap.String("module", name), zap.Error(err))
		return 0, 0
	default:
		zap.L().Error("delete_module: simulated tree unusable", zap.String("container", container), zap.String("module", name), zap.Error(err))
		return -1, unix.EIO
	}
}
