# Google Cloud official references

- Authentication overview: https://docs.cloud.google.com/docs/authentication
- Authenticate REST requests: https://docs.cloud.google.com/docs/authentication/rest
- Application Default Credentials: https://docs.cloud.google.com/docs/authentication/application-default-credentials
- Google API Discovery Service: https://developers.google.com/discovery/v1/using
- Google Cloud API reference index: https://cloud.google.com/apis/docs/overview
- System parameters and gRPC HTTP metadata: https://docs.cloud.google.com/apis/docs/system-parameters
- gRPC protocol over HTTP/2: https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md
- Speech-to-Text streaming overview: https://docs.cloud.google.com/speech-to-text/docs/v1/transcribe-streaming-audio
- Speech-to-Text v2 StreamingRecognize RPC: https://cloud.google.com/speech-to-text/v2/docs/reference/rpc/google.cloud.speech.v2
- Cloud Asset Inventory: https://docs.cloud.google.com/asset-inventory/docs/asset-inventory-overview
- Google Cloud MCP overview: https://docs.cloud.google.com/mcp/overview
- Cloud Storage objects.get media and Range download: https://docs.cloud.google.com/storage/docs/json_api/v1/objects/get

The adapter requests a token internally and sends it only to validated `googleapis.com` hosts. Public discovery documents are fetched without credentials. For `auth_scheme=grpc`, it sends and validates the official five-byte gRPC record framing over HTTP/2, requires `grpc-status=0`, and never accepts caller-supplied authentication or gRPC protocol-control headers.
