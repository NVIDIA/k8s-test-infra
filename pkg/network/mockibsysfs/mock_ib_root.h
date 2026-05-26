/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 */

#ifndef MOCK_IB_ROOT_H
#define MOCK_IB_ROOT_H

/* Must match nvml-mock Helm MOCK_IB_ROOT and setup.sh render --output parent. */
#define MOCK_IB_ROOT_DEFAULT "/var/lib/nvml-mock/ib"

const char *mock_ib_root(void);

#endif /* MOCK_IB_ROOT_H */
