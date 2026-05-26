# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

CC      ?= gcc
CFLAGS  ?= -O2 -Wall -Wextra -Wno-format-truncation -fPIC -fvisibility=default
LDFLAGS ?= -shared -Wl,-soname,libibmocksys.so.1 -ldl -lpthread

LIB_NAME    := libibmocksys.so
LIB_SONAME  := $(LIB_NAME).1
LIB_VERSION ?= 1.2.0
LIB_REAL    := $(LIB_NAME).$(LIB_VERSION)

.PHONY: all clean

all: $(LIB_NAME)

$(LIB_REAL): shim.c umad_mock.c mock_ib_root.c umad_mock.h mock_ib_root.h
	$(CC) $(CFLAGS) $(LDFLAGS) -o $@ shim.c umad_mock.c mock_ib_root.c

$(LIB_NAME): $(LIB_REAL)
	ln -sf $(LIB_REAL) $(LIB_SONAME)
	ln -sf $(LIB_SONAME) $(LIB_NAME)

clean:
	rm -f $(LIB_NAME)*
