# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

CC      ?= gcc
CFLAGS  ?= -O2 -Wall -Wextra -fPIC -fvisibility=default
LDFLAGS ?= -shared -ldl

LIB_NAME    := libpcisysfs.so
LIB_SONAME  := $(LIB_NAME).1
LIB_VERSION ?= 1.0.0
LIB_REAL    := $(LIB_NAME).$(LIB_VERSION)

HELPERS := open-fortified fopen-read

.PHONY: all clean test
all: $(LIB_NAME) $(HELPERS)

$(LIB_REAL): src/shim.c
	$(CC) $(CFLAGS) $(LDFLAGS) -Wl,-soname,$(LIB_SONAME) -o $@ $<

$(LIB_NAME): $(LIB_REAL)
	@ln -sf $(LIB_REAL) $(LIB_SONAME)
	@ln -sf $(LIB_SONAME) $(LIB_NAME)

# _FORTIFY_SOURCE=2 + -O2 make gcc emit __open_2 for runtime-variable flags.
open-fortified: testbin/open-fortified.c
	$(CC) -O2 -D_FORTIFY_SOURCE=2 -Wall -o $@ $<

fopen-read: testbin/fopen-read.c
	$(CC) -O2 -Wall -o $@ $<

test: all
	@go test -tags integration -v .

clean:
	@rm -f $(LIB_NAME)* $(HELPERS)
