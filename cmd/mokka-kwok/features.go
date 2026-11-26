// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

type features struct {
	gpuPublishing, gpuDriver             string
	computeDomains, nodeResourceTopology bool
}

func (f features) validate() error {
	if f.gpuPublishing != "DevicePlugin" && f.gpuPublishing != "DRA" {
		return errors.New("expected DevicePlugin or DRA for --gpu-publishing")
	}
	if len(validation.IsDNS1123Subdomain(f.gpuDriver)) != 0 || len(f.gpuDriver) > 63 {
		return errors.New("expected a DNS subdomain of at most 63 characters for --gpu-driver")
	}
	return nil
}

type requiredAPI struct {
	version   string
	resources []string
}

func (f features) requiredAPIs() []requiredAPI {
	var apis []requiredAPI
	if f.gpuPublishing == "DRA" || f.computeDomains {
		resources := []string{"resourceslices", "resourceclaims"}
		if f.gpuPublishing == "DRA" {
			resources = append(resources, "deviceclasses")
		}
		apis = append(apis, requiredAPI{"resource.k8s.io/v1", resources})
	}
	if f.computeDomains {
		apis = append(apis, requiredAPI{"resource.nvidia.com/v1beta1", []string{"computedomains", "computedomaincliques"}})
	}
	if f.nodeResourceTopology {
		apis = append(apis, requiredAPI{"topology.node.k8s.io/v1alpha2", []string{"noderesourcetopologies"}})
	}
	return apis
}

func (f features) checkAPIs(ctx context.Context, kube kubernetes.Interface) error {
	for _, api := range f.requiredAPIs() {
		list, err := kube.Discovery().ServerResourcesForGroupVersion(api.version)
		if err != nil {
			return fmt.Errorf("discover %s: %w", api.version, err)
		}
		for _, name := range api.resources {
			if !slices.ContainsFunc(list.APIResources, func(r metav1.APIResource) bool { return r.Name == name }) {
				return fmt.Errorf("cluster does not serve %s/%s", api.version, name)
			}
		}
	}
	if f.gpuPublishing == "DRA" {
		class, err := kube.ResourceV1().DeviceClasses().Get(ctx, f.gpuDriver, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("read GPU DeviceClass %s: %w", f.gpuDriver, err)
		}
		if apierrors.IsNotFound(err) || ptr.Deref(class.Spec.ExtendedResourceName, "") != gpuResource {
			zap.L().Warn("GPU DeviceClass is missing or does not advertise nvidia.com/gpu; Pods requesting the extended resource cannot use these Nodes", zap.String("deviceClass", f.gpuDriver))
		}
	}
	return nil
}

func (f features) waitForAPIs(ctx context.Context, kube kubernetes.Interface, interval time.Duration) error {
	var lastError string
	return wait.PollUntilContextCancel(ctx, interval, true, func(ctx context.Context) (bool, error) {
		err := f.checkAPIs(ctx, kube)
		if err == nil {
			return true, nil
		}
		if err.Error() != lastError {
			zap.L().Warn("Waiting for KWOK data plane APIs", zap.Error(err))
			lastError = err.Error()
		}
		return false, nil
	})
}
