/*
 * Copyright (c) 2023, NVIDIA CORPORATION.  All rights reserved.
 *
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

package kubernetes

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewGPUJob creates a new GPU job with the given name, image and gpuCount.
// It runs the nvidia cuda sample vectoradd.
func NewGPUJob(name string, image string, gpuCount int) *batchv1.Job {
	if name == "" {
		name = "gpu-job"
	}
	if image == "" {
		image = "nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda11.7.1"
	}
	if gpuCount == 0 {
		gpuCount = 1
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: batchv1.JobSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: name + "-pod",
				},
				Spec: v1.PodSpec{
					RestartPolicy: "Never",
					Containers: []v1.Container{
						{
							Name:  "cuda-container",
							Image: image,
							Resources: v1.ResourceRequirements{
								Limits: v1.ResourceList{
									"nvidia.com/gpu": resource.MustParse(fmt.Sprintf("%d", gpuCount)),
								},
							},
						},
					},
					Tolerations: []v1.Toleration{
						{
							Key:      "nvidia.com/gpu",
							Operator: "Exists",
							Effect:   "NoSchedule",
						},
					},
				},
			},
		},
	}
}
