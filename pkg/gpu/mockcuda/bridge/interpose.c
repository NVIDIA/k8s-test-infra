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

/*
 * In-memory interposition of the statically linked cuda-sample. This file was
 * split out of cuda.go's cgo preamble for readability and to give the helpers a
 * single external definition (a preamble that also holds //export directives is
 * compiled into more than one translation unit). Only the four entry points in
 * interpose.h are visible to Go; everything else is file-local here.
 */

#define _GNU_SOURCE /* RTLD_DEFAULT, SA_SIGINFO */

#include <dlfcn.h>
#include <execinfo.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#include "cuda_types.h"
#include "interpose.h"
#include "patch.h"

static unsigned long mockExeBase(void);

// mockSegvHandler prints the faulting address and a backtrace (as offsets from
// the sample's load base) so crashes inside the static CUDA runtime can be
// mapped back to disassembly offsets. Debug aid only; installed when
// MOCK_CUDA_DEBUG is set.
static void mockSegvHandler(int sig, siginfo_t* info, void* uctx) {
	(void)sig;
	(void)uctx;
	unsigned long base = mockExeBase();
	void* bt[32];
	int n = backtrace(bt, 32);
	fprintf(stderr, "[CUDA] *** SIGSEGV at addr=%p base=0x%lx ***\n", info->si_addr, base);
	for (int i = 0; i < n; i++) {
		unsigned long a = (unsigned long)bt[i];
		unsigned long off = (base && a > base && a < base + 0x200000) ? (a - base) : 0;
		fprintf(stderr, "[CUDA]   #%d %p  off=0x%lx\n", i, bt[i], off);
	}
	_exit(139);
}

static void mockInstallSegvHandler(void) {
	if (getenv("MOCK_CUDA_DEBUG") == NULL) {
		return;
	}
	struct sigaction sa;
	memset(&sa, 0, sizeof(sa));
	sa.sa_sigaction = mockSegvHandler;
	sa.sa_flags = SA_SIGINFO;
	sigaction(SIGSEGV, &sa, NULL);
}

int mockCudaVectorAdd(void* rawArgs) {
	void** args = (void**)rawArgs;
	if (args == NULL || args[0] == NULL || args[1] == NULL || args[2] == NULL || args[3] == NULL) {
		return 0;
	}

	float* a = *(float**)args[0];
	float* b = *(float**)args[1];
	float* c = *(float**)args[2];
	int n = *(int*)args[3];
	if (a == NULL || b == NULL || c == NULL || n <= 0) {
		return 0;
	}

	for (int i = 0; i < n; i++) {
		c[i] = a[i] + b[i];
	}
	return 1;
}

#define MOCK_CUDA_GENERIC_STUB(name) __attribute__((weak)) int name() { fprintf(stderr, "[CUDA] optional stub called: %s\n", #name); return 0; }
MOCK_CUDA_GENERIC_STUB(cuMemPoolExportToShareableHandle)
MOCK_CUDA_GENERIC_STUB(cuMemPoolImportFromShareableHandle)
MOCK_CUDA_GENERIC_STUB(cuMemPoolExportPointer)
MOCK_CUDA_GENERIC_STUB(cuMemPoolImportPointer)
MOCK_CUDA_GENERIC_STUB(cuMemcpy2DUnaligned)
MOCK_CUDA_GENERIC_STUB(cuMemcpy2DAsync)
MOCK_CUDA_GENERIC_STUB(cuMemcpy3D)
MOCK_CUDA_GENERIC_STUB(cuMemcpy3DAsync)
MOCK_CUDA_GENERIC_STUB(cuMemcpy3DPeer)
MOCK_CUDA_GENERIC_STUB(cuMemcpy3DPeerAsync)
MOCK_CUDA_GENERIC_STUB(cuDeviceGetNvSciSyncAttributes)
MOCK_CUDA_GENERIC_STUB(cuStreamCopyAttributes)
MOCK_CUDA_GENERIC_STUB(cuStreamGetAttribute)
MOCK_CUDA_GENERIC_STUB(cuStreamSetAttribute)
MOCK_CUDA_GENERIC_STUB(cuDeviceGraphMemTrim)
MOCK_CUDA_GENERIC_STUB(cuDeviceGetGraphMemAttribute)
MOCK_CUDA_GENERIC_STUB(cuDeviceSetGraphMemAttribute)
MOCK_CUDA_GENERIC_STUB(cuStreamBeginCapture)
MOCK_CUDA_GENERIC_STUB(cuStreamBeginCaptureToGraph)
MOCK_CUDA_GENERIC_STUB(cuStreamEndCapture)
MOCK_CUDA_GENERIC_STUB(cuStreamIsCapturing)
MOCK_CUDA_GENERIC_STUB(cuStreamGetCaptureInfo)
MOCK_CUDA_GENERIC_STUB(cuStreamUpdateCaptureDependencies)
MOCK_CUDA_GENERIC_STUB(cuDeviceRegisterAsyncNotification)
MOCK_CUDA_GENERIC_STUB(cuDeviceUnregisterAsyncNotification)

