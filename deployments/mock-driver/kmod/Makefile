# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# kbuild wrapper for the stub nvidia module. Built at runtime by the
# mock-driver entrypoint against the node's kernel:
#   make -C /lib/modules/$(uname -r)/build M=$(pwd) modules
obj-m := nvidia.o
nvidia-objs := nvidia_stub.o
