// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package nodecatalog

import (
	"slices"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/require"
)

func TestCatalogMaintainsImmutableIndexedSnapshots(t *testing.T) {
	catalog := New()
	blue := catalogNode("blue", "blue-v1", 2, map[string]string{"pool": "blue", "zone": "west"})
	green := catalogNode("green", "green-v1", 1, map[string]string{"pool": "green", "zone": "west"})
	catalog.Upsert(blue)
	catalog.Upsert(green)

	initial := catalog.Snapshot()
	require.Equal(t, []string{"blue", "green"}, recordNames(initial.Records()))
	require.Same(t, initial, catalog.Snapshot(), "unchanged catalogs should reuse their immutable snapshot")
	blueCandidates := catalog.Candidates(labels.SelectorFromSet(labels.Set{"pool": "blue"}))
	require.Equal(t, []string{"blue"}, recordNames(blueCandidates))
	blueRecord, found := catalog.GetByName("blue")
	require.True(t, found)
	require.Same(t, blueRecord, blueCandidates[0], "indexes should point at the canonical record")
	require.Same(t, blueRecord, initial.Records()[0], "snapshots should point at the canonical record")
	require.Equal(t, []string{"blue", "green"}, recordNames(catalog.Candidates(mustSelector(t, "zone"))))

	allocation := initial.AllocationNodes()
	require.Len(t, allocation, 2)
	require.Equal(t, types.UID("blue-v1"), allocation[0].UID)
	require.Same(t, blue, initial.Records()[0].Node())

	updated := blue.DeepCopy()
	updated.Labels["pool"] = "green"
	catalog.Upsert(updated)

	require.Equal(t, "blue", initial.Records()[0].Node().Labels["pool"], "published records must remain immutable")
	require.Equal(t, []string{}, recordNames(catalog.Candidates(labels.SelectorFromSet(labels.Set{"pool": "blue"}))))
	require.Equal(t, []string{"blue", "green"}, recordNames(catalog.Candidates(labels.SelectorFromSet(labels.Set{"pool": "green"}))))
	require.NotSame(t, initial, catalog.Snapshot())
}

func TestCatalogDeleteUsesExactNodeIdentity(t *testing.T) {
	catalog := New()
	catalog.Upsert(catalogNode("same", "old-uid", 1, map[string]string{"pool": "blue"}))
	replacement := catalogNode("same", "new-uid", 2, map[string]string{"pool": "green"})
	catalog.Upsert(replacement)

	catalog.Delete("same", "old-uid")
	record, found := catalog.GetByName("same")
	require.True(t, found)
	require.Equal(t, types.UID("new-uid"), record.Node().UID)
	require.Same(t, record, catalog.GetByUID("new-uid"))
	require.Nil(t, catalog.GetByUID("old-uid"))

	catalog.Delete("same", "new-uid")
	_, found = catalog.GetByName("same")
	require.False(t, found)
	require.Empty(t, catalog.Snapshot().Records())
}

func TestCatalogReusesAllocationViewUntilNodeStateChanges(t *testing.T) {
	catalog := New()
	catalog.Upsert(catalogNode("one", "one-uid", 1, map[string]string{"pool": "blue"}))

	first := catalog.Snapshot().AllocationNodes()
	second := catalog.Snapshot().AllocationNodes()
	require.NotEmpty(t, first)
	require.Same(t, &first[0], &second[0], "reconciles should share one immutable allocation view")

	catalog.Upsert(catalogNode("two", "two-uid", 2, map[string]string{"pool": "blue"}))
	third := catalog.Snapshot().AllocationNodes()
	require.Len(t, third, 2)
	require.NotSame(t, &first[0], &third[0])
}

func BenchmarkCatalogSteadySnapshot100K(b *testing.B) {
	catalog := New()
	for index := range 100_000 {
		identity := strconv.Itoa(index)
		catalog.Upsert(catalogNode("node-"+identity, types.UID("uid-"+identity), int64(index), map[string]string{
			"mokka.nvidia.com/sgpu-node": "true",
			"pool":                       "scale",
		}))
	}
	require.Len(b, catalog.Snapshot().AllocationNodes(), 100_000)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = catalog.Snapshot().AllocationNodes()
	}
}

func mustSelector(t *testing.T, expression string) labels.Selector {
	t.Helper()
	selector, err := labels.Parse(expression)
	require.NoError(t, err)
	return selector
}

func catalogNode(name string, uid types.UID, created int64, nodeLabels map[string]string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: name, UID: uid, CreationTimestamp: metav1.NewTime(time.Unix(created, 0)), Labels: nodeLabels,
	}}
}

func recordNames(records []*Record) []string {
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.Node().Name)
	}
	slices.Sort(names)
	return names
}