MOCK_CUDA_GENERIC_STUB(cuArray3DCreate)
MOCK_CUDA_GENERIC_STUB(cuArray3DGetDescriptor)
MOCK_CUDA_GENERIC_STUB(cuArrayCreate)
MOCK_CUDA_GENERIC_STUB(cuArrayDestroy)
MOCK_CUDA_GENERIC_STUB(cuArrayGetDescriptor)
MOCK_CUDA_GENERIC_STUB(cuArrayGetMemoryRequirements)
MOCK_CUDA_GENERIC_STUB(cuArrayGetPlane)
MOCK_CUDA_GENERIC_STUB(cuArrayGetSparseProperties)
MOCK_CUDA_GENERIC_STUB(cuCtxResetPersistingL2Cache)
MOCK_CUDA_GENERIC_STUB(cuDestroyExternalMemory)
MOCK_CUDA_GENERIC_STUB(cuDestroyExternalSemaphore)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamConsumerAcquireFrame)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamConsumerConnect)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamConsumerConnectWithFlags)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamConsumerDisconnect)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamConsumerReleaseFrame)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamProducerConnect)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamProducerDisconnect)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamProducerPresentFrame)
MOCK_CUDA_GENERIC_STUB(cuEGLStreamProducerReturnFrame)
MOCK_CUDA_GENERIC_STUB(cuEventCreate)
MOCK_CUDA_GENERIC_STUB(cuEventCreateFromEGLSync)
MOCK_CUDA_GENERIC_STUB(cuEventDestroy)
MOCK_CUDA_GENERIC_STUB(cuEventElapsedTime)
MOCK_CUDA_GENERIC_STUB(cuEventQuery)
MOCK_CUDA_GENERIC_STUB(cuEventRecord)
MOCK_CUDA_GENERIC_STUB(cuEventRecordWithFlags)
MOCK_CUDA_GENERIC_STUB(cuEventSynchronize)
MOCK_CUDA_GENERIC_STUB(cuExternalMemoryGetMappedBuffer)
MOCK_CUDA_GENERIC_STUB(cuExternalMemoryGetMappedMipmappedArray)
MOCK_CUDA_GENERIC_STUB(cuFuncGetAttribute)
MOCK_CUDA_GENERIC_STUB(cuFuncGetName)
MOCK_CUDA_GENERIC_STUB(cuFuncGetParamInfo)
MOCK_CUDA_GENERIC_STUB(cuFuncSetAttribute)
MOCK_CUDA_GENERIC_STUB(cuFuncSetCacheConfig)
MOCK_CUDA_GENERIC_STUB(cuFuncSetSharedMemConfig)
MOCK_CUDA_GENERIC_STUB(cuGLCtxCreate)
MOCK_CUDA_GENERIC_STUB(cuGLGetDevices)
MOCK_CUDA_GENERIC_STUB(cuGLInit)
MOCK_CUDA_GENERIC_STUB(cuGLMapBufferObject)
MOCK_CUDA_GENERIC_STUB(cuGLMapBufferObjectAsync)
MOCK_CUDA_GENERIC_STUB(cuGLRegisterBufferObject)
MOCK_CUDA_GENERIC_STUB(cuGLSetBufferObjectMapFlags)
MOCK_CUDA_GENERIC_STUB(cuGLUnmapBufferObject)
MOCK_CUDA_GENERIC_STUB(cuGLUnmapBufferObjectAsync)
MOCK_CUDA_GENERIC_STUB(cuGLUnregisterBufferObject)
MOCK_CUDA_GENERIC_STUB(cuGetErrorName)
MOCK_CUDA_GENERIC_STUB(cuGetErrorString)
MOCK_CUDA_GENERIC_STUB(cuGraphAddChildGraphNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddDependencies)
MOCK_CUDA_GENERIC_STUB(cuGraphAddEmptyNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddEventRecordNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddEventWaitNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddExternalSemaphoresSignalNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddExternalSemaphoresWaitNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddHostNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddKernelNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddMemAllocNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddMemFreeNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddMemcpyNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddMemsetNode)
MOCK_CUDA_GENERIC_STUB(cuGraphAddNode)
MOCK_CUDA_GENERIC_STUB(cuGraphChildGraphNodeGetGraph)
MOCK_CUDA_GENERIC_STUB(cuGraphClone)
MOCK_CUDA_GENERIC_STUB(cuGraphConditionalHandleCreate)
MOCK_CUDA_GENERIC_STUB(cuGraphCreate)
MOCK_CUDA_GENERIC_STUB(cuGraphDebugDotPrint)
MOCK_CUDA_GENERIC_STUB(cuGraphDestroy)
MOCK_CUDA_GENERIC_STUB(cuGraphDestroyNode)
MOCK_CUDA_GENERIC_STUB(cuGraphEventRecordNodeGetEvent)
MOCK_CUDA_GENERIC_STUB(cuGraphEventRecordNodeSetEvent)
MOCK_CUDA_GENERIC_STUB(cuGraphEventWaitNodeGetEvent)
MOCK_CUDA_GENERIC_STUB(cuGraphEventWaitNodeSetEvent)
MOCK_CUDA_GENERIC_STUB(cuGraphExecChildGraphNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExecDestroy)
MOCK_CUDA_GENERIC_STUB(cuGraphExecEventRecordNodeSetEvent)
MOCK_CUDA_GENERIC_STUB(cuGraphExecEventWaitNodeSetEvent)
MOCK_CUDA_GENERIC_STUB(cuGraphExecExternalSemaphoresSignalNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExecExternalSemaphoresWaitNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExecGetFlags)
MOCK_CUDA_GENERIC_STUB(cuGraphExecHostNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExecKernelNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExecMemcpyNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExecMemsetNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExecNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExecUpdate)
MOCK_CUDA_GENERIC_STUB(cuGraphExternalSemaphoresSignalNodeGetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExternalSemaphoresSignalNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExternalSemaphoresWaitNodeGetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphExternalSemaphoresWaitNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphGetEdges)
MOCK_CUDA_GENERIC_STUB(cuGraphGetNodes)
MOCK_CUDA_GENERIC_STUB(cuGraphGetRootNodes)
MOCK_CUDA_GENERIC_STUB(cuGraphHostNodeGetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphHostNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphInstantiate)
MOCK_CUDA_GENERIC_STUB(cuGraphInstantiateWithFlags)
MOCK_CUDA_GENERIC_STUB(cuGraphInstantiateWithParams)
MOCK_CUDA_GENERIC_STUB(cuGraphKernelNodeCopyAttributes)
MOCK_CUDA_GENERIC_STUB(cuGraphKernelNodeGetAttribute)
MOCK_CUDA_GENERIC_STUB(cuGraphKernelNodeGetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphKernelNodeSetAttribute)
MOCK_CUDA_GENERIC_STUB(cuGraphKernelNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphLaunch)
MOCK_CUDA_GENERIC_STUB(cuGraphMemAllocNodeGetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphMemFreeNodeGetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphMemcpyNodeGetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphMemcpyNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphMemsetNodeGetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphMemsetNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphNodeFindInClone)
MOCK_CUDA_GENERIC_STUB(cuGraphNodeGetDependencies)
MOCK_CUDA_GENERIC_STUB(cuGraphNodeGetDependentNodes)
MOCK_CUDA_GENERIC_STUB(cuGraphNodeGetEnabled)
MOCK_CUDA_GENERIC_STUB(cuGraphNodeGetType)
MOCK_CUDA_GENERIC_STUB(cuGraphNodeSetEnabled)
MOCK_CUDA_GENERIC_STUB(cuGraphNodeSetParams)
MOCK_CUDA_GENERIC_STUB(cuGraphReleaseUserObject)
MOCK_CUDA_GENERIC_STUB(cuGraphRemoveDependencies)
MOCK_CUDA_GENERIC_STUB(cuGraphRetainUserObject)
MOCK_CUDA_GENERIC_STUB(cuGraphUpload)
MOCK_CUDA_GENERIC_STUB(cuGraphicsEGLRegisterImage)
MOCK_CUDA_GENERIC_STUB(cuGraphicsGLRegisterBuffer)
MOCK_CUDA_GENERIC_STUB(cuGraphicsGLRegisterImage)
MOCK_CUDA_GENERIC_STUB(cuGraphicsMapResources)
MOCK_CUDA_GENERIC_STUB(cuGraphicsResourceGetMappedEglFrame)
MOCK_CUDA_GENERIC_STUB(cuGraphicsResourceGetMappedMipmappedArray)
MOCK_CUDA_GENERIC_STUB(cuGraphicsResourceGetMappedPointer)
MOCK_CUDA_GENERIC_STUB(cuGraphicsResourceSetMapFlags)
MOCK_CUDA_GENERIC_STUB(cuGraphicsSubResourceGetMappedArray)
MOCK_CUDA_GENERIC_STUB(cuGraphicsUnmapResources)
MOCK_CUDA_GENERIC_STUB(cuGraphicsUnregisterResource)
MOCK_CUDA_GENERIC_STUB(cuGraphicsVDPAURegisterOutputSurface)
MOCK_CUDA_GENERIC_STUB(cuGraphicsVDPAURegisterVideoSurface)
MOCK_CUDA_GENERIC_STUB(cuImportExternalMemory)
MOCK_CUDA_GENERIC_STUB(cuImportExternalSemaphore)
MOCK_CUDA_GENERIC_STUB(cuIpcCloseMemHandle)
MOCK_CUDA_GENERIC_STUB(cuIpcGetEventHandle)
MOCK_CUDA_GENERIC_STUB(cuIpcGetMemHandle)
MOCK_CUDA_GENERIC_STUB(cuIpcOpenEventHandle)
MOCK_CUDA_GENERIC_STUB(cuIpcOpenMemHandle)
MOCK_CUDA_GENERIC_STUB(cuKernelGetAttribute)
MOCK_CUDA_GENERIC_STUB(cuKernelGetName)
MOCK_CUDA_GENERIC_STUB(cuKernelGetParamInfo)
MOCK_CUDA_GENERIC_STUB(cuKernelSetAttribute)
MOCK_CUDA_GENERIC_STUB(cuKernelSetCacheConfig)
MOCK_CUDA_GENERIC_STUB(cuLaunchCooperativeKernel)
MOCK_CUDA_GENERIC_STUB(cuLaunchCooperativeKernelMultiDevice)
MOCK_CUDA_GENERIC_STUB(cuLaunchHostFunc)
MOCK_CUDA_GENERIC_STUB(cuLaunchKernelEx)
MOCK_CUDA_GENERIC_STUB(cuLibraryGetGlobal)
MOCK_CUDA_GENERIC_STUB(cuLibraryGetManaged)
MOCK_CUDA_GENERIC_STUB(cuLinkAddData)
MOCK_CUDA_GENERIC_STUB(cuLinkAddFile)
MOCK_CUDA_GENERIC_STUB(cuLinkComplete)
MOCK_CUDA_GENERIC_STUB(cuLinkCreate)
MOCK_CUDA_GENERIC_STUB(cuLinkDestroy)
MOCK_CUDA_GENERIC_STUB(cuMipmappedArrayCreate)
MOCK_CUDA_GENERIC_STUB(cuMipmappedArrayDestroy)
MOCK_CUDA_GENERIC_STUB(cuMipmappedArrayGetLevel)
MOCK_CUDA_GENERIC_STUB(cuMipmappedArrayGetMemoryRequirements)
MOCK_CUDA_GENERIC_STUB(cuMipmappedArrayGetSparseProperties)
MOCK_CUDA_GENERIC_STUB(cuModuleGetGlobal)
MOCK_CUDA_GENERIC_STUB(cuModuleGetSurfRef)
MOCK_CUDA_GENERIC_STUB(cuModuleGetTexRef)
MOCK_CUDA_GENERIC_STUB(cuModuleLoad)
MOCK_CUDA_GENERIC_STUB(cuModuleLoadFatBinary)
MOCK_CUDA_GENERIC_STUB(cuModuleUnload)
MOCK_CUDA_GENERIC_STUB(cuOccupancyAvailableDynamicSMemPerBlock)
MOCK_CUDA_GENERIC_STUB(cuOccupancyMaxActiveBlocksPerMultiprocessorWithFlags)
MOCK_CUDA_GENERIC_STUB(cuOccupancyMaxActiveClusters)
MOCK_CUDA_GENERIC_STUB(cuOccupancyMaxPotentialClusterSize)
MOCK_CUDA_GENERIC_STUB(cuPointerGetAttribute)
MOCK_CUDA_GENERIC_STUB(cuPointerGetAttributes)
MOCK_CUDA_GENERIC_STUB(cuProfilerInitialize)
MOCK_CUDA_GENERIC_STUB(cuProfilerStart)
MOCK_CUDA_GENERIC_STUB(cuProfilerStop)
MOCK_CUDA_GENERIC_STUB(cuSignalExternalSemaphoresAsync)
MOCK_CUDA_GENERIC_STUB(cuSurfObjectCreate)
MOCK_CUDA_GENERIC_STUB(cuSurfObjectDestroy)
MOCK_CUDA_GENERIC_STUB(cuSurfObjectGetResourceDesc)
MOCK_CUDA_GENERIC_STUB(cuTexObjectCreate)
MOCK_CUDA_GENERIC_STUB(cuTexObjectDestroy)
MOCK_CUDA_GENERIC_STUB(cuTexObjectGetResourceDesc)
MOCK_CUDA_GENERIC_STUB(cuTexObjectGetResourceViewDesc)
MOCK_CUDA_GENERIC_STUB(cuTexObjectGetTextureDesc)
MOCK_CUDA_GENERIC_STUB(cuThreadExchangeStreamCaptureMode)
MOCK_CUDA_GENERIC_STUB(cuUserObjectCreate)
MOCK_CUDA_GENERIC_STUB(cuUserObjectRelease)
MOCK_CUDA_GENERIC_STUB(cuUserObjectRetain)
MOCK_CUDA_GENERIC_STUB(cuVDPAUCtxCreate)
MOCK_CUDA_GENERIC_STUB(cuVDPAUGetDevice)
MOCK_CUDA_GENERIC_STUB(cuWaitExternalSemaphoresAsync)

