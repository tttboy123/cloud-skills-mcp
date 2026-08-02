---
name: aws-cloud
description: Operate or inspect any AWS resource through the cloud-skills-mcp universal AWS API gateway. Use for AWS, EC2, S3, IAM, Lambda, RDS, EKS, CloudFormation, Cloud Control, or any AWS CLI service/operation task.
---

# AWS Cloud

Use the unified MCP server for every documented AWS CLI service and operation. The gateway fixes the executable to `aws`, validates service/operation names and arguments, and uses the official AWS credential chain.

## Workflow

1. Call `cloud_provider_status` with `provider="aws"`; report only adapter availability and credential source/status. `unverified` is normal before a live API call and is not authentication proof.
2. If the operation or parameters are uncertain, call `aws_api_discover` with the AWS CLI service and operation.
3. Use `aws_api_read` only for operations classified as read-only (`describe*`, `list*`, `get*`, `head*`, `search*`, and similar).
4. For any other operation, obtain explicit human approval for the exact account, region, resources, operation, and expected effect. Then call `aws_api_mutate` with `force=true`.
5. Secret-resource operations require the operator-controlled sensitive gate. Credential issuance or export operations such as STS AssumeRole, login tokens, presigned credentials, and AccessKey creation are never exposed by the gateway.
6. Return the provider RequestId when present. Never return, print, store, or ask the MCP server to reveal credentials.

## MCP arguments

- `service`: AWS CLI service code, such as `ec2`, `s3api`, `iam`, or `cloudcontrolapi`.
- `operation`: documented AWS CLI operation in kebab case.
- `region`: optional region.
- `parameters`: structured JSON passed through a mode-0600 temporary `--cli-input-json` file.
- `arguments`: optional non-credential AWS CLI flags. Endpoint, proxy, TLS-disable, credential, and unapproved file flags are rejected.

Example read: `aws_api_read(service="ec2", operation="describe-instances", region="us-east-1", parameters={"MaxResults":20})`.

## Credentials

Use AWS profiles/SSO, web identity, IAM roles, or the standard `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / optional session token environment chain. Keep credentials in the server environment or official AWS config, never in MCP arguments.

Read [references/official-docs.md](references/official-docs.md) when authentication, Cloud Control, or operation naming needs verification.
