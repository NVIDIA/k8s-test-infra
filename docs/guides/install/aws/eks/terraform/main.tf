# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

data "aws_availability_zones" "available" {
  state = "available"

  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  vpc_cidr = "10.80.0.0/16"
  azs      = slice(data.aws_availability_zones.available.names, 0, 2)

  tags = {
    Project   = "mokka"
    Purpose   = "managed-kubernetes-installation-test"
    ManagedBy = "terraform"
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "6.7.3"

  name = var.cluster_name
  cidr = local.vpc_cidr

  azs             = local.azs
  private_subnets = [for index, _ in local.azs : cidrsubnet(local.vpc_cidr, 4, index)]
  public_subnets  = [for index, _ in local.azs : cidrsubnet(local.vpc_cidr, 8, index + 48)]

  enable_nat_gateway = true
  single_nat_gateway = true

  public_subnet_tags = {
    "kubernetes.io/role/elb" = 1
  }

  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = 1
  }
}

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "21.25.0"

  name               = var.cluster_name
  kubernetes_version = var.kubernetes_version

  endpoint_public_access       = true
  endpoint_public_access_cidrs = var.cluster_endpoint_public_access_cidrs
  endpoint_private_access      = true

  enable_cluster_creator_admin_permissions = true

  addons = {
    coredns    = {}
    kube-proxy = {}
    vpc-cni = {
      before_compute = true
    }
  }

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  eks_managed_node_groups = {
    mokka = {
      ami_type            = "AL2023_x86_64_NVIDIA"
      ami_release_version = var.eks_ami_release_version
      instance_types      = var.worker_instance_types

      min_size     = var.worker_count
      max_size     = var.worker_count
      desired_size = var.worker_count

      disk_size = 30

      # Own the full AL2023 nodeadm document so the runtime configuration can
      # be extended before nodeadm starts containerd.
      enable_bootstrap_user_data = true

      # nodeadm merges this partial NodeConfig into the module-generated
      # configuration, preserving the AMI defaults while declaring the NRI
      # endpoint that Mokka can connect to when its NRI plugin is enabled.
      cloudinit_pre_nodeadm = [
        {
          content_type = "application/node.eks.aws"
          content      = <<-EOT
            ---
            apiVersion: node.eks.aws/v1alpha1
            kind: NodeConfig
            spec:
              containerd:
                config: |
                  [plugins."io.containerd.nri.v1.nri"]
                    disable = false
                    disable_connections = false
                    socket_path = "/var/run/nri/nri.sock"
          EOT
        }
      ]

      # The accelerated AL2023 image already installs and registers the NVIDIA
      # runtime. Select CDI mode after nodeadm has generated the base runtime
      # configuration, then restart containerd once and verify the result.
      cloudinit_post_nodeadm = [
        {
          content_type = "text/x-shellscript"
          content      = <<-EOT
            #!/bin/bash
            set -euxo pipefail

            nvidia-ctk config --in-place \
              --set nvidia-container-runtime.mode=cdi
            grep -Eq '^[[:space:]]*mode[[:space:]]*=[[:space:]]*"cdi"[[:space:]]*$' \
              /etc/nvidia-container-runtime/config.toml

            systemctl restart containerd
            systemctl is-active --quiet containerd

            for _ in $(seq 30); do
              test -S /var/run/nri/nri.sock && break
              sleep 1
            done

            if ! test -S /var/run/nri/nri.sock; then
              journalctl -u containerd --no-pager | tail -50 || true
              exit 1
            fi
          EOT
        }
      ]

      labels = {
        "mokka.nvidia.com/type" = "sgpu"
      }

      update_config = {
        max_unavailable = 1
      }
    }
  }
}