void* mockCudaDlsym(const char* symbol) {
	return dlsym(RTLD_DEFAULT, symbol);
}

static void mockCudaFmtUUID(char* buf, const void* exportTableId) {
	const unsigned char* b = (const unsigned char*)exportTableId;
	if (b == NULL) {
		strcpy(buf, "<nil>");
		return;
	}
	sprintf(buf,
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7],
		b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15]);
}

// ---------------------------------------------------------------------------
// Instrumented export table (reverse-engineering probe).
//
// The statically-linked CUDA runtime requires several private "export tables"
// from the driver. Each table is laid out as { size_t size; void (*fn0)(); ... }
// and the runtime validates `size`, then calls the function pointers by fixed
// byte offset (0x8, 0x10, ...). To learn exactly which slots this specific
// runtime calls (and in what order across malloc/memcpy/launch), we hand it a
// table of logging trampolines and record every call. MOCK_CUDA_EXPORT_PROBE
// gates this behaviour.
// ---------------------------------------------------------------------------
#define MOCK_EXPORT_SLOTS 128

// mockExeBase returns the load base of the statically-linked cuda-sample
// binary (the module that calls our export-table trampolines). Export-table
// call sites are identified by their return address minus this base, which is
// stable for a given binary regardless of ASLR.
static unsigned long mockExeBase(void) {
	static unsigned long base = 0;
	static int done = 0;
	if (done) {
		return base;
	}
	done = 1;
	FILE* f = fopen("/proc/self/maps", "r");
	if (f == NULL) {
		return 0;
	}
	char line[8192];
	unsigned long min = ~0UL;
	while (fgets(line, sizeof(line), f) != NULL) {
		char* slash = strchr(line, '/');
		if (slash == NULL) {
			continue;
		}
		if (strstr(slash, "vectorAdd") == NULL && strstr(slash, "sample") == NULL) {
			continue;
		}
		unsigned long s = strtoul(line, NULL, 16);
		if (s < min) {
			min = s;
		}
	}
	fclose(f);
	if (min != ~0UL) {
		base = min;
	}
	return base;
}

