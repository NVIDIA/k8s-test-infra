// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main provides CUDA Runtime/Driver API bridge functions.
// Built as a c-shared library to produce libcuda.so.1.

package main

/*
#cgo linux LDFLAGS: -ldl
// The in-memory interposition machinery lives in interpose.c / interpose.h;
// only the entry points Go calls are declared there. stdlib.h and string.h are
// kept for the C.malloc/C.calloc/C.free and C.memset/C.memcpy/C.strncpy calls
// the Go bridge makes directly.
#include <stdlib.h>
#include <string.h>
#include "cuda_types.h"
#include "interpose.h"

*/
import "C"
import (
	"fmt"
	"os"
	"unsafe"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockcuda/engine"
)

// =============================================================================
// Initialization
// =============================================================================

//export cuInit
func cuInit(flags C.uint) C.CUresult {
	C.mockPatchAttestation()
	return C.CUresult(toCudaError(engine.GetEngine().Init(uint(flags))))
}

//export cudaDriverGetVersion
func cudaDriverGetVersion(driverVersion *C.int) C.cudaError_t {
	if driverVersion == nil {
		return C.cudaErrorInvalidValue
	}
	ver, err := engine.GetEngine().DriverGetVersion()
	if err != engine.CudaSuccess {
		return toCudaError(err)
	}
	*driverVersion = C.int(ver)
	return C.cudaSuccess
}

//export cuDriverGetVersion
func cuDriverGetVersion(driverVersion *C.int) C.CUresult {
	if driverVersion == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	ver, err := engine.GetEngine().DriverGetVersion()
	if err != engine.CudaSuccess {
		return C.CUresult(toCudaError(err))
	}
	*driverVersion = C.int(ver)
	return C.CUresult(C.cudaSuccess)
}

// =============================================================================
// Device Management
// =============================================================================

//export cudaGetDeviceCount
func cudaGetDeviceCount(count *C.int) C.cudaError_t {
	if count == nil {
		return C.cudaErrorInvalidValue
	}
	c, err := engine.GetEngine().GetDeviceCount()
	if err != engine.CudaSuccess {
		return toCudaError(err)
	}
	*count = C.int(c)
	return C.cudaSuccess
}

//export cudaSetDevice
func cudaSetDevice(device C.int) C.cudaError_t {
	return toCudaError(engine.GetEngine().SetDevice(int(device)))
}

// =============================================================================
// Memory Management
// =============================================================================

//export cudaMalloc
func cudaMalloc(devPtr *unsafe.Pointer, size C.size_t) C.cudaError_t {
	if devPtr == nil {
		return C.cudaErrorInvalidValue
	}
	// Allocate real C memory so the pointer is valid and go vet clean.
	// The engine tracks it by uintptr key for bookkeeping.
	cPtr := C.malloc(C.size_t(size))
	if cPtr == nil {
		return C.cudaErrorMemoryAllocation
	}
	err := engine.GetEngine().TrackAllocation(uintptr(cPtr), uint64(size))
	if err != engine.CudaSuccess {
		C.free(cPtr)
		return toCudaError(err)
	}
	*devPtr = cPtr
	return C.cudaSuccess
}

//export cudaFree
func cudaFree(devPtr unsafe.Pointer) C.cudaError_t {
	err := engine.GetEngine().Free(uintptr(devPtr))
	if err == engine.CudaSuccess && devPtr != nil {
		C.free(devPtr)
	}
	return toCudaError(err)
}

//export cudaMemcpy
func cudaMemcpy(dst unsafe.Pointer, src unsafe.Pointer, count C.size_t, kind C.cudaMemcpyKind) C.cudaError_t {
	if count == 0 {
		return C.cudaSuccess
	}
	if dst == nil || src == nil {
		return C.cudaErrorInvalidValue
	}
	C.memcpy(dst, src, count)
	return toCudaError(engine.GetEngine().Memcpy(engine.CudaMemcpyKind(kind)))
}

// =============================================================================
// Execution
// =============================================================================

//export cudaLaunchKernel
func cudaLaunchKernel(
	funcPtr unsafe.Pointer,
	gridDim C.dim3,
	blockDim C.dim3,
	args *unsafe.Pointer,
	sharedMem C.size_t,
	stream C.cudaStream_t,
) C.cudaError_t {
	if C.mockCudaVectorAdd(unsafe.Pointer(args)) != 0 {
		cudaDebug("[CUDA] cudaLaunchKernel simulated vectorAdd\n")
	}
	return toCudaError(engine.GetEngine().LaunchKernel())
}

//export cudaDeviceSynchronize
func cudaDeviceSynchronize() C.cudaError_t {
	return toCudaError(engine.GetEngine().DeviceSynchronize())
}

// =============================================================================
// Error Handling
// =============================================================================

//export cudaGetErrorString
func cudaGetErrorString(err C.cudaError_t) *C.char {
	return errStrings.get(engine.CudaError(err))
}

