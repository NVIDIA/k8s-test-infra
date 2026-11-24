// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package identity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComponentsAreUnambiguous(t *testing.T) {
	t.Parallel()
	require.NotEqual(t, New("x").String("ab").String("c").Sum(), New("x").String("a").String("bc").Sum())
	require.NotEqual(t, New("x").String("a").Sum(), New("y").String("a").Sum())
	require.NotEqual(t, New("x").Int(1, 2).Sum(), New("x").Int(2, 1).Sum())
}

// TestSharedPrefix checks that extending one prefix in two directions leaves
// each result intact.
func TestSharedPrefix(t *testing.T) {
	t.Parallel()
	base := New("x").String("node")
	a := base.String("a")
	b := base.String("b")
	require.Equal(t, New("x").String("node").String("a").Sum(), a.Sum())
	require.Equal(t, New("x").String("node").String("b").Sum(), b.Sum())
}

func TestUUIDIsVersion5(t *testing.T) {
	t.Parallel()
	u := New("x").UUID()
	require.Len(t, u, 36)
	require.Equal(t, byte('5'), u[14])
}