// mockRangeExec and mockBuildTramp are defined in patch.c (declared in patch.h)
// so they have a single external definition callable from both here and the
// unit-test hook.

#if defined(__x86_64__)
// Offset of the driver-attestation failure branch (jne 0x2fd7f, error 0x67)
// in cuda-sample:vectoradd-cuda12.5.0 (amd64). The embedded static CUDA 12.5
// runtime computes a keyed 128-bit MAC over the driver-version/device blobs and
// compares it against the value our private export table returns. On a mock
// (GPU-less) driver there is nothing to attest, so we neutralise the check by
// overwriting the 6-byte near-jump (0F 85 xx xx xx xx) with NOPs, forcing the
// comparison to always fall through to the success path.
//
// This is amd64-only: the MAC check lives on the private export-table path, and
// on arm64 the runtime-API interposition below bypasses that path entirely
// (verified: the arm64 sample prints "Test PASSED" without it).
#define MOCK_OFF_ATTEST_JNE 0x2fe71

void mockPatchAttestation(void) {
	static int done = 0;
	if (done) {
		return;
	}
	done = 1;
	mockInstallSegvHandler();
	unsigned long base = mockExeBase();
	if (base == 0) {
		return;
	}
	unsigned char* target = (unsigned char*)(base + MOCK_OFF_ATTEST_JNE);
	if (!mockRangeExec(base + MOCK_OFF_ATTEST_JNE, 6)) {
		if (getenv("MOCK_CUDA_DEBUG")) {
			fprintf(stderr, "[CUDA] attestation patch skipped (offset not executable) base=0x%lx\n", base);
		}
		return;
	}
	long pagesize = sysconf(_SC_PAGESIZE);
	if (pagesize <= 0) {
		pagesize = 4096;
	}
	uintptr_t start = (uintptr_t)target & ~((uintptr_t)pagesize - 1);
	size_t span = (size_t)(((uintptr_t)target + 6) - start);
	if (mprotect((void*)start, span, PROT_READ | PROT_WRITE | PROT_EXEC) != 0) {
		return;
	}
	int patched = 0;
	if (target[0] == 0x0F && target[1] == 0x85) {
		for (int i = 0; i < 6; i++) {
			target[i] = 0x90;
		}
		patched = 1;
	}
	mprotect((void*)start, span, PROT_READ | PROT_EXEC);
	if (getenv("MOCK_CUDA_DEBUG")) {
		fprintf(stderr, "[CUDA] attestation patch @%p base=0x%lx applied=%d\n",
			(void*)target, base, patched);
	}
}
#else
// On non-amd64 targets the attestation MAC check is bypassed by the
// runtime-API interposition, so no in-memory patch is required.
void mockPatchAttestation(void) {}
#endif

