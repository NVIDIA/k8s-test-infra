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

#include "patch.h"

#include <stdint.h>
#include <stdio.h>
#include <string.h>

int mockRangeExec(unsigned long addr, size_t len) {
	if (len == 0) {
		return 0;
	}
	unsigned long end = addr + len;
	if (end < addr) {
		return 0; /* address overflow */
	}
	FILE* f = fopen("/proc/self/maps", "r");
	if (f == NULL) {
		return 0;
	}
	char line[8192];
	int ok = 0;
	while (fgets(line, sizeof(line), f) != NULL) {
		unsigned long lo = 0, hi = 0;
		char perms[8] = {0};
		if (sscanf(line, "%lx-%lx %7s", &lo, &hi, perms) != 3) {
			continue;
		}
		if (addr >= lo && end <= hi) {
			ok = (perms[2] == 'x'); /* perms formatted as "r-xp" */
			break;
		}
	}
	fclose(f);
	return ok;
}

void mockBuildTramp(unsigned char* code, void* target) {
	unsigned long a = (unsigned long)target;
#if defined(__x86_64__)
	code[0] = 0x48;
	code[1] = 0xB8;
	memcpy(code + 2, &a, 8);
	code[10] = 0xFF;
	code[11] = 0xE0;
#elif defined(__aarch64__)
	/* insn[0] = ldr x16,#8 ; insn[1] = br x16 ; followed by the 8-byte target. */
	uint32_t insn[2] = {0x58000050u, 0xD61F0200u};
	memcpy(code, insn, sizeof(insn));
	memcpy(code + 8, &a, 8);
#else
	(void)a;
	(void)code;
#endif
}
