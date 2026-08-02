---
name: google-cloud
description: Operate or inspect any Google Cloud resource through cloud-skills-mcp and authenticated googleapis.com REST. Use for GCP, Compute Engine, Cloud Storage, IAM, GKE, Cloud SQL, BigQuery, Cloud Run, or any documented Google Cloud API.
---

# Google Cloud

Use the unified MCP server for authenticated Google Cloud REST APIs. The gateway accepts only `googleapis.com` HTTPS endpoints and gets access tokens directly from the official Google Auth ADC chain, with authenticated gcloud identity as a compatibility fallback.

## Workflow

1. Call `cloud_provider_status` with `provider="gcp"`.
2. Use `gcp_api_discover(service=<discovery-name>, operation=<version>)` to read the public Google Discovery document when available; otherwise use the linked official API reference.
3. Use `gcp_api_read` for `GET`, `HEAD`, or `OPTIONS` only.
4. For `POST`, `PUT`, `PATCH`, or `DELETE`, obtain explicit human approval for the project, URL, method, body, resources, and effect; then use `gcp_api_mutate(force=true)`.
5. Secret, credential, token, password, service-account-key, and access-key endpoints require the separate sensitive gate.
6. Never pass OAuth tokens, API keys, signed URL parameters, cookies, or authorization headers.

## MCP arguments

- `method` and `url`: exact documented `googleapis.com` API request.
- `project`: optional quota/billing project, sent as `X-Goog-User-Project`.
- `headers`: non-credential conditional or product headers.
- `body`: JSON-compatible request body.
- `body_file`: binary/media upload body under an operator-approved `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` directory. Do not combine it with `body`; use resumable/chunk upload APIs above 64 MiB.

Example read: `gcp_api_read(method="GET", url="https://compute.googleapis.com/compute/v1/projects/<project>/aggregated/instances", project="<project>")`.

## Credentials

Use Application Default Credentials, workload identity, service-account impersonation, or an authenticated gcloud account. If a service-account JSON file is required, set `GOOGLE_APPLICATION_CREDENTIALS` only in the MCP server environment; never pass its contents through MCP.

Read [references/official-docs.md](references/official-docs.md) for ADC, REST authentication, discovery, and asset inventory.