// ---------------------------------------------------------------------------
// CUDA Runtime API interposition
//
// The vectoradd-cuda12.5.0 sample links the CUDA runtime statically, so its
// cudaMalloc/cudaMemcpy/cudaLaunchKernel/... live at fixed offsets inside the
// executable and drive an undocumented private context state machine in the
// embedded driver. Rather than reimplement that state machine, we redirect the
// handful of runtime entry points the sample actually uses to host-side
// implementations by overwriting their prologues with an absolute jump. This
// runs NVIDIA's genuine, unmodified binary; only the CUDA runtime calls are
// serviced on the CPU (there is no real GPU).
//
// Offsets are specific to nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0
// and differ per architecture (the image publishes both linux/amd64 and
// linux/arm64). The amd64 build is stripped, so its offsets were recovered by
// disassembly; the arm64 build ships symbols, so its offsets are the addresses
// of the like-named cuda* entry points.
// ---------------------------------------------------------------------------
#if defined(__x86_64__) || defined(__aarch64__)
#if defined(__x86_64__)
#define MOCK_RT_MALLOC       0x56760
#define MOCK_RT_FREE         0x56f40
#define MOCK_RT_MEMCPY       0x6f4d0
#define MOCK_RT_LAUNCH       0x746c0
#define MOCK_RT_GETLASTERROR 0x4d310
#elif defined(__aarch64__)
#define MOCK_RT_MALLOC       0x4acc8
#define MOCK_RT_FREE         0x4b338
#define MOCK_RT_MEMCPY       0x5f1c8
#define MOCK_RT_LAUNCH       0x63648
#define MOCK_RT_GETLASTERROR 0x43190
#endif
// MOCK_TRAMP_LEN (per-arch trampoline size) comes from patch.h.

