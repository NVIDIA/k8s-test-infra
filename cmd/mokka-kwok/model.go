// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/sgpu/inventory"
	mokkaclient "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	mokkainformers "github.com/NVIDIA/k8s-test-infra/pkg/generated/informers/externalversions/api/v1alpha1"
)

const rackUIDIndex = "rackUID"

// Keep one copy of each object. A rack's bound node names join its UID-indexed
// state to the Pod index. Informers maintain both indexes on updates and deletes.
type model struct {
	racks cache.SharedIndexInformer
	pods  cache.SharedIndexInformer
}

func newModel(mokka mokkaclient.Interface, kube kubernetes.Interface, shard string) model {
	racks := mokkainformers.NewFilteredSGPURackInformer(mokka, 0, cache.Indexers{
		rackUIDIndex: func(obj any) ([]string, error) {
			rack, err := meta.Accessor(obj)
			if err != nil {
				return nil, err
			}
			return []string{string(rack.GetUID())}, nil
		},
	}, func(o *metav1.ListOptions) { o.LabelSelector = labels.Set{inventory.KWOKShardLabel: shard}.String() })
	pods := coreinformers.NewFilteredPodInformer(kube, metav1.NamespaceAll, 0, cache.Indexers{
		"nodeName": func(obj any) ([]string, error) { return []string{obj.(*corev1.Pod).Spec.NodeName}, nil },
	}, func(o *metav1.ListOptions) {
		o.FieldSelector = fields.OneTermNotEqualSelector("spec.nodeName", "").String()
	})
	return model{racks: racks, pods: pods}
}

func (m model) run(ctx context.Context, c config) error {
	group, ctx := errgroup.WithContext(ctx)
	server := health.NewServer(c.listen, health.DefaultShutdownTimeout)
	server.SetReadiness(func() health.Probe {
		if !m.racks.HasSynced() || !m.pods.HasSynced() {
			return health.Unhealthy("waiting for rack and pod caches")
		}
		return health.OK()
	})
	group.Go(func() error { m.racks.RunWithContext(ctx); return nil })
	group.Go(func() error { m.pods.RunWithContext(ctx); return nil })
	group.Go(func() error {
		if cache.WaitForCacheSync(ctx.Done(), m.racks.HasSynced, m.pods.HasSynced) {
			zap.L().Info("Rack and Pod caches synced", zap.String("shard", c.shard),
				zap.Int("racks", len(m.racks.GetStore().ListKeys())), zap.Int("boundPods", len(m.pods.GetStore().ListKeys())))
		}
		return nil
	})
	group.Go(func() error { return server.Run(ctx) })
	return group.Wait()
}
