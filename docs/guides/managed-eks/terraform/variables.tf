variable "aws_account_id" {
  description = "AWS account that Terraform is allowed to modify."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.aws_account_id))
    error_message = "aws_account_id must be a 12-digit AWS account ID."
  }
}

variable "aws_region" {
  description = "AWS region in which to create the test cluster."
  type        = string
  default     = "us-west-2"
}

variable "cluster_name" {
  description = "Name used for the EKS cluster and its supporting resources."
  type        = string
  default     = "mokka-817"
}

variable "kubernetes_version" {
  description = "EKS Kubernetes minor version."
  type        = string
  default     = "1.35"
}

variable "eks_ami_release_version" {
  description = "Pinned EKS-optimized AL2023 release for reproducible workers."
  type        = string
  default     = "1.35.7-20260911"
}

variable "cluster_endpoint_public_access_cidrs" {
  description = "CIDR blocks allowed to reach the public Kubernetes API endpoint. Use the operator's public IP as a /32."
  type        = list(string)

  validation {
    condition = length(var.cluster_endpoint_public_access_cidrs) > 0 && alltrue([
      for cidr in var.cluster_endpoint_public_access_cidrs :
      can(cidrnetmask(cidr)) && cidr != "0.0.0.0/0"
    ])
    error_message = "Supply valid restricted CIDRs; 0.0.0.0/0 is intentionally rejected."
  }
}

variable "worker_instance_types" {
  description = "CPU-only EC2 instance types for the Mokka worker group."
  type        = list(string)
  default     = ["t3.large"]

  validation {
    condition     = length(var.worker_instance_types) > 0
    error_message = "worker_instance_types must contain at least one CPU-only EC2 instance type."
  }
}

variable "worker_count" {
  description = "Fixed number of workers. Two validates DaemonSet behavior across nodes."
  type        = number
  default     = 2

  validation {
    condition     = var.worker_count >= 1 && floor(var.worker_count) == var.worker_count
    error_message = "worker_count must be a positive integer."
  }
}

variable "nvidia_container_toolkit_version" {
  description = "NVIDIA Container Toolkit RPM version installed for CDI hooks. No GPU driver is installed."
  type        = string
  default     = "1.20.0-1"
}
