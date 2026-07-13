package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSharedLibraryExportsDriverVersionAPI(t *testing.T) {
	out := filepath.Join(t.TempDir(), "libcuda.so")

	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".")
	buildOut, err := cmd.CombinedOutput()
	require.NoError(t, err, string(buildOut))

	cmd = exec.Command("go", "tool", "nm", out)
	nmOut, err := cmd.CombinedOutput()
	require.NoError(t, err, string(nmOut))

	header, err := os.ReadFile(out[:len(out)-len(filepath.Ext(out))] + ".h")
	require.NoError(t, err)
	// cgo renders the Go `C.ulonglong` flags parameter as `long long unsigned int`.
	require.Contains(t, string(header), "cuGetProcAddress(char* symbol, void** pfn, int cudaVersion, long long unsigned int flags, int* symbolStatus)")

	symbols := string(nmOut)
	for _, symbol := range []string{
		"cudaDriverGetVersion",
		"cuDriverGetVersion",
		"cuGetProcAddress",
		"cuGetProcAddress_v2",
		"cuGetExportTable",
		"cuDeviceGet",
		"cuDeviceGetCount",
		"cuDeviceGetName",
		"cuDeviceTotalMem_v2",
		"cuDeviceGetAttribute",
		"cuDevicePrimaryCtxRetain",
		"cuDevicePrimaryCtxRelease",
		"cuCtxSetCurrent",
		"cuCtxGetCurrent",
		"cuCtxSynchronize",
		"cuModuleLoadData",
		"cuModuleGetFunction",
		"cuLaunchKernel",
		"cuLibraryLoadData",
		"cuLibraryGetKernel",
		"cuLibraryGetModule",
		"cuKernelGetFunction",
		"cuLibraryUnload",
		"cuMemAlloc_v2",
		"cuMemFree_v2",
		"cuMemcpyHtoD_v2",
		"cuMemcpyDtoH_v2",
		"cuMemFree",
		"cuMemAlloc",
		"cuMemcpyDtoH",
		"cuMemcpyHtoD",
		"cuCtxPopCurrent",
		"cuCtxPushCurrent",
		"cuCtxDetach",
		"cuCtxCreate",
		"cuDevicePrimaryCtxSetFlags",
		"cuDeviceTotalMem",
		"cuDeviceGetP2PAttribute",
		"cuDeviceGetByPCIBusId",
		"cuDeviceGetPCIBusId",
		"cuDeviceGetUuid",
		"cuDeviceGetTexture1DLinearMaxWidth",
		"cuDeviceGetDefaultMemPool",
		"cuDeviceSetMemPool",
		"cuDeviceGetMemPool",
		"cuFlushGPUDirectRDMAWrites",
		"cuCtxGetFlags",
		"cuCtxGetApiVersion",
		"cuCtxGetDevice",
		"cuCtxGetLimit",
		"cuCtxSetLimit",
		"cuCtxGetCacheConfig",
		"cuCtxSetCacheConfig",
		"cuCtxGetSharedMemConfig",
		"cuCtxSetSharedMemConfig",
		"cuCtxGetStreamPriorityRange",
		"cuMemGetInfo",
		"cuMemAllocManaged",
		"cuMemAllocPitch",
		"cuMemGetAddressRange",
		"cuMemcpy",
		"cuMemcpyAsync",
		"cuMemcpyHtoDAsync",
		"cuMemcpyDtoHAsync",
		"cuMemcpyDtoD",
		"cuMemcpyDtoDAsync",
		"cuMemsetD8",
		"cuMemsetD8Async",
		"cuMemsetD2D8",
		"cuMemsetD2D8Async",
	} {
		require.Contains(t, symbols, symbol)
	}
}
