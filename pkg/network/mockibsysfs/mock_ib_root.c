/*
 * Copyright 2026 NVIDIA CORPORATION
 * SPDX-License-Identifier: Apache-2.0
 */

#include "mock_ib_root.h"

#include <pthread.h>
#include <stdlib.h>
#include <string.h>

static const char *root_cached;
static pthread_once_t root_once = PTHREAD_ONCE_INIT;

static void init_root(void) {
	const char *root = getenv("MOCK_IB_ROOT");
	if (!root || root[0] == '\0')
		root = MOCK_IB_ROOT_DEFAULT;
	root_cached = root;
}

const char *mock_ib_root(void) {
	pthread_once(&root_once, init_root);
	return root_cached;
}
