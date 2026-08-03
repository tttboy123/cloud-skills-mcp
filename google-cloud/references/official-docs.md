# Google Cloud official references

- Authentication overview: https://docs.cloud.google.com/docs/authentication
- Authenticate REST requests: https://docs.cloud.google.com/docs/authentication/rest
- Application Default Credentials: https://docs.cloud.google.com/docs/authentication/application-default-credentials
- Google API Discovery Service: https://developers.google.com/discovery/v1/using
- Google Cloud API reference index: https://cloud.google.com/apis/docs/overview
- System parameters and gRPC HTTP metadata: https://docs.cloud.google.com/apis/docs/system-parameters
- gRPC protocol over HTTP/2: https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md
- Official ProtoJSON mapping and 64-bit integer rules: https://protobuf.dev/programming-guides/json/
- Official protobuf-go ProtoJSON API: https://pkg.go.dev/google.golang.org/protobuf/encoding/protojson
- Official protobuf-go FileDescriptorSet registry API: https://pkg.go.dev/google.golang.org/protobuf/reflect/protodesc
- Official protobuf-go dynamic message/type API: https://pkg.go.dev/google.golang.org/protobuf/types/dynamicpb
- Official Google API protobuf definitions: https://github.com/googleapis/googleapis
- Speech-to-Text streaming overview: https://docs.cloud.google.com/speech-to-text/docs/v1/transcribe-streaming-audio
- Speech-to-Text v2 StreamingRecognize RPC: https://cloud.google.com/speech-to-text/v2/docs/reference/rpc/google.cloud.speech.v2
- Firestore Listen gRPC-only observation RPC: https://cloud.google.com/firestore/docs/reference/rpc/google.firestore.v1#google.firestore.v1.Firestore.Listen
- Cloud Logging TailLogEntries server stream: https://cloud.google.com/logging/docs/reference/v2/rpc/google.logging.v2#google.logging.v2.LoggingServiceV2.TailLogEntries
- Pub/Sub StreamingPull persistent bidirectional connection and acknowledgement workflow: https://cloud.google.com/pubsub/docs/pull
- Gemini Live API stateful WebSocket message reference: https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/multimodal-live
- Gemini Live API WebSocket and ADC proxy tutorial: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/live-api/get-started-websocket
- Vertex AI Live API session management, GoAway, and session resumption: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/live-api/start-manage-session
- Live API session-resumption buffering and replay best practices: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/live-api/best-practices
- Official Go Gen AI SDK Vertex Live endpoint, ADC Bearer header, setup ordering, and Bidi path: https://github.com/googleapis/go-genai/blob/85ce5bec1c6c460ce2a4fbbdf440d937ee4518d2/live.go
- Official Go Gen AI SDK `FunctionCall`, `FunctionResponse`, `SessionResumptionConfig`, `LiveServerSessionResumptionUpdate`, and `LiveServerGoAway` wire types: https://github.com/googleapis/go-genai/blob/85ce5bec1c6c460ce2a4fbbdf440d937ee4518d2/types.go
- Cloud Asset Inventory: https://docs.cloud.google.com/asset-inventory/docs/asset-inventory-overview
- Google Cloud MCP overview: https://docs.cloud.google.com/mcp/overview
- Cloud Storage objects.get media and Range download: https://docs.cloud.google.com/storage/docs/json_api/v1/objects/get
- Firebase Realtime Database REST streaming and SSE events: https://firebase.google.com/docs/database/rest/retrieve-data#section-rest-streaming
- Firebase Realtime Database service-account OAuth scopes and Bearer authentication: https://firebase.google.com/docs/database/rest/auth
- Firebase Realtime Database official location and URL formats: https://firebase.google.com/docs/database/locations
- Firebase Realtime Database response, listener, and query limits: https://firebase.google.com/docs/database/usage/limits

The adapter requests a token internally and sends it only to validated `googleapis.com` or Firebase Realtime Database hosts. Public discovery documents are fetched without credentials. For `auth_scheme=firebase-sse`, it requests the documented `firebase.database` and `userinfo.email` scopes, keeps the Bearer token in the Authorization header, accepts only official database URL formats, manually validates required 307 redirects without permitting path/query changes, and atomically publishes bounded sanitized SSE events. For `auth_scheme=grpc`, it sends and validates the official five-byte gRPC record framing over HTTP/2, requires a successful terminal `grpc-status` when the provider ends the call, and never accepts caller-supplied authentication or gRPC protocol-control headers. Raw mode preserves framed protobuf. `payload_mode=protobuf-json` loads a bounded local FileDescriptorSet, resolves the exact URL service/method, converts bounded ProtoJSON request objects with dynamic protobuf types, rejects incomplete response schemas, and atomically publishes NDJSON; descriptor bytes never leave the process. Descriptor-declared server streams require an explicit 1–256 response-message limit and 1–300 second timeout, so Firestore Listen and Logging TailLogEntries cannot wait forever. Exact official observation paths are read-only, while Pub/Sub StreamingPull stays mutation-gated because the bidirectional request carries acknowledgements and ack-deadline changes. For `auth_scheme=vertex-live-ws`, it uses only the official global, regional, or `us|eu` multi-region aiplatform host and Bidi path, puts the ADC Bearer token only in the Upgrade header, enforces setup/setupComplete ordering, bounds messages and time, matches tool responses to provider call IDs, and can transparently reconnect using an internal-only handle plus acknowledged-message replay. It strips `newHandle`, validates server JSON, and atomically publishes NDJSON.