//export cudaGetLastError
func cudaGetLastError() C.cudaError_t {
	return C.cudaSuccess
}

//export cudaPeekAtLastError
func cudaPeekAtLastError() C.cudaError_t {
	return C.cudaSuccess
}

// =============================================================================
// Additional stubs commonly needed by GPU Operator Validator
// =============================================================================

//export cudaGetDevice
func cudaGetDevice(device *C.int) C.cudaError_t {
	if device == nil {
		return C.cudaErrorInvalidValue
	}
	d, err := engine.GetEngine().GetDevice()
	if err != engine.CudaSuccess {
		return toCudaError(err)
	}
	*device = C.int(d)
	return C.cudaSuccess
}

//export cudaDeviceReset
func cudaDeviceReset() C.cudaError_t {
	return C.cudaSuccess
}

//export cudaRuntimeGetVersion
func cudaRuntimeGetVersion(runtimeVersion *C.int) C.cudaError_t {
	if runtimeVersion == nil {
		return C.cudaErrorInvalidValue
	}
	// Return same as driver version for simplicity
	ver, err := engine.GetEngine().DriverGetVersion()
	if err != engine.CudaSuccess {
		return toCudaError(err)
	}
	*runtimeVersion = C.int(ver)
	return C.cudaSuccess
}

// =============================================================================
// CUDA Driver API compatibility for statically linked CUDA runtime samples
// =============================================================================

// Opaque CUDA handles the static runtime may dereference. Back them with large
// zeroed heap buffers (rather than fake integer pointers) so that any field the
// runtime reads from a handle returns 0 instead of faulting.
var (
	mockContext  = newOpaqueHandle()
	mockModule   = newOpaqueHandle()
	mockFunction = newOpaqueHandle()
	mockLibrary  = newOpaqueHandle()
	mockKernel   = newOpaqueHandle()
	mockMemPool  = newOpaqueHandle()
	mockStream   = newOpaqueHandle()
)

func newOpaqueHandle() unsafe.Pointer {
	return unsafe.Pointer(C.calloc(1, 65536))
}

// devicePtr reinterprets a CUdeviceptr as the host address it aliases. In this
// mock, "device" memory is backed by ordinary host malloc() blocks, so a
// device pointer value is always a real host address. This is the one place
// that turns that integer back into a pointer.
func devicePtr(d C.CUdeviceptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(d)) //nolint:govet // mock device pointers are host addresses by construction
}