// Number of runtime entry points redirected (kept in sync with the sites[]
// table in mockPatchRuntime); sizes the saved-original rollback buffer.
#define MOCK_RT_SITES 5

static cudaError_t mockRtMalloc(void** devPtr, size_t size) {
	if (devPtr == NULL) {
		return cudaErrorInvalidValue;
	}
	// "Device" memory is plain host memory; the sample memcpys into it and the
	// emulated kernel reads/writes it directly.
	void* p = malloc(size ? size : 1);
	if (p == NULL) {
		return cudaErrorMemoryAllocation;
	}
	*devPtr = p;
	return cudaSuccess;
}

static cudaError_t mockRtFree(void* devPtr) {
	free(devPtr);
	return cudaSuccess;
}

static cudaError_t mockRtMemcpy(void* dst, const void* src, size_t count, int kind) {
	(void)kind;
	if (dst != NULL && src != NULL && count > 0) {
		memcpy(dst, src, count);
	}
	return cudaSuccess;
}

static cudaError_t mockRtGetLastError(void) {
	return cudaSuccess;
}

static cudaError_t mockRtLaunch(const void* func, dim3 gridDim, dim3 blockDim,
	void** args, size_t sharedMem, void* stream) {
	(void)func;
	(void)gridDim;
	(void)blockDim;
	(void)sharedMem;
	(void)stream;
	mockCudaVectorAdd((void*)args);
	return cudaSuccess;
}

// mockWriteCode overwrites len bytes at dst, toggling write permission around
// the store and flushing the I-cache afterwards (required on aarch64; harmless
// on x86). Returns 0 on success. The caller must have verified dst is
// executable-mapped via mockRangeExec. On failure the destination is left
// unchanged (mprotect is the only step that can fail, and it fails before the
// store).
static int mockWriteCode(unsigned char* dst, const unsigned char* code, size_t len) {
	long ps = sysconf(_SC_PAGESIZE);
	if (ps <= 0) {
		ps = 4096;
	}
	uintptr_t start = (uintptr_t)dst & ~((uintptr_t)ps - 1);
	size_t span = (size_t)(((uintptr_t)dst + len) - start);
	if (mprotect((void*)start, span, PROT_READ | PROT_WRITE | PROT_EXEC) != 0) {
		return -1;
	}
	memcpy(dst, code, len);
	mprotect((void*)start, span, PROT_READ | PROT_EXEC);
	__builtin___clear_cache((char*)dst, (char*)dst + len);
	return 0;
}

