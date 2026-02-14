import type { Project } from '../types';

export const projects: Project[] = [
  {
    slug: 'gpu-operator',
    name: 'GPU Operator',
    repo: 'NVIDIA/gpu-operator',
    description: 'Automates the management of all NVIDIA software components needed to provision GPU nodes in Kubernetes.',
    category: 'operator',
  },
  {
    slug: 'nvidia-container-toolkit',
    name: 'Container Toolkit',
    repo: 'NVIDIA/nvidia-container-toolkit',
    description: 'Build and run GPU-accelerated containers with automatic NVIDIA driver and runtime configuration.',
    category: 'runtime',
  },
  {
    slug: 'k8s-device-plugin',
    name: 'K8s Device Plugin',
    repo: 'NVIDIA/k8s-device-plugin',
    description: 'Kubernetes device plugin to expose GPUs as schedulable resources in a cluster.',
    category: 'operator',
  },
  {
    slug: 'k8s-dra-driver-gpu',
    name: 'K8s DRA Driver GPU',
    repo: 'NVIDIA/k8s-dra-driver-gpu',
    description: 'Dynamic Resource Allocation (DRA) driver for NVIDIA GPUs in Kubernetes 1.31+.',
    category: 'driver',
  },
  {
    slug: 'holodeck',
    name: 'Holodeck',
    repo: 'NVIDIA/holodeck',
    description: 'Declarative cloud infrastructure provisioning for GPU-accelerated Kubernetes test environments.',
    category: 'testing',
  },
  {
    slug: 'go-nvml',
    name: 'go-nvml',
    repo: 'NVIDIA/go-nvml',
    description: 'Go bindings for the NVIDIA Management Library (NVML) for GPU monitoring and management.',
    category: 'library',
  },
  {
    slug: 'mig-parted',
    name: 'MIG Parted',
    repo: 'NVIDIA/mig-parted',
    description: 'Declarative tool for partitioning NVIDIA GPUs into MIG (Multi-Instance GPU) devices.',
    category: 'library',
  },
  {
    slug: 'gpu-driver-container',
    name: 'GPU Driver Container',
    repo: 'NVIDIA/gpu-driver-container',
    description: 'Containerized NVIDIA GPU drivers for automated deployment and lifecycle management.',
    category: 'driver',
  },
  {
    slug: 'k8s-nim-operator',
    name: 'K8s NIM Operator',
    repo: 'NVIDIA/k8s-nim-operator',
    description: 'Kubernetes operator for managing NVIDIA NIM (NVIDIA Inference Microservices) deployments.',
    category: 'operator',
  },
];
