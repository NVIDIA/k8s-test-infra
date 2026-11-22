// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	mokkav1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	mokkafake "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned/fake"
)

func TestConfig(t *testing.T) {
	t.Parallel()
	c, err := parseConfig([]string{"data-plane", "--shard-name", "replica-0"})
	require.NoError(t, err)
	require.Equal(t, config{shard: "replica-0", listen: ":8080"}, c)
	for _, args := range [][]string{nil, {"data-plane"}, {"data-plane", "--shard-name", "bad/name"}, {"data-plane", "--shard-name", "valid", "--listen-addr", "bad"}} {
		_, err = parseConfig(args)
		require.Error(t, err)
	}
}

func TestRackModel(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rack := &mokkav1.SGPURack{ObjectMeta: metav1.ObjectMeta{Name: "owned", UID: "first", Labels: map[string]string{inventory.KWOKShardLabel: "replica-0"}}}
	other := rack.DeepCopy()
	other.Name = "other"
	other.UID = "second"
	other.Labels[inventory.KWOKShardLabel] = "replica-1"
	mokka := mokkafake.NewSimpleClientset(rack, other)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "test"}, Spec: corev1.PodSpec{NodeName: "node-a"}}
	kube := kubefake.NewSimpleClientset(pod)
	m := newModel(mokka, kube, "replica-0")
	go m.racks.RunWithContext(ctx)
	go m.pods.RunWithContext(ctx)
	require.True(t, cache.WaitForCacheSync(ctx.Done(), m.racks.HasSynced, m.pods.HasSynced))
	require.Equal(t, []string{"owned"}, m.racks.GetStore().ListKeys())
	owned, err := m.racks.GetIndexer().IndexKeys(rackUIDIndex, "first")
	require.NoError(t, err)
	require.Equal(t, []string{"owned"}, owned)
	bound, err := m.pods.GetIndexer().IndexKeys("nodeName", "node-a")
	require.NoError(t, err)
	require.Equal(t, []string{"test/worker"}, bound)
	for _, action := range kube.Actions() {
		switch a := action.(type) {
		case clienttesting.ListAction:
			require.Equal(t, "spec.nodeName!=", a.GetListRestrictions().Fields.String())
		case clienttesting.WatchAction:
			require.Equal(t, "spec.nodeName!=", a.GetWatchRestrictions().Fields.String())
		}
	}
	require.NoError(t, mokka.MokkaV1alpha1().SGPURacks().Delete(ctx, rack.Name, metav1.DeleteOptions{}))
	require.Eventually(t, func() bool { return len(m.racks.GetIndexer().ListIndexFuncValues(rackUIDIndex)) == 0 }, 3*time.Second, 10*time.Millisecond)
}