// mockPatchRuntime redirects the handful of CUDA runtime entry points the
// sample uses to host-side implementations. It is all-or-nothing: every site is
// bounds-checked and identity-verified first, then patched while the original
// bytes are saved, and any write failure rolls back the sites already patched.
// A partial patch (e.g. cudaMalloc redirected but cudaMemcpy not) would hand
// host pointers to the genuine static runtime and crash, so the mock installs
// the full set or none of it.
static void mockPatchRuntime(void) {
	static int done = 0;
	if (done) {
		return;
	}
	done = 1;
	unsigned long base = mockExeBase();
	if (base == 0) {
		return;
	}
	int dbg = getenv("MOCK_CUDA_DEBUG") != NULL;

	struct {
		unsigned long off;
		void* target;
	} sites[MOCK_RT_SITES] = {
		{ MOCK_RT_MALLOC, (void*)mockRtMalloc },
		{ MOCK_RT_FREE, (void*)mockRtFree },
		{ MOCK_RT_MEMCPY, (void*)mockRtMemcpy },
		{ MOCK_RT_LAUNCH, (void*)mockRtLaunch },
		{ MOCK_RT_GETLASTERROR, (void*)mockRtGetLastError },
	};

	// Preflight: every site must sit in an executable mapping. This prevents
	// faulting on an unmapped offset and rejects any binary whose layout does
	// not match this build before we read or write anything.
	for (int i = 0; i < MOCK_RT_SITES; i++) {
		if (!mockRangeExec(base + sites[i].off, MOCK_TRAMP_LEN)) {
			if (dbg) {
				fprintf(stderr, "[CUDA] runtime patch skipped (offset 0x%lx not executable) base=0x%lx\n", sites[i].off, base);
			}
			return;
		}
	}

	// Identity gate: the cudaMalloc prologue must match the known build for this
	// architecture. Guards against mis-patching any other binary this library
	// might be preloaded into.
	//   x86-64 : push %rbp; mov %rsp,%rbp        (55 48 89 E5)
	//   aarch64: stp x29,x30,[sp,#-N]!; mov x29,sp
	//            (stp masked to 0xA9007BFD, then 0x910003FD)
	unsigned char* probe = (unsigned char*)(base + MOCK_RT_MALLOC);
#if defined(__x86_64__)
	int prologueOK = (probe[0] == 0x55 && probe[1] == 0x48 && probe[2] == 0x89 && probe[3] == 0xE5);
#elif defined(__aarch64__)
	uint32_t i0, i1;
	memcpy(&i0, probe, 4);
	memcpy(&i1, probe + 4, 4);
	int prologueOK = ((i0 & 0xFF00FFFFu) == 0xA9007BFDu && i1 == 0x910003FDu);
#endif
	if (!prologueOK) {
		if (dbg) {
			fprintf(stderr, "[CUDA] runtime patch skipped (unexpected binary) base=0x%lx\n", base);
		}
		return;
	}

	// All-or-nothing: save each site's original bytes, then patch. On the first
	// write failure, restore the sites already patched and bail.
	unsigned char saved[MOCK_RT_SITES][MOCK_TRAMP_LEN];
	for (int i = 0; i < MOCK_RT_SITES; i++) {
		unsigned char* dst = (unsigned char*)(base + sites[i].off);
		memcpy(saved[i], dst, MOCK_TRAMP_LEN);
		unsigned char code[MOCK_TRAMP_LEN];
		mockBuildTramp(code, sites[i].target);
		if (mockWriteCode(dst, code, MOCK_TRAMP_LEN) != 0) {
			for (int j = 0; j < i; j++) {
				mockWriteCode((unsigned char*)(base + sites[j].off), saved[j], MOCK_TRAMP_LEN);
			}
			if (dbg) {
				fprintf(stderr, "[CUDA] runtime patch FAILED at 0x%lx; rolled back %d site(s) base=0x%lx\n", sites[i].off, i, base);
			}
			return;
		}
	}
	if (dbg) {
		fprintf(stderr, "[CUDA] runtime API interposition installed base=0x%lx\n", base);
	}
}
#else
// Unsupported architecture: the mock provides the exported CUDA symbols but does
// not perform in-memory interposition (which requires per-arch offsets and
// trampolines). The statically linked sample will not run against the mock here.
static void mockPatchRuntime(void) {}
#endif

// mockCtor runs at library load (including via LD_PRELOAD) so the runtime
// interposition is in place before the sample's main() executes.
__attribute__((constructor)) static void mockCtor(void) {
	mockInstallSegvHandler();
	mockPatchRuntime();
}

// Export-table call-site offsets in cuda-sample:vectoradd-cuda12.5.0
// (return address - load base). Each corresponds to a specific private driver
// function the static CUDA 12.5 runtime invokes; the required behaviour was
// derived by disassembling the binary.
#define MOCK_OFF_VER_A 0x2381c // versionFn(this,out): *out must be > 0x1d5
#define MOCK_OFF_VER_B 0x2383c // fn2(this,out):      *out must be > 0xd
#define MOCK_OFF_BOOL0 0x3c51c // boolFn(): return value compared == 1