func cudaDebug(format string, args ...any) {
	if os.Getenv("MOCK_CUDA_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

//export cuGetProcAddress
func cuGetProcAddress(symbol *C.char, pfn *unsafe.Pointer, cudaVersion C.int, flags C.ulonglong, symbolStatus *C.int) C.CUresult {
	return lookupDriverSymbol(symbol, pfn, symbolStatus)
}

//export cuGetProcAddress_v2
func cuGetProcAddress_v2(symbol *C.char, pfn *unsafe.Pointer, cudaVersion C.int, flags C.ulonglong, symbolStatus *C.int) C.CUresult {
	return lookupDriverSymbol(symbol, pfn, symbolStatus)
}

func lookupDriverSymbol(symbol *C.char, pfn *unsafe.Pointer, symbolStatus *C.int) C.CUresult {
	if symbol == nil || pfn == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	symbolName := C.GoString(symbol)
	if symbolName == "" {
		*pfn = nil
		if symbolStatus != nil {
			*symbolStatus = 1
		}
		return C.CUresult(C.cudaErrorInvalidValue)
	}

	ptr := C.mockCudaDlsym(symbol)
	if ptr == nil {
		if os.Getenv("MOCK_CUDA_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "[CUDA] cuGetProcAddress missing: %s\n", symbolName)
		}
		*pfn = nil
		if symbolStatus != nil {
			*symbolStatus = 0
		}
		return C.CUresult(C.cudaSuccess)
	}
	if os.Getenv("MOCK_CUDA_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[CUDA] cuGetProcAddress found: %s -> %p\n", symbolName, ptr)
	}
	*pfn = ptr
	if symbolStatus != nil {
		*symbolStatus = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetCount
func cuDeviceGetCount(count *C.int) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetCount\n")
	if count == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	c, err := engine.GetEngine().GetDeviceCount()
	if err != engine.CudaSuccess {
		return C.CUresult(toCudaError(err))
	}
	*count = C.int(c)
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGet
func cuDeviceGet(device *C.CUdevice, ordinal C.int) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGet(%d)\n", int(ordinal))
	if device == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	if err := engine.GetEngine().SetDevice(int(ordinal)); err != engine.CudaSuccess {
		return C.CUresult(toCudaError(err))
	}
	*device = C.CUdevice(ordinal)
	cudaDebug("[CUDA] cuDeviceGet assigned device=%d\n", int(ordinal))
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetName
func cuDeviceGetName(name *C.char, length C.int, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetName(device=%d)\n", int(device))
	if name == nil || length <= 0 {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	cName := C.CString("Mock CUDA Device")
	defer C.free(unsafe.Pointer(cName))
	C.strncpy(name, cName, C.size_t(length-1))
	*(*C.char)(unsafe.Pointer(uintptr(unsafe.Pointer(name)) + uintptr(length-1))) = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceTotalMem_v2
func cuDeviceTotalMem_v2(bytes *C.size_t, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceTotalMem_v2(device=%d)\n", int(device))
	if bytes == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*bytes = C.size_t(80 * 1024 * 1024 * 1024)
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetAttribute
func cuDeviceGetAttribute(pi *C.int, attrib C.int, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetAttribute(attrib=%d, device=%d)\n", int(attrib), int(device))
	if pi == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}

	value := 1
	switch int(attrib) {
	case 1: // CU_DEVICE_ATTRIBUTE_MAX_THREADS_PER_BLOCK
		value = 1024
	case 2, 3: // MAX_BLOCK_DIM_X/Y
		value = 1024
	case 4: // MAX_BLOCK_DIM_Z
		value = 64
	case 5, 6: // MAX_GRID_DIM_X/Y
		value = 2147483647
	case 7: // MAX_GRID_DIM_Z
		value = 65535
	case 8: // MAX_SHARED_MEMORY_PER_BLOCK
		value = 49152
	case 9: // TOTAL_CONSTANT_MEMORY
		value = 65536
	case 10: // WARP_SIZE
		value = 32
	case 11: // MAX_PITCH
		value = 2147483647
	case 12: // MAX_REGISTERS_PER_BLOCK
		value = 65536
	case 13: // CLOCK_RATE
		value = 1980000
	case 16: // MULTIPROCESSOR_COUNT
		value = 132
	case 31, 32, 41: // CONCURRENT_KERNELS, ECC_ENABLED, UNIFIED_ADDRESSING
		value = 1
	case 33: // PCI_BUS_ID
		value = 0x1a
	case 34: // PCI_DEVICE_ID
		value = 0
	case 36: // MEMORY_CLOCK_RATE
		value = 2619000
	case 37: // GLOBAL_MEMORY_BUS_WIDTH
		value = 5120
	case 38: // L2_CACHE_SIZE
		value = 50 * 1024 * 1024
	case 39: // MAX_THREADS_PER_MULTIPROCESSOR
		value = 2048
	case 75: // COMPUTE_CAPABILITY_MAJOR
		value = 9
	case 76: // COMPUTE_CAPABILITY_MINOR
		value = 0
	case 81: // MAX_SHARED_MEMORY_PER_MULTIPROCESSOR
		value = 228 * 1024
	case 97: // MAX_SHARED_MEMORY_PER_BLOCK_OPTIN
		value = 228 * 1024
	}

	*pi = C.int(value)
	return C.CUresult(C.cudaSuccess)
}

//export cuDevicePrimaryCtxRetain
func cuDevicePrimaryCtxRetain(ctx *C.CUcontext, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDevicePrimaryCtxRetain(device=%d)\n", int(device))
	if ctx == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*ctx = C.CUcontext(mockContext)
	return C.CUresult(C.cudaSuccess)
}

//export cuDevicePrimaryCtxRelease
func cuDevicePrimaryCtxRelease(device C.CUdevice) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuDevicePrimaryCtxSetFlags_v2
func cuDevicePrimaryCtxSetFlags_v2(device C.CUdevice, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuDevicePrimaryCtxGetState
func cuDevicePrimaryCtxGetState(device C.CUdevice, flags *C.uint, active *C.int) C.CUresult {
	if flags != nil {
		*flags = 0
	}
	if active != nil {
		*active = 1
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuDevicePrimaryCtxReset
func cuDevicePrimaryCtxReset(device C.CUdevice) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxSetCurrent
func cuCtxSetCurrent(ctx C.CUcontext) C.CUresult {
	cudaDebug("[CUDA] cuCtxSetCurrent\n")
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxGetCurrent
func cuCtxGetCurrent(ctx *C.CUcontext) C.CUresult {
	cudaDebug("[CUDA] cuCtxGetCurrent\n")
	if ctx == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*ctx = C.CUcontext(mockContext)
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxSynchronize
func cuCtxSynchronize() C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuModuleLoadData
func cuModuleLoadData(module *C.CUmodule, image unsafe.Pointer) C.CUresult {
	if module == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*module = C.CUmodule(mockModule)
	return C.CUresult(C.cudaSuccess)
}

//export cuModuleGetFunction
func cuModuleGetFunction(function *C.CUfunction, module C.CUmodule, name *C.char) C.CUresult {
	if function == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*function = C.CUfunction(mockFunction)
	return C.CUresult(C.cudaSuccess)
}

//export cuLaunchKernel
func cuLaunchKernel(function C.CUfunction, gridX, gridY, gridZ, blockX, blockY, blockZ C.uint, sharedMemBytes C.uint, stream C.cudaStream_t, kernelParams *unsafe.Pointer, extra *unsafe.Pointer) C.CUresult {
	cudaDebug("[CUDA] cuLaunchKernel\n")
	if C.mockCudaVectorAdd(unsafe.Pointer(kernelParams)) != 0 {
		cudaDebug("[CUDA] cuLaunchKernel simulated vectorAdd\n")
	}
	return C.CUresult(toCudaError(engine.GetEngine().LaunchKernel()))
}

// =============================================================================
// CUDA 12 library API (cuLibrary*/cuKernel*)
//
// The statically-linked CUDA 12.5 runtime loads its embedded fatbin through the
// library API rather than cuModuleLoadData. We hand back opaque non-NULL handles
// and resolve every kernel to the single mock function; the actual arithmetic is
// performed in cuLaunchKernel via mockCudaVectorAdd.
// =============================================================================

//export cuLibraryLoadData
func cuLibraryLoadData(library *C.CUlibrary, code unsafe.Pointer, jitOptions unsafe.Pointer, jitOptionsValues unsafe.Pointer, numJitOptions C.uint, libraryOptions unsafe.Pointer, libraryOptionValues unsafe.Pointer, numLibraryOptions C.uint) C.CUresult {
	cudaDebug("[CUDA] cuLibraryLoadData\n")
	if library == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*library = C.CUlibrary(mockLibrary)
	return C.CUresult(C.cudaSuccess)
}

//export cuLibraryLoadFromFile
func cuLibraryLoadFromFile(library *C.CUlibrary, fileName *C.char, jitOptions unsafe.Pointer, jitOptionsValues unsafe.Pointer, numJitOptions C.uint, libraryOptions unsafe.Pointer, libraryOptionValues unsafe.Pointer, numLibraryOptions C.uint) C.CUresult {
	cudaDebug("[CUDA] cuLibraryLoadFromFile\n")
	if library == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*library = C.CUlibrary(mockLibrary)
	return C.CUresult(C.cudaSuccess)
}

//export cuLibraryGetModule
func cuLibraryGetModule(pMod *C.CUmodule, library C.CUlibrary) C.CUresult {
	cudaDebug("[CUDA] cuLibraryGetModule\n")
	if pMod == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*pMod = C.CUmodule(mockModule)
	return C.CUresult(C.cudaSuccess)
}

//export cuLibraryGetKernel
func cuLibraryGetKernel(pKernel *C.CUkernel, library C.CUlibrary, name *C.char) C.CUresult {
	cudaDebug("[CUDA] cuLibraryGetKernel\n")
	if pKernel == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*pKernel = C.CUkernel(mockKernel)
	return C.CUresult(C.cudaSuccess)
}

//export cuKernelGetFunction
func cuKernelGetFunction(pFunc *C.CUfunction, kernel C.CUkernel) C.CUresult {
	cudaDebug("[CUDA] cuKernelGetFunction\n")
	if pFunc == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*pFunc = C.CUfunction(mockFunction)
	return C.CUresult(C.cudaSuccess)
}

//export cuLibraryUnload
func cuLibraryUnload(library C.CUlibrary) C.CUresult {
	cudaDebug("[CUDA] cuLibraryUnload\n")
	return C.CUresult(C.cudaSuccess)
}

//export cuMemAlloc_v2
func cuMemAlloc_v2(dptr *C.CUdeviceptr, bytesize C.size_t) C.CUresult {
	cudaDebug("[CUDA] cuMemAlloc_v2(%d)\n", uint64(bytesize))
	if dptr == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	ptr := C.malloc(bytesize)
	if ptr == nil {
		return C.CUresult(C.cudaErrorMemoryAllocation)
	}
	if err := engine.GetEngine().TrackAllocation(uintptr(ptr), uint64(bytesize)); err != engine.CudaSuccess {
		C.free(ptr)
		return C.CUresult(toCudaError(err))
	}
	*dptr = C.CUdeviceptr(uintptr(ptr))
	return C.CUresult(C.cudaSuccess)
}

//export cuMemFree_v2
func cuMemFree_v2(dptr C.CUdeviceptr) C.CUresult {
	ptr := devicePtr(dptr)
	err := engine.GetEngine().Free(uintptr(ptr))
	if err == engine.CudaSuccess && ptr != nil {
		C.free(ptr)
	}
	return C.CUresult(toCudaError(err))
}

//export cuMemcpyHtoD_v2
func cuMemcpyHtoD_v2(dstDevice C.CUdeviceptr, srcHost unsafe.Pointer, byteCount C.size_t) C.CUresult {
	cudaDebug("[CUDA] cuMemcpyHtoD_v2(%d)\n", uint64(byteCount))
	if byteCount == 0 {
		return C.CUresult(C.cudaSuccess)
	}
	if dstDevice == 0 || srcHost == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	C.memcpy(devicePtr(dstDevice), srcHost, byteCount)
	return C.CUresult(C.cudaSuccess)
}

//export cuMemcpyDtoH_v2
func cuMemcpyDtoH_v2(dstHost unsafe.Pointer, srcDevice C.CUdeviceptr, byteCount C.size_t) C.CUresult {
	cudaDebug("[CUDA] cuMemcpyDtoH_v2(%d)\n", uint64(byteCount))
	if byteCount == 0 {
		return C.CUresult(C.cudaSuccess)
	}
	if dstHost == nil || srcDevice == 0 {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	C.memcpy(dstHost, devicePtr(srcDevice), byteCount)
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceTotalMem
func cuDeviceTotalMem(bytes *C.size_t, device C.CUdevice) C.CUresult {
	return cuDeviceTotalMem_v2(bytes, device)
}

//export cuDevicePrimaryCtxSetFlags
func cuDevicePrimaryCtxSetFlags(device C.CUdevice, flags C.uint) C.CUresult {
	return cuDevicePrimaryCtxSetFlags_v2(device, flags)
}

//export cuCtxCreate
func cuCtxCreate(ctx *C.CUcontext, flags C.uint, device C.CUdevice) C.CUresult {
	if ctx == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*ctx = C.CUcontext(mockContext)
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxDetach
func cuCtxDetach(ctx C.CUcontext) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxPushCurrent
func cuCtxPushCurrent(ctx C.CUcontext) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxPopCurrent
func cuCtxPopCurrent(ctx *C.CUcontext) C.CUresult {
	if ctx != nil {
		*ctx = C.CUcontext(mockContext)
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemcpyHtoD
func cuMemcpyHtoD(dstDevice C.CUdeviceptr, srcHost unsafe.Pointer, byteCount C.size_t) C.CUresult {
	return cuMemcpyHtoD_v2(dstDevice, srcHost, byteCount)
}

//export cuMemcpyDtoH
func cuMemcpyDtoH(dstHost unsafe.Pointer, srcDevice C.CUdeviceptr, byteCount C.size_t) C.CUresult {
	return cuMemcpyDtoH_v2(dstHost, srcDevice, byteCount)
}

//export cuMemAlloc
func cuMemAlloc(dptr *C.CUdeviceptr, bytesize C.size_t) C.CUresult {
	return cuMemAlloc_v2(dptr, bytesize)
}

//export cuMemFree
func cuMemFree(dptr C.CUdeviceptr) C.CUresult {
	return cuMemFree_v2(dptr)
}

//export cuDeviceGetP2PAttribute
func cuDeviceGetP2PAttribute(value *C.int, attrib C.int, srcDevice C.CUdevice, dstDevice C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetP2PAttribute(attrib=%d)\n", int(attrib))
	if value == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*value = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetByPCIBusId
func cuDeviceGetByPCIBusId(device *C.CUdevice, pciBusID *C.char) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetByPCIBusId\n")
	if device == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*device = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetPCIBusId
func cuDeviceGetPCIBusId(pciBusID *C.char, length C.int, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetPCIBusId(device=%d)\n", int(device))
	if pciBusID == nil || length <= 0 {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	busID := C.CString("0000:1A:00.0")
	defer C.free(unsafe.Pointer(busID))
	C.strncpy(pciBusID, busID, C.size_t(length-1))
	*(*C.char)(unsafe.Pointer(uintptr(unsafe.Pointer(pciBusID)) + uintptr(length-1))) = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetUuid
func cuDeviceGetUuid(uuid unsafe.Pointer, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetUuid(device=%d)\n", int(device))
	if uuid == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	C.memset(uuid, 0, 16)
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetTexture1DLinearMaxWidth
func cuDeviceGetTexture1DLinearMaxWidth(maxWidthInElements *C.size_t, format C.int, numChannels C.uint, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetTexture1DLinearMaxWidth\n")
	if maxWidthInElements == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*maxWidthInElements = 1 << 27
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetDefaultMemPool
func cuDeviceGetDefaultMemPool(pool *unsafe.Pointer, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetDefaultMemPool\n")
	if pool == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*pool = mockMemPool
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceSetMemPool
func cuDeviceSetMemPool(device C.CUdevice, pool unsafe.Pointer) C.CUresult {
	cudaDebug("[CUDA] cuDeviceSetMemPool\n")
	return C.CUresult(C.cudaSuccess)
}

//export cuDeviceGetMemPool
func cuDeviceGetMemPool(pool *unsafe.Pointer, device C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuDeviceGetMemPool\n")
	return cuDeviceGetDefaultMemPool(pool, device)
}

//export cuFlushGPUDirectRDMAWrites
func cuFlushGPUDirectRDMAWrites(target C.int, scope C.int) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxGetFlags
func cuCtxGetFlags(flags *C.uint) C.CUresult {
	cudaDebug("[CUDA] cuCtxGetFlags\n")
	if flags == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*flags = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxGetApiVersion
func cuCtxGetApiVersion(ctx C.CUcontext, version *C.uint) C.CUresult {
	cudaDebug("[CUDA] cuCtxGetApiVersion\n")
	if version == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*version = C.uint(engine.DefaultDriverVersion)
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxGetDevice
func cuCtxGetDevice(device *C.CUdevice) C.CUresult {
	cudaDebug("[CUDA] cuCtxGetDevice\n")
	if device == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*device = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxGetLimit
func cuCtxGetLimit(value *C.size_t, limit C.int) C.CUresult {
	if value == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*value = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxSetLimit
func cuCtxSetLimit(limit C.int, value C.size_t) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxGetCacheConfig
func cuCtxGetCacheConfig(config *C.int) C.CUresult {
	if config == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*config = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxSetCacheConfig
func cuCtxSetCacheConfig(config C.int) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxGetSharedMemConfig
func cuCtxGetSharedMemConfig(config *C.int) C.CUresult {
	if config == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*config = 0
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxSetSharedMemConfig
func cuCtxSetSharedMemConfig(config C.int) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxGetStreamPriorityRange
func cuCtxGetStreamPriorityRange(leastPriority *C.int, greatestPriority *C.int) C.CUresult {
	if leastPriority != nil {
		*leastPriority = 0
	}
	if greatestPriority != nil {
		*greatestPriority = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemGetInfo
func cuMemGetInfo(free *C.size_t, total *C.size_t) C.CUresult {
	if free != nil {
		*free = C.size_t(80 * 1024 * 1024 * 1024)
	}
	if total != nil {
		*total = C.size_t(80 * 1024 * 1024 * 1024)
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemAllocManaged
func cuMemAllocManaged(dptr *C.CUdeviceptr, bytesize C.size_t, flags C.uint) C.CUresult {
	return cuMemAlloc_v2(dptr, bytesize)
}

//export cuMemAllocPitch
func cuMemAllocPitch(dptr *C.CUdeviceptr, pitch *C.size_t, widthInBytes C.size_t, height C.size_t, elementSizeBytes C.uint) C.CUresult {
	if pitch != nil {
		*pitch = widthInBytes
	}
	return cuMemAlloc_v2(dptr, widthInBytes*height)
}

//export cuMemGetAddressRange
func cuMemGetAddressRange(base *C.CUdeviceptr, size *C.size_t, dptr C.CUdeviceptr) C.CUresult {
	if base != nil {
		*base = dptr
	}
	if size != nil {
		*size = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemcpy
func cuMemcpy(dst C.CUdeviceptr, src C.CUdeviceptr, byteCount C.size_t) C.CUresult {
	if byteCount == 0 {
		return C.CUresult(C.cudaSuccess)
	}
	if dst == 0 || src == 0 {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	C.memcpy(devicePtr(dst), devicePtr(src), byteCount)
	return C.CUresult(C.cudaSuccess)
}

//export cuMemcpyAsync
func cuMemcpyAsync(dst C.CUdeviceptr, src C.CUdeviceptr, byteCount C.size_t, stream C.cudaStream_t) C.CUresult {
	return cuMemcpy(dst, src, byteCount)
}

//export cuMemcpyHtoDAsync
func cuMemcpyHtoDAsync(dstDevice C.CUdeviceptr, srcHost unsafe.Pointer, byteCount C.size_t, stream C.cudaStream_t) C.CUresult {
	return cuMemcpyHtoD_v2(dstDevice, srcHost, byteCount)
}

//export cuMemcpyDtoHAsync
func cuMemcpyDtoHAsync(dstHost unsafe.Pointer, srcDevice C.CUdeviceptr, byteCount C.size_t, stream C.cudaStream_t) C.CUresult {
	return cuMemcpyDtoH_v2(dstHost, srcDevice, byteCount)
}

//export cuMemcpyDtoD
func cuMemcpyDtoD(dstDevice C.CUdeviceptr, srcDevice C.CUdeviceptr, byteCount C.size_t) C.CUresult {
	return cuMemcpy(dstDevice, srcDevice, byteCount)
}

//export cuMemcpyDtoDAsync
func cuMemcpyDtoDAsync(dstDevice C.CUdeviceptr, srcDevice C.CUdeviceptr, byteCount C.size_t, stream C.cudaStream_t) C.CUresult {
	return cuMemcpy(dstDevice, srcDevice, byteCount)
}

//export cuMemsetD8
func cuMemsetD8(dstDevice C.CUdeviceptr, value C.uchar, count C.size_t) C.CUresult {
	if dstDevice == 0 {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	C.memset(devicePtr(dstDevice), C.int(value), count)
	return C.CUresult(C.cudaSuccess)
}

//export cuMemsetD8Async
func cuMemsetD8Async(dstDevice C.CUdeviceptr, value C.uchar, count C.size_t, stream C.cudaStream_t) C.CUresult {
	return cuMemsetD8(dstDevice, value, count)
}

//export cuMemsetD2D8
func cuMemsetD2D8(dstDevice C.CUdeviceptr, dstPitch C.size_t, value C.uchar, width C.size_t, height C.size_t) C.CUresult {
	for row := uintptr(0); row < uintptr(height); row++ {
		C.memset(unsafe.Pointer(uintptr(devicePtr(dstDevice))+row*uintptr(dstPitch)), C.int(value), width)
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemsetD2D8Async
func cuMemsetD2D8Async(dstDevice C.CUdeviceptr, dstPitch C.size_t, value C.uchar, width C.size_t, height C.size_t, stream C.cudaStream_t) C.CUresult {
	return cuMemsetD2D8(dstDevice, dstPitch, value, width, height)
}

//export cuMemAllocAsync
func cuMemAllocAsync(dptr *C.CUdeviceptr, bytesize C.size_t, stream C.cudaStream_t) C.CUresult {
	return cuMemAlloc_v2(dptr, bytesize)
}

//export cuMemAllocFromPoolAsync
func cuMemAllocFromPoolAsync(dptr *C.CUdeviceptr, bytesize C.size_t, pool unsafe.Pointer, stream C.cudaStream_t) C.CUresult {
	return cuMemAlloc_v2(dptr, bytesize)
}

//export cuMemFreeAsync
func cuMemFreeAsync(dptr C.CUdeviceptr, stream C.cudaStream_t) C.CUresult {
	return cuMemFree_v2(dptr)
}

//export cuMemPoolTrimTo
func cuMemPoolTrimTo(pool unsafe.Pointer, minBytesToKeep C.size_t) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemPoolSetAttribute
func cuMemPoolSetAttribute(pool unsafe.Pointer, attr C.int, value unsafe.Pointer) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemPoolGetAttribute
func cuMemPoolGetAttribute(pool unsafe.Pointer, attr C.int, value unsafe.Pointer) C.CUresult {
	if value != nil {
		*(*C.ulonglong)(value) = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemPoolSetAccess
func cuMemPoolSetAccess(pool unsafe.Pointer, mapInfo unsafe.Pointer, count C.size_t) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemPoolGetAccess
func cuMemPoolGetAccess(flags *C.uint, pool unsafe.Pointer, location unsafe.Pointer) C.CUresult {
	if flags != nil {
		*flags = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemPoolCreate
func cuMemPoolCreate(pool *unsafe.Pointer, props unsafe.Pointer) C.CUresult {
	if pool == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*pool = mockMemPool
	return C.CUresult(C.cudaSuccess)
}

//export cuMemPoolDestroy
func cuMemPoolDestroy(pool unsafe.Pointer) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemFreeHost
func cuMemFreeHost(ptr unsafe.Pointer) C.CUresult {
	if ptr != nil {
		C.free(ptr)
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemHostAlloc
func cuMemHostAlloc(pp *unsafe.Pointer, bytesize C.size_t, flags C.uint) C.CUresult {
	if pp == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*pp = C.malloc(bytesize)
	if *pp == nil {
		return C.CUresult(C.cudaErrorMemoryAllocation)
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemHostGetDevicePointer
func cuMemHostGetDevicePointer(pdptr *C.CUdeviceptr, p unsafe.Pointer, flags C.uint) C.CUresult {
	if pdptr == nil || p == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*pdptr = C.CUdeviceptr(uintptr(p))
	return C.CUresult(C.cudaSuccess)
}

//export cuMemHostGetFlags
func cuMemHostGetFlags(pFlags *C.uint, p unsafe.Pointer) C.CUresult {
	if pFlags != nil {
		*pFlags = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemHostRegister
func cuMemHostRegister(p unsafe.Pointer, bytesize C.size_t, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemHostUnregister
func cuMemHostUnregister(p unsafe.Pointer) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemcpyPeer
func cuMemcpyPeer(dstDevice C.CUdeviceptr, dstContext C.CUcontext, srcDevice C.CUdeviceptr, srcContext C.CUcontext, byteCount C.size_t) C.CUresult {
	return cuMemcpy(dstDevice, srcDevice, byteCount)
}

//export cuMemcpyPeerAsync
func cuMemcpyPeerAsync(dstDevice C.CUdeviceptr, dstContext C.CUcontext, srcDevice C.CUdeviceptr, srcContext C.CUcontext, byteCount C.size_t, stream C.cudaStream_t) C.CUresult {
	return cuMemcpy(dstDevice, srcDevice, byteCount)
}

//export cuDeviceCanAccessPeer
func cuDeviceCanAccessPeer(canAccessPeer *C.int, device C.CUdevice, peerDevice C.CUdevice) C.CUresult {
	if canAccessPeer != nil {
		*canAccessPeer = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxEnablePeerAccess
func cuCtxEnablePeerAccess(peerContext C.CUcontext, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuCtxDisablePeerAccess
func cuCtxDisablePeerAccess(peerContext C.CUcontext) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemAdvise
func cuMemAdvise(devPtr C.CUdeviceptr, count C.size_t, advice C.int, device C.CUdevice) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemPrefetchAsync
func cuMemPrefetchAsync(devPtr C.CUdeviceptr, count C.size_t, dstDevice C.CUdevice, stream C.cudaStream_t) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuMemRangeGetAttribute
func cuMemRangeGetAttribute(data unsafe.Pointer, dataSize C.size_t, attribute C.int, devPtr C.CUdeviceptr, count C.size_t) C.CUresult {
	if data != nil && dataSize > 0 {
		C.memset(data, 0, dataSize)
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuMemRangeGetAttributes
func cuMemRangeGetAttributes(data *unsafe.Pointer, dataSizes *C.size_t, attributes *C.int, numAttributes C.size_t, devPtr C.CUdeviceptr, count C.size_t) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamCreate
func cuStreamCreate(stream *C.cudaStream_t, flags C.uint) C.CUresult {
	if stream == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*stream = C.cudaStream_t(mockStream)
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamCreateWithPriority
func cuStreamCreateWithPriority(stream *C.cudaStream_t, flags C.uint, priority C.int) C.CUresult {
	return cuStreamCreate(stream, flags)
}

//export cuStreamGetPriority
func cuStreamGetPriority(stream C.cudaStream_t, priority *C.int) C.CUresult {
	if priority != nil {
		*priority = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamGetFlags
func cuStreamGetFlags(stream C.cudaStream_t, flags *C.uint) C.CUresult {
	if flags != nil {
		*flags = 0
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamGetCtx
func cuStreamGetCtx(stream C.cudaStream_t, ctx *C.CUcontext) C.CUresult {
	if ctx != nil {
		*ctx = C.CUcontext(mockContext)
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamGetId
func cuStreamGetId(stream C.cudaStream_t, streamID *C.ulonglong) C.CUresult {
	if streamID != nil {
		*streamID = 1
	}
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamDestroy
func cuStreamDestroy(stream C.cudaStream_t) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamSynchronize
func cuStreamSynchronize(stream C.cudaStream_t) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamQuery
func cuStreamQuery(stream C.cudaStream_t) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamWaitEvent
func cuStreamWaitEvent(stream C.cudaStream_t, event unsafe.Pointer, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamAddCallback
func cuStreamAddCallback(stream C.cudaStream_t, callback unsafe.Pointer, userData unsafe.Pointer, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamAttachMemAsync
func cuStreamAttachMemAsync(stream C.cudaStream_t, devPtr C.CUdeviceptr, length C.size_t, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamWaitValue32
func cuStreamWaitValue32(stream C.cudaStream_t, addr C.CUdeviceptr, value C.uint, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamWriteValue32
func cuStreamWriteValue32(stream C.cudaStream_t, addr C.CUdeviceptr, value C.uint, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamWaitValue64
func cuStreamWaitValue64(stream C.cudaStream_t, addr C.CUdeviceptr, value C.ulonglong, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamWriteValue64
func cuStreamWriteValue64(stream C.cudaStream_t, addr C.CUdeviceptr, value C.ulonglong, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuStreamBatchMemOp
func cuStreamBatchMemOp(stream C.cudaStream_t, count C.uint, paramArray unsafe.Pointer, flags C.uint) C.CUresult {
	return C.CUresult(C.cudaSuccess)
}

//export cuModuleGetLoadingMode
func cuModuleGetLoadingMode(mode *C.int) C.CUresult {
	if mode == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*mode = 0
	return C.CUresult(C.cudaSuccess)
}

// cuGetExportTable is the private CUDA driver entry point the statically-linked
// CUDA runtime probes during initialization to obtain optional internal
// function tables (tools callback hooks, private memory-pool helpers, etc.).
// Its ABI is undocumented and version-specific, so instead of fabricating a
// table (which the runtime would later dereference and crash on) the mock
// reports the table as unavailable. The runtime treats CUDA_ERROR_NOT_FOUND as
// "optional table absent" and falls back to the public driver API, which the
// mock implements in full. Verified by disassembling the cuda-sample vectorAdd
// binary: each cuGetExportTable call site is `test %eax,%eax; je <use-table>`,
// so a non-zero result skips the table.
const cudaErrorNotFound = 500

//export cuGetExportTable
func cuGetExportTable(exportTable *unsafe.Pointer, exportTableID unsafe.Pointer) C.CUresult {
	if exportTable == nil {
		return C.CUresult(C.cudaErrorInvalidValue)
	}
	*exportTable = C.mockCudaGetExportTableProbe(exportTableID)
	return C.CUresult(C.cudaSuccess)
}
