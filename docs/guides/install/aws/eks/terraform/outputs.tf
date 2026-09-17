# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

output "cluster_name" {
  description = "EKS cluster name."
  value       = module.eks.cluster_name
}

output "configure_kubectl" {
  description = "Command that adds or refreshes this cluster in kubeconfig."
  value       = "aws eks update-kubeconfig --region ${var.aws_region} --name ${module.eks.cluster_name} --alias ${var.cluster_name}"
}

output "worker_node_group_id" {
  description = "Managed node group that hosts Mokka."
  value       = module.eks.eks_managed_node_groups["mokka"].node_group_id
}

output "vpc_id" {
  description = "VPC created for this disposable cluster."
  value       = module.vpc.vpc_id
}
