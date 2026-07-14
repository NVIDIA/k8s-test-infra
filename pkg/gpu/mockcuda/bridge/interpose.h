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

#ifndef MOCKCUDA_INTERPOSE_H
#define MOCKCUDA_INTERPOSE_H

/*
 * The in-memory interposition machinery for the statically linked cuda-sample
 * lives in interpose.c. Only the entry points the Go bridge (cuda.go) calls are
 * declared here; everything else (the SIGSEGV debug handler, load-base finder,
 * runtime-API patcher, export-table probe, and the optional weak driver stubs)
 * is file-local to interpose.c.
 */

/*
 * mockCudaVectorAdd performs, on the host, the element-wise addition the
 * sample's GPU kernel would do. rawArgs is the CUDA kernel argument array
 * {float** a, float** b, float** c, int* n}. Returns 1 if it ran, 0 if the
 * arguments were unusable.
 */
int mockCudaVectorAdd(void* rawArgs);

/*
 * mockPatchAttestation neutralises the amd64 static-runtime attestation MAC
 * check in the loaded sample. It is idempotent and a no-op on other
 * architectures or against any other binary.
 */
void mockPatchAttestation(void);

/*
 * mockCudaDlsym resolves symbol in the process-global scope (RTLD_DEFAULT). The
 * mock's cuGetProcAddress uses it to hand back optional driver entry points
 * (including the weak stubs defined in interpose.c).
 */
void* mockCudaDlsym(const char* symbol);

/*
 * mockCudaGetExportTableProbe returns the instrumented private export table the
 * static CUDA runtime requests through cuGetExportTable.
 */
void* mockCudaGetExportTableProbe(const void* exportTableId);

#endif /* MOCKCUDA_INTERPOSE_H */
