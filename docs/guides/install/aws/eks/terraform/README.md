# EKS reference cluster (Terraform)

This directory creates a disposable Amazon EKS cluster that matches the
configuration validated in the
[Amazon EKS guide](../README.md): two on-demand `t3.large` AL2023 workers,
labelled `mokka.nvidia.com/type=sgpu`, in private subnets across two
availability zones. It is a reproducibility aid, not a requirement; the guide
works on any compatible existing cluster. It creates billable AWS resources, so
destroy the stack when you finish.

Run every command below from this directory. When the cluster is up, continue
with [Install Mokka](../README.md#2-install-mokka) in the guide; the workers
are already labelled, so skip its labelling step.

Set the variables the commands below use:

```bash
export MOKKA_AWS_PROFILE=default
export MOKKA_AWS_REGION=us-west-2
export MOKKA_CLUSTER=mokka-817
export MOKKA_CONTEXT=mokka-817
```

> [!WARNING]
> **Use a test AWS account**
>
> This Terraform configuration creates IAM roles, networking, an EKS
> cluster, and EC2 instances. Use an isolated account or obtain approval for
> those resources. The `aws_account_id` variable prevents an accidental
> apply to a different account, but it is not a substitute for
> least-privilege IAM.

## Create the cluster

Install [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
and [Terraform](https://developer.hashicorp.com/terraform/install) 1.5.7 or
newer. Your AWS identity needs permission to manage EKS, EC2 networking and
instances, IAM roles, Key Management Service (KMS) keys, and CloudWatch log
groups. A production account should use narrowly scoped permissions.

> [!WARNING]
> **Cluster-creator access is only for the reference cluster**
>
> The template sets `enable_cluster_creator_admin_permissions = true`. The
> EKS module creates an access entry that grants the Terraform caller the
> `AmazonEKSClusterAdminPolicy`, which provides full cluster-admin access.
> This keeps a disposable test cluster operable by the person who creates it;
> it is not a recommended production access model. Production deployments
> should manage access entries explicitly and grant each operator or role
> only the permissions it needs.

### Authenticate and constrain access

Authenticate using your normal AWS CLI flow. For IAM Identity Center (SSO), for
example:

```bash
aws sso login --profile "${MOKKA_AWS_PROFILE}"
aws sts get-caller-identity --profile "${MOKKA_AWS_PROFILE}"
```

Confirm that the returned account is the one you intend to modify. Then prepare
the Terraform inputs:

```bash
cp terraform.tfvars.example terraform.tfvars
curl -fsS https://checkip.amazonaws.com
```

Edit `terraform.tfvars` and replace:

- `aws_account_id` with the 12-digit account from `get-caller-identity`; and
- the example API CIDR with the displayed public IP plus `/32`.

The template rejects `0.0.0.0/0`. Its public EKS endpoint is reachable only
from the listed CIDRs, while its private endpoint remains available inside the
VPC.

Terraform's AWS provider may otherwise select credentials from another
credential source. Export the exact profile credentials into this shell before
each Terraform operation:

```bash
eval "$(aws configure export-credentials \
  --profile "${MOKKA_AWS_PROFILE}" --format env)"
```

### Apply Terraform

```bash
terraform init
terraform fmt -check -recursive
terraform validate
terraform plan -out=mokka.tfplan
terraform apply mokka.tfplan
```

Creation normally takes 15–25 minutes. Keep the generated
`terraform.tfstate` file: it is required for an exact destroy.

Configure kubectl and verify the cluster:

```bash
aws eks update-kubeconfig \
  --profile "${MOKKA_AWS_PROFILE}" \
  --region "${MOKKA_AWS_REGION}" \
  --name "${MOKKA_CLUSTER}" \
  --alias "${MOKKA_CONTEXT}"

kubectl --context "${MOKKA_CONTEXT}" wait \
  --for=condition=Ready nodes --all --timeout=10m
kubectl --context "${MOKKA_CONTEXT}" get nodes -o wide
kubectl --context "${MOKKA_CONTEXT}" get pods -n kube-system
```

Expect two Ready AL2023 nodes in different availability zones, with no external
IP address. CoreDNS, `aws-node`, and `kube-proxy` should be Running.

## Troubleshooting

### Terraform uses the wrong AWS identity

Run `aws sts get-caller-identity`, re-export the selected profile credentials,
and retry. The provider's `allowed_account_ids` check stops the apply if the
account differs from `terraform.tfvars`.

### kubectl cannot reach the API server

Your public IP may have changed. Update
`cluster_endpoint_public_access_cidrs` in `terraform.tfvars`, export the AWS
credentials again, and run `terraform apply`.

## Destroy the cluster

Destroy all of the stack's AWS resources when you finish:

```bash
eval "$(aws configure export-credentials \
  --profile "${MOKKA_AWS_PROFILE}" --format env)"
terraform destroy
```

Review the destroy plan, enter `yes`, and wait for completion. Confirm that it
reports `Destroy complete` before deleting this directory's
Terraform state.
