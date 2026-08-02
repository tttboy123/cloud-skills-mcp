# Six-cloud API protocol coverage

Date: 2026-08-02
Status: living implementation inventory

This inventory separates resource addressability from protocol-family support.
An API is usable only when its official endpoint, authentication scheme,
request transport, response transport, and safety classification are all
covered. A generic REST gateway alone is not evidence that every provider API
is complete.

## Implemented common transport

| Capability | Current implementation | Bound |
|---|---|---|
| JSON, XML, form, query, and arbitrary HTTPS methods | Exact provider URL plus scalar query, non-credential headers, JSON/string body | 1 MiB inline request; 2 MiB inline provider response |
| Binary/media upload | `body_file` streams a regular file below an approved root | 64 MiB per request; provider multipart/resumable operations compose larger uploads |
| Binary/media download and export | `response_file` streams a successful response to a new mode-0600 file and atomically publishes it | 1 GiB default per request; operator configurable; provider `Range` operations compose larger downloads |
| Error response | Bounded in-memory read with credential-field redaction | No output file is created |
| Redirect | Disabled | Prevents credentials or signatures crossing hosts |

The six object-storage implementations all expose an official HTTP download
body and byte ranges: [AWS S3 GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html),
[Azure Get Blob](https://learn.microsoft.com/en-us/rest/api/storageservices/get-blob),
[Google Cloud Storage objects.get](https://docs.cloud.google.com/storage/docs/json_api/v1/objects/get),
[Alibaba OSS GetObject](https://www.alibabacloud.com/help/en/oss/developer-reference/getobject),
[Tencent COS GET Object](https://intl.cloud.tencent.com/document/product/436/7753),
and [Baidu BOS GetObject](https://cloud.baidu.com/doc/BOS/s/xkc5pcmcj).

## Authentication and protocol families

| Provider | Implemented | Still requiring implementation or proof |
|---|---|---|
| AWS | SigV4 header-signed HTTPS; AWS SDK default IAM/AKSK/STS chain | SigV4a multi-region signing; AWS event-stream/chunked payload signing; service-by-service live vectors |
| Azure | Entra bearer REST through non-CLI service principal, workload identity, or managed identity; public and sovereign endpoint/audience routing | Official data planes that have no Entra authorization path; long-lived streaming/WebSocket protocols; service-by-service live vectors |
| Google Cloud | ADC OAuth bearer REST on `googleapis.com`; public Discovery documents | APIs with no REST/HTTP transcoding; gRPC streaming and WebSocket transports; service-by-service live vectors |
| Alibaba Cloud | ACS3 OpenAPI and OSS4 REST; credentials-go RAM/OIDC/ECS/AKSK/STS chain | Product-specific legacy signatures or non-HTTP transports not covered by ACS3/OSS4; service-by-service live vectors |
| Tencent Cloud | TC3 API 3.0 and COS REST signatures; AKSK/CAM temporary credentials | Legacy/product-specific signatures outside TC3/COS; long-lived streaming/WebSocket protocols; service-by-service live vectors |
| Baidu AI Cloud | BCE auth v1 and v2 signed HTTPS; AKSK/IAM-STS session token | Product-specific legacy signatures or non-HTTP transports outside BCE v1/v2; service-by-service live vectors |

Credential minting/export and caller-supplied signed URLs, SAS, bearer tokens,
API keys, or Authorization headers remain intentionally outside the public MCP
surface. The only credential entrypoints are operator-controlled AKSK,
temporary AKSK/session tokens, or official IAM identity chains.

## Completion rule

Do not mark a provider or the overall goal complete from this inventory until:

1. every official public resource API is mapped to an implemented protocol
   family or a documented non-resource/security exclusion;
2. each protocol family has hermetic request/signature/transport vectors;
3. representative control-plane, object/data-plane, upload, download, and
   mutation-gate calls pass against real credentials; and
4. a newly published API can be invoked without adding a resource-specific Go
   handler.
