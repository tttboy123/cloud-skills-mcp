---
name: google-cloud
description: Operate or inspect Google Cloud resources through cloud-skills-mcp and authenticated googleapis.com REST or raw gRPC over HTTP/2. Use for GCP, Compute Engine, Cloud Storage, IAM, GKE, Cloud SQL, BigQuery, Cloud Run, Speech streaming, or any documented Google Cloud API.
---

# Google Cloud

Use the unified MCP server for authenticated Google Cloud REST APIs and finite gRPC request streams carried directly over HTTP/2. The gateway accepts only `googleapis.com` HTTPS endpoints and gets access tokens from the official Google Auth ADC chain. It never executes gcloud and has no gcloud-token fallback.

## Workflow

1. Call `cloud_provider_status` with `provider="gcp"`; treat `available` as adapter readiness and `credential_status=unverified` as pending live authentication.
2. Use `gcp_api_discover(service=<discovery-name>, operation=<version>)` to read the public Google Discovery document when REST transcoding is available; for gRPC-only methods use the official RPC reference and protobuf definitions.
3. Use `gcp_api_read` for REST `GET`, `HEAD`, or `OPTIONS`. With `auth_scheme="grpc"`, read-like RPC names such as `Get`, `List`, `Recognize`, and `StreamingRecognize` are conservatively accepted even though gRPC itself uses HTTP `POST`.
4. For REST `POST`, `PUT`, `PATCH`, or `DELETE`, and for any gRPC method not classified as read-like, obtain explicit human approval for the project, URL, method, request messages, resources, and effect; then use `gcp_api_mutate(force=true)`.
5. Secret-resource operations require the separate sensitive gate. IAM Credentials, STS/token exchange, service-account private-key creation, API-key export, and sign-in token endpoints are never exposed by the gateway.
6. Never pass OAuth tokens, API keys, signed URL parameters, cookies, or authorization headers.

## MCP arguments

- `method` and `url`: exact documented `googleapis.com` API request.
- `project`: optional quota/billing project, sent as `X-Goog-User-Project`.
- `headers`: non-credential conditional or product headers.
- `body`: JSON-compatible request body.
- `body_file`: binary/media upload body under an operator-approved `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` directory. Do not combine it with `body`; use resumable/chunk upload APIs above 64 MiB.
- `response_file`: new approved-root file for media downloads, exports, or other large responses. Use the official `Range` header above the configured per-call limit; existing files are never overwritten.
- `auth_scheme="grpc"`: direct HTTP/2 gRPC. `method` must be `POST`; `url` must be `https://<service>.googleapis.com/<fully-qualified-Service>/<Method>`; `body_file` must contain one or more standard uncompressed gRPC records (`0x00` plus a four-byte big-endian protobuf length plus the serialized protobuf message); and `response_file` is required for the same raw framed-protobuf response. Do not set `Content-Type`, `TE`, or any `grpc-*` protocol header.
- `stream_interval_ms`: optional delay between complete gRPC request messages. The file is message-framed, so `stream_chunk_bytes` is forbidden. Observe product limits in the official RPC reference; Speech-to-Text v2 limits each streaming audio request message to 15 KB.

Example read: `gcp_api_read(method="GET", url="https://compute.googleapis.com/compute/v1/projects/<project>/aggregated/instances", project="<project>")`.

Example finite Speech-to-Text v2 stream: `gcp_api_read(auth_scheme="grpc", service="speech", operation="StreamingRecognize", method="POST", url="https://speech.googleapis.com/google.cloud.speech.v2.Speech/StreamingRecognize", project="<project>", headers={"x-goog-request-params":"recognizer=projects/<project>/locations/global/recognizers/_"}, body_file="/approved/grpc/speech-request.grpc", response_file="/approved/grpc/speech-response.grpc", stream_interval_ms=100)`.

The first Speech v2 message contains the recognizer/configuration required by the RPC contract and later messages contain audio only. The gateway validates framing, authenticates the HTTP/2 request, checks response framing and `grpc-status`, and atomically publishes the response file; protobuf construction and decoding remain driven by the official service schema rather than guessed JSON transcoding.

## Credentials

Use Application Default Credentials, workload identity, service-account impersonation, or an attached service account. If a credential file is required, set `GOOGLE_APPLICATION_CREDENTIALS` only in the MCP server environment; never pass its contents through MCP.

Read [references/official-docs.md](references/official-docs.md) for ADC, REST authentication, gRPC-over-HTTP/2, discovery, and asset inventory.