static long mockCudaExportProbe2(int slot, void* ret, void* a1, void* a2, void* a3, void* a4, void* a5) {
	unsigned long base = mockExeBase();
	unsigned long off = base ? ((unsigned long)ret - base) : (unsigned long)ret;
	long rv = 0;
	switch (off) {
	case MOCK_OFF_VER_A:
		if (a2 != NULL) {
			*(unsigned long*)a2 = 0x1d6UL;
		}
		break;
	case MOCK_OFF_VER_B:
		if (a2 != NULL) {
			*(unsigned long*)a2 = 0xeUL;
		}
		break;
	case MOCK_OFF_BOOL0:
		rv = 1;
		break;
	default:
		break;
	}
	if (getenv("MOCK_CUDA_DEBUG")) {
		fprintf(stderr, "[CUDA] export slot 0x%x off=0x%lx (args %p %p %p %p %p) -> %ld\n",
			(slot + 1) * 8, off, a1, a2, a3, a4, a5, rv);
	}
	return rv;
}
#define mockCudaExportProbe(slot, a1, a2, a3, a4, a5) mockCudaExportProbe2((slot), __builtin_return_address(0), (a1), (a2), (a3), (a4), (a5))

// Distinct trampolines (indices 0..127). Each has a unique address the runtime
// can call and we identify by slot.
#define T(n) static long mockExportTramp##n(void* a1, void* a2, void* a3, void* a4, void* a5) { return mockCudaExportProbe(n, a1, a2, a3, a4, a5); }
T(0) T(1) T(2) T(3) T(4) T(5) T(6) T(7) T(8) T(9) T(10) T(11) T(12) T(13) T(14) T(15)
T(16) T(17) T(18) T(19) T(20) T(21) T(22) T(23) T(24) T(25) T(26) T(27) T(28) T(29) T(30) T(31)
T(32) T(33) T(34) T(35) T(36) T(37) T(38) T(39) T(40) T(41) T(42) T(43) T(44) T(45) T(46) T(47)
T(48) T(49) T(50) T(51) T(52) T(53) T(54) T(55) T(56) T(57) T(58) T(59) T(60) T(61) T(62) T(63)
T(64) T(65) T(66) T(67) T(68) T(69) T(70) T(71) T(72) T(73) T(74) T(75) T(76) T(77) T(78) T(79)
T(80) T(81) T(82) T(83) T(84) T(85) T(86) T(87) T(88) T(89) T(90) T(91) T(92) T(93) T(94) T(95)
T(96) T(97) T(98) T(99) T(100) T(101) T(102) T(103) T(104) T(105) T(106) T(107) T(108) T(109) T(110) T(111)
T(112) T(113) T(114) T(115) T(116) T(117) T(118) T(119) T(120) T(121) T(122) T(123) T(124) T(125) T(126) T(127)
#undef T

#define TE(n) (void*)mockExportTramp##n
static void* mockCudaExportTable[MOCK_EXPORT_SLOTS + 1] = {
	(void*)0x1000, // slot 0: struct size (large enough to pass validation)
	TE(0), TE(1), TE(2), TE(3), TE(4), TE(5), TE(6), TE(7), TE(8), TE(9), TE(10), TE(11), TE(12), TE(13), TE(14), TE(15),
	TE(16), TE(17), TE(18), TE(19), TE(20), TE(21), TE(22), TE(23), TE(24), TE(25), TE(26), TE(27), TE(28), TE(29), TE(30), TE(31),
	TE(32), TE(33), TE(34), TE(35), TE(36), TE(37), TE(38), TE(39), TE(40), TE(41), TE(42), TE(43), TE(44), TE(45), TE(46), TE(47),
	TE(48), TE(49), TE(50), TE(51), TE(52), TE(53), TE(54), TE(55), TE(56), TE(57), TE(58), TE(59), TE(60), TE(61), TE(62), TE(63),
	TE(64), TE(65), TE(66), TE(67), TE(68), TE(69), TE(70), TE(71), TE(72), TE(73), TE(74), TE(75), TE(76), TE(77), TE(78), TE(79),
	TE(80), TE(81), TE(82), TE(83), TE(84), TE(85), TE(86), TE(87), TE(88), TE(89), TE(90), TE(91), TE(92), TE(93), TE(94), TE(95),
	TE(96), TE(97), TE(98), TE(99), TE(100), TE(101), TE(102), TE(103), TE(104), TE(105), TE(106), TE(107), TE(108), TE(109), TE(110), TE(111),
	TE(112), TE(113), TE(114), TE(115), TE(116), TE(117), TE(118), TE(119), TE(120), TE(121), TE(122), TE(123), TE(124), TE(125), TE(126),
};

void* mockCudaGetExportTableProbe(const void* exportTableId) {
	mockPatchAttestation();
	if (getenv("MOCK_CUDA_DEBUG")) {
		char u[64];
		mockCudaFmtUUID(u, exportTableId);
		fprintf(stderr, "[CUDA] cuGetExportTable(id=%s) -> probe table %p\n", u, (void*)mockCudaExportTable);
	}
	return (void*)mockCudaExportTable;
}
