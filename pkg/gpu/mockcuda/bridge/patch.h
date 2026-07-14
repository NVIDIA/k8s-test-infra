/*
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

#ifndef MOCKCUDA_PATCH_H
#define MOCKCUDA_PATCH_H

#include <stddef.h>

/*
 * These helpers back the in-memory interposition of the statically linked
 * cuda-sample. They live in a standalone translation unit (patch.c) rather than
 * a cgo preamble so they have a single external definition: the preamble is
 * compiled into more than one object file (it also holds //export directives),
 * which would duplicate any non-static definition placed there. A single
 * definition here lets both the preamble and the unit-test hook call them.
 */

/* Absolute-jump trampoline length written over each redirected entry point. */
#if defined(__x86_64__)
#define MOCK_TRAMP_LEN 12
#elif defined(__aarch64__)
#define MOCK_TRAMP_LEN 16
#endif

/*
 * mockRangeExec reports whether [addr, addr+len) lies entirely within a single
 * executable mapping in /proc/self/maps. Patch/probe sites are checked with it
 * before the mock reads or writes there, so preloading this library into an
 * unrelated process (or a differently sized build) can never fault on an
 * unmapped offset or scribble over non-code memory.
 */
int mockRangeExec(unsigned long addr, size_t len);

/*
 * mockBuildTramp fills code[MOCK_TRAMP_LEN] with an absolute jump to target:
 *   x86-64  (12 bytes): movabs $target,%rax ; jmp *%rax
 *   aarch64 (16 bytes): ldr x16,#8 ; br x16 ; .quad target
 * The trampoline never returns to the original body and does not touch the
 * return-address register/stack, so the redirected host function returns
 * straight to the sample's caller.
 */
void mockBuildTramp(unsigned char* code, void* target);

#endif /* MOCKCUDA_PATCH_H */
