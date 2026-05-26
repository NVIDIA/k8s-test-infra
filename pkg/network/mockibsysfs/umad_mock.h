/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 *
 * Userspace umad(4) emulation for ibping (OpenIB vendor ping MADs only).
 * Paired with the sysfs redirector in shim.c.
 */

#ifndef UMAD_MOCK_H
#define UMAD_MOCK_H

#include <poll.h>
#include <stddef.h>
#include <sys/types.h>

/* After path rewrite, open() calls this instead of the placeholder file. */
int umad_mock_open(const char *resolved_path);

int umad_mock_is_mock_fd(int fd);

int umad_mock_ioctl(int fd, unsigned long request, void *arg);

ssize_t umad_mock_read(int fd, void *buf, size_t count);

ssize_t umad_mock_write(int fd, const void *buf, size_t count);

int umad_mock_close(int fd);

int umad_mock_poll(struct pollfd *fds, nfds_t nfds, int timeout);

#endif /* UMAD_MOCK_H */
