# Six-cloud API protocol coverage

Date: 2026-08-03
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
| AWS | SigV4 and SigV4a header-signed HTTPS; SigV4/SigV4a S3 `aws-chunked` streaming payload signing with optional signed CRC32, CRC32C, CRC64NVME, SHA-1, or SHA-256 trailer; finite SigV4 HTTP EventStream request signing and raw response-file streaming; AWS SDK default IAM/AKSK/STS chain | Interactive WebSocket sessions; service-by-service live vectors |
| Azure | Entra bearer REST through non-CLI service principal, workload identity, or managed identity; public and sovereign endpoint/audience routing; Azure Maps public/geographic endpoint scope; Azure Health Data Services and legacy API for FHIR service audiences plus the shared DICOM audience | Official data planes that have no Entra authorization path; long-lived streaming/WebSocket protocols; service-by-service live vectors |
| Google Cloud | ADC OAuth bearer REST on `googleapis.com`; public Discovery documents | APIs with no REST/HTTP transcoding; gRPC streaming and WebSocket transports; service-by-service live vectors |
| Alibaba Cloud | ACS3 OpenAPI; legacy RPC/ROA V2; DataHub `DATAHUB` and OpenSearch V3 `OPENSEARCH` HMAC-SHA1; MaxCompute project/data/Tunnel ODPS V2/V4; classic Function Compute `FC`, current `fcapp.run` Trigger ACS3, and custom-domain Trigger POP HMAC-SHA1; OSS4; SLS v1/v4; MNS/SMQ; and Tablestore OTS v2/v4; credentials-go RAM/OIDC/ECS/AKSK/STS chain | Product-specific signatures or non-HTTP transports outside implemented families; service-by-service live vectors |
| Tencent Cloud | TC3 API 3.0 and COS REST signatures; AKSK/CAM temporary credentials | Legacy/product-specific signatures outside TC3/COS; long-lived streaming/WebSocket protocols; service-by-service live vectors |
| Baidu AI Cloud | BCE auth v1 and v2 signed HTTPS; AKSK/IAM-STS session token | Product-specific legacy signatures or non-HTTP transports outside BCE v1/v2; service-by-service live vectors |

Credential minting/export and caller-supplied signed URLs, SAS, bearer tokens,
API keys, or Authorization headers remain intentionally outside the public MCP
surface. The only credential entrypoints are operator-controlled AKSK,
temporary AKSK/session tokens, or official IAM identity chains.

## Explicit unavailable or credential-bound mappings

- Alibaba Cloud Batch Compute is not an active API surface: Alibaba Cloud's
  discontinuation notice states that the service, console, support, and ticket
  service were completely discontinued on May 15, 2026. Its historical
  `batchcompute.*.aliyuncs.com` signer is therefore not exposed as a callable
  MCP scheme; the replacement E-HPC resource OpenAPI remains addressable
  through the general Alibaba Cloud adapter.
- DataWorks DataService published business APIs are application endpoints, not
  DataWorks resource-management APIs. The current official documentation says
  their AppKey/AppSecret or AppCode cannot be replaced by RAM AKSK. They remain
  outside the user-required AKSK/IAM-only credential surface; DataWorks
  platform resources exposed through Alibaba Cloud OpenAPI remain addressable.

## Completion rule

Do not mark a provider or the overall goal complete from this inventory until:

1. every official public resource API is mapped to an implemented protocol
   family or a documented non-resource/security exclusion;
2. each protocol family has hermetic request/signature/transport vectors;
3. representative control-plane, object/data-plane, upload, download, and
   mutation-gate calls pass against real credentials; and
4. a newly published API can be invoked without adding a resource-specific Go
   handler.
