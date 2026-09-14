# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: MIT OR GPL-2.0-only

KVER ?= $(shell uname -r)
KDIR ?= /lib/modules/$(KVER)/build

GO_CMD ?= go
GOARCH ?= $(shell $(GO_CMD) env GOARCH)

# Kbuild writes every artifact into the directory M names -- objects, .ko files,
# .mod.c, modules.order, Module.symvers and a hidden .<name>.cmd per artifact --
# and has no output-directory flag that is portable across kernel versions. So M
# points at dist/ and the sources are linked in, which keeps the build's entire
# footprint inside one ignored directory.
DIST := $(CURDIR)/dist

TEST_EXEC ?= sudo

.PHONY: all clean test test-binary check-kernel stage

all: check-kernel stage
	$(MAKE) -C $(KDIR) M=$(DIST) modules

# Symlinks rather than copies, so an edit to a source never has a stale twin in
# dist/. The links are rebuilt every time and cleared first, so a renamed or
# deleted source cannot linger as a dangling entry Kbuild would still compile.
stage:
	@mkdir -p $(DIST)/src
	@find $(DIST) -type l -delete
	@ln -sf $(CURDIR)/Kbuild $(DIST)/Kbuild
	@ln -sf $(CURDIR)/src/*.c $(CURDIR)/src/*.h $(DIST)/src/

# The one precondition worth failing fast on. Every other build error is a
# compiler message; this one is an environment that cannot build any
# out-of-tree module at all, and the default message for it is obscure.
check-kernel:
	@test -d $(KDIR) || { \
	  echo "ERROR: no kernel build tree at $(KDIR)."; \
	  echo "       Install the headers for $(KVER) (linux-headers-$(KVER) or kernel-devel),"; \
	  echo "       or set KDIR to a tree for the kernel you are targeting."; \
	  echo "       See README.md: OrbStack and Docker Desktop ship no build tree at all."; \
	  exit 1; }

# insmod needs CAP_SYS_MODULE. -exec runs only the compiled test binary as root,
# leaving the Go toolchain and module cache owned by the calling user. Set
# TEST_EXEC= to disable when already running as root (a container, most of CI).
test: all
	$(GO_CMD) test -tags integration $(if $(TEST_EXEC),-exec $(TEST_EXEC),) -v .

# A standalone test binary for a host that has a matching kernel but no Go
# toolchain -- the macOS path, where the toolchain is on the workstation and the
# kernel is in the VM. Build it on the workstation, run it over `colima ssh`.
# The binary resolves dist/ relative to its working directory, so run it from
# this directory.
test-binary:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) \
	  $(GO_CMD) test -tags integration -c -o $(DIST)/kmod.test .

clean:
	@test -d $(KDIR) && $(MAKE) -C $(KDIR) M=$(DIST) clean || true
	rm -rf $(DIST)
