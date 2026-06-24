//go:build integration

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

// Package main loads the built libnccl.so.2 and exercises the C ABI:
// version + a CommInitAll/AllReduce/CommDestroy round trip. The cgo bridge
// lives in abi_cgo.go because Go forbids `import "C"` in *_test.go files.
package main

import "testing"

func TestABIRoundTrip(t *testing.T) {
	rc, ver := getVersion()
	if rc != 0 || ver <= 0 {
		t.Fatalf("ncclGetVersion -> rc=%d ver=%d", rc, ver)
	}
	const n = 2
	rc, comms := commInitAll(n)
	if rc != 0 {
		t.Fatal("ncclCommInitAll failed")
	}
	for i := 0; i < n; i++ {
		if rc := allReduce(comms[i]); rc != 0 {
			t.Fatalf("allreduce rank %d -> %d", i, rc)
		}
	}
	for i := 0; i < n; i++ {
		if commDestroy(comms[i]) != 0 {
			t.Fatalf("destroy %d failed", i)
		}
	}
}
