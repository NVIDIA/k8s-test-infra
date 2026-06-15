# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

CC      ?= gcc
CFLAGS  ?= -O2 -Wall -Wextra -fPIC -fvisibility=default
LDFLAGS ?= -shared -ldl

LIB_NAME    := libibmocksys.so
LIB_SONAME  := $(LIB_NAME).1
LIB_VERSION ?= 1.0.0
LIB_REAL    := $(LIB_NAME).$(LIB_VERSION)

UMAD_LIB_NAME   := libibmockumad.so
UMAD_LIB_SONAME := $(UMAD_LIB_NAME).1
UMAD_LIB_VERSION ?= 1.0.0
UMAD_LIB_REAL   := $(UMAD_LIB_NAME).$(UMAD_LIB_VERSION)

VERBS_LIB_NAME   := libibmockverbs.so
VERBS_LIB_SONAME := $(VERBS_LIB_NAME).1
VERBS_LIB_VERSION ?= 1.0.0
VERBS_LIB_REAL   := $(VERBS_LIB_NAME).$(VERBS_LIB_VERSION)

# libibmockrdma.so is the in-process libibverbs provider that carries the RDMA
# verbs data path (issue #374). Unlike the other shims it is a real (minimal)
# rdma-core provider coupled to the private verbs_context ABI, so it must be
# compiled against the image's pinned libibverbs-dev and links pthread.
RDMA_LIB_NAME   := libibmockrdma.so
RDMA_LIB_SONAME := $(RDMA_LIB_NAME).1
RDMA_LIB_VERSION ?= 1.0.0
RDMA_LIB_REAL   := $(RDMA_LIB_NAME).$(RDMA_LIB_VERSION)
RDMA_LDFLAGS    := -shared -ldl -lpthread

.PHONY: all clean

all: $(LIB_NAME) $(UMAD_LIB_NAME) $(VERBS_LIB_NAME) $(RDMA_LIB_NAME)

$(LIB_REAL): c/shim.c
	$(CC) $(CFLAGS) $(LDFLAGS) -Wl,-soname,$(LIB_SONAME) -o $@ $<

$(LIB_NAME): $(LIB_REAL)
	ln -sf $(LIB_REAL) $(LIB_SONAME)
	ln -sf $(LIB_SONAME) $(LIB_NAME)

$(UMAD_LIB_REAL): c/umad_shim.c
	$(CC) $(CFLAGS) $(LDFLAGS) -Wl,-soname,$(UMAD_LIB_SONAME) -o $@ $<

$(UMAD_LIB_NAME): $(UMAD_LIB_REAL)
	ln -sf $(UMAD_LIB_REAL) $(UMAD_LIB_SONAME)
	ln -sf $(UMAD_LIB_SONAME) $(UMAD_LIB_NAME)

$(VERBS_LIB_REAL): c/verbs_shim.c
	$(CC) $(CFLAGS) $(LDFLAGS) -Wl,-soname,$(VERBS_LIB_SONAME) -o $@ $<

$(VERBS_LIB_NAME): $(VERBS_LIB_REAL)
	ln -sf $(VERBS_LIB_REAL) $(VERBS_LIB_SONAME)
	ln -sf $(VERBS_LIB_SONAME) $(VERBS_LIB_NAME)

$(RDMA_LIB_REAL): c/rdma_shim.c
	$(CC) $(CFLAGS) $(RDMA_LDFLAGS) -Wl,-soname,$(RDMA_LIB_SONAME) -o $@ $<

$(RDMA_LIB_NAME): $(RDMA_LIB_REAL)
	ln -sf $(RDMA_LIB_REAL) $(RDMA_LIB_SONAME)
	ln -sf $(RDMA_LIB_SONAME) $(RDMA_LIB_NAME)

clean:
	rm -f $(LIB_NAME)* $(UMAD_LIB_NAME)* $(VERBS_LIB_NAME)* $(RDMA_LIB_NAME)*
