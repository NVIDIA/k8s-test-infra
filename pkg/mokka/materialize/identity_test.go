// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package materialize

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

func TestIdentityV1Vectors(t *testing.T) {
	uid := types.UID("inventory-uid-a")
	require.Equal(t, "2c79cf0a-9456-5f4d-aef6-c9dba304466a", FabricUUID(uid, "compute", 2))
	require.Equal(t, "GPU-e1fc2f7b-7e71-5cdd-addf-11103758e19f", GPUUUID(uid, "compute", 2, 0, 0))
	require.Equal(t, "01184543437958700640", GPUSerial(uid, "compute", 2, 0, 0))
}

func TestIdentitiesHaveSeparateStableDomains(t *testing.T) {
	uid := types.UID("inventory-uid-a")
	require.Equal(t, FabricUUID(uid, "compute", 2), FabricUUID(uid, "compute", 2))
	require.Equal(t, GPUUUID(uid, "compute", 2, 0, 0), GPUUUID(uid, "compute", 2, 0, 0))
	require.Equal(t, GPUSerial(uid, "compute", 2, 0, 0), GPUSerial(uid, "compute", 2, 0, 0))

	require.NotEqual(t, FabricUUID(uid, "compute", 2), FabricUUID(types.UID("replacement-uid"), "compute", 2))
	require.NotEqual(t, GPUUUID(uid, "compute", 2, 0, 0), GPUUUID(uid, "compute", 2, 0, 1))
	require.NotEqual(t, GPUSerial(uid, "compute", 2, 0, 0), GPUSerial(uid, "compute", 2, 0, 1))
	require.NotEqual(t, "GPU-"+FabricUUID(uid, "compute", 2), GPUUUID(uid, "compute", 2, 0, 0))
}
