# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

CC      ?= gcc
CFLAGS  ?= -O2 -Wall -Wextra -fPIC -fvisibility=default
LDFLAGS ?= -shared -Wl,-soname,libibmocksys.so.1 -ldl

LIB_NAME    := libibmocksys.so
LIB_SONAME  := $(LIB_NAME).1
LIB_VERSION ?= 1.0.0
LIB_REAL    := $(LIB_NAME).$(LIB_VERSION)

.PHONY: all clean

all: $(LIB_NAME)

$(LIB_REAL): shim.c
	$(CC) $(CFLAGS) $(LDFLAGS) -o $@ $<

$(LIB_NAME): $(LIB_REAL)
	ln -sf $(LIB_REAL) $(LIB_SONAME)
	ln -sf $(LIB_SONAME) $(LIB_NAME)

clean:
	rm -f $(LIB_NAME)*
