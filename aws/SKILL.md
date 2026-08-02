---
name: aws-cloud
description: Operate or inspect any AWS resource through direct SigV4 or SigV4a HTTPS requests in cloud-skills-mcp. Use for AWS, EC2, S3, S3 Multi-Region Access Points, IAM, Lambda, RDS, EKS, CloudFormation, Cloud Control, or any documented AWS API.
---

# AWS Cloud

Use the unified MCP server for documented AWS HTTP APIs. The gateway validates the exact official endpoint, signs the request in-process with AWS SigV4 or SigV4a, and never executes AWS CLI.

## Workflow

1. Call `cloud_provider_status` with `provider="aws"`; report only adapter availability and credential source/status. `unverified` is normal before a live API call and is not authentication proof.
2. Verify the service endpoint, HTTP request shape, signing service, region, action, and API version in the official API reference; `aws_api_discover` returns navigation links.
3. Use `aws_api_read` only for operations classified as read-only (`describe*`, `list*`, `get*`, `head*`, `search*`, and similar).
4. For any other operation, obtain explicit human approval for the exact account, region, resources, operation, and expected effect. Then call `aws_api_mutate` with `force=true`.
5. Secret-resource operations require the operator-controlled sensitive gate. Credential issuance or export operations such as STS AssumeRole, login tokens, presigned credentials, and AccessKey creation are never exposed by the gateway.
6. Return the provider RequestId when present. Never return, print, store, or ask the MCP server to reveal credentials.

## MCP arguments

- `auth_scheme`: `sigv4` (default) or `sigv4a` when the official endpoint requires multi-region signing.
- `service`: SigV4 signing service, such as `ec2`, `s3`, `iam`, or `cloudcontrolapi`.
- `operation`: documented action name used for read/write classification.
- `region`: required SigV4 signing region; use the documented pseudo-region for global services.
- `region_set`: required only for SigV4a, as a comma-separated official region set such as `us-east-1,us-west-*`. It is not a token or credential.
- `payload_mode`: use `aws-chunked` only for SigV4 S3 `PutObject` or `UploadPart` streaming bodies. The server creates the 64 KiB chunk framing and chained signatures.
- `payload_mode=aws-eventstream`: for a finite SigV4 HTTP request whose `body_file` contains consecutive CRC-valid unsigned Amazon EventStream frames. The server validates the 24 MiB per-frame bound, adds signing envelopes and a terminal frame. This is not an interactive WebSocket transport.
- `method` and `url`: exact official AWS HTTPS request.
- `parameters`: optional scalar query parameters; use `body` for Query/JSON protocol payloads.
- `headers`, `body`, `body_file`: non-credential request data; local files require an operator-approved root.
- `response_file`: optional new approved-root file for large/binary responses. Use the documented `Range` header for objects larger than the configured per-call limit; existing files are never overwritten.

Example read: `aws_api_read(auth_scheme="sigv4", service="ec2", operation="describe-instances", region="us-east-1", method="POST", url="https://ec2.us-east-1.amazonaws.com/", headers={"Content-Type":"application/x-www-form-urlencoded"}, body="Action=DescribeInstances&Version=2016-11-15&MaxResults=20")`.

Example streaming upload: `aws_api_mutate(auth_scheme="sigv4", payload_mode="aws-chunked", service="s3", operation="put-object", region="us-east-1", method="PUT", url="https://bucket.s3.us-east-1.amazonaws.com/object", body_file="/approved/uploads/object.bin", force=true)`.

Example finite event stream: `aws_api_mutate(auth_scheme="sigv4", payload_mode="aws-eventstream", service="transcribestreaming", operation="start-stream-transcription", region="us-east-1", method="POST", url="https://transcribestreaming.us-east-1.amazonaws.com/stream-transcription", body_file="/approved/streams/audio.events", response_file="/approved/streams/transcript.events", force=true)`.

## Credentials

Use the AWS SDK credential chain: profiles/SSO, web identity, IAM roles, or `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / optional `AWS_SESSION_TOKEN`. Keep credentials in the server environment or official AWS config, never in MCP arguments.

Read [references/official-docs.md](references/official-docs.md) when authentication, Cloud Control, or operation naming needs verification.
