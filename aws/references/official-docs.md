# AWS official references

- AWS API reference index: https://docs.aws.amazon.com/index.html#lang/en_us
- Signature Version 4: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html
- Create a signed request: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html
- SigV4/SigV4a authentication scheme and region-set configuration: https://docs.aws.amazon.com/sdkref/latest/guide/feature-auth-scheme.html
- AWS SDK for Go v2 credential loading: https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html
- Amazon ECR private Registry HTTP authentication: https://docs.aws.amazon.com/AmazonECR/latest/userguide/registry_auth.html
- Amazon ECR Public Registry HTTP authentication and tags-API exclusion: https://docs.aws.amazon.com/AmazonECR/latest/public/public-registry-auth.html
- Amazon ECR private/public classic, FIPS, and dual-stack endpoints: https://docs.aws.amazon.com/general/latest/gr/ecr.html
- Amazon ECR Public IPv4/dual-stack Registry endpoints: https://docs.aws.amazon.com/AmazonECR/latest/public/public-ecr-requests.html
- Amazon ECR GetAuthorizationToken API: https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_GetAuthorizationToken.html
- Amazon ECR Public GetAuthorizationToken API: https://docs.aws.amazon.com/AmazonECRPublic/latest/APIReference/API_GetAuthorizationToken.html
- Amazon ECR Starport S3 layer bucket boundary: https://docs.aws.amazon.com/AmazonECR/latest/userguide/vpc-endpoints.html
- Cloud Control API resource operations: https://docs.aws.amazon.com/cloudcontrolapi/latest/userguide/resource-operations.html
- Resource Explorer: https://docs.aws.amazon.com/resource-explorer/latest/userguide/welcome.html
- S3 GetObject and Range download: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html
- S3 SigV4 signed chunked uploads: https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sigv4-streaming.html
- S3 SigV4 signed trailing headers and official request vector: https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sigv4-streaming-trailers.html
- S3 payload signing modes: https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sigv4-auth-using-authorization-header.html
- S3 upload checksum algorithms: https://docs.aws.amazon.com/AmazonS3/latest/userguide/checking-object-integrity-upload.html
- S3 data integrity and default CRC64NVME: https://docs.aws.amazon.com/AmazonS3/latest/userguide/checking-object-integrity.html
- AWS Java SDK SigV4a chunk/trailer verification vectors: https://github.com/aws/aws-sdk-java-v2/blob/f294ac6c6af6b60cc219b209f87735d1f27614e0/core/auth-crt/src/test/java/software/amazon/awssdk/authcrt/signer/internal/ChunkedEncodingFunctionalTest.java
- AWS C Auth SigV4a chunk algorithms and fixed signature padding: https://github.com/awslabs/aws-c-auth/blob/16c7289432839ca7e49132b68ad5a776e7279394/source/aws_signing.c
- Amazon EventStream wire format: https://smithy.io/2.0/aws/amazon-eventstream.html
- Transcribe SigV4 event-stream signing: https://docs.aws.amazon.com/transcribe/latest/dg/streaming-setting-up.html
- Transcribe StartStreamTranscription WebSocket/API parameters: https://docs.aws.amazon.com/transcribe/latest/APIReference/API_streaming_StartStreamTranscription.html
- Transcribe Medical WebSocket path and parameters: https://docs.aws.amazon.com/transcribe/latest/dg/streaming-medical-conversation.html
- Transcribe Call Analytics WebSocket and first ConfigurationEvent: https://docs.aws.amazon.com/transcribe/latest/dg/tca-start-stream.html
- Transcribe streaming endpoints and quotas: https://docs.aws.amazon.com/general/latest/gr/transcribe.html
- Transcribe AudioEvent one-second maximum and chunk sizing: https://docs.aws.amazon.com/transcribe/latest/APIReference/API_streaming_AudioEvent.html
- AWS IoT MQTT/WSS versus HTTPS publish capabilities, endpoint path, and SigV4 authentication: https://docs.aws.amazon.com/iot/latest/developerguide/protocols.html
- AWS IoT MQTT 3.1.1/5.0 features, persistent-session semantics, supported properties, reason codes, QoS 0/1 bound, and specification differences: https://docs.aws.amazon.com/iot/latest/developerguide/mqtt.html
- AWS IoT IAM query signing and the STS session-token canonical-query exception: https://docs.aws.amazon.com/iot/latest/developerguide/iam-users-groups-roles.html
- AWS IoT MQTT topic filters and wildcard rules: https://docs.aws.amazon.com/iot/latest/developerguide/topics.html
- AWS IoT MQTT packet, subscription, payload, keepalive, and WebSocket limits: https://docs.aws.amazon.com/general/latest/gr/iot-core.html
- AWS IoT Device SDK Python v2 query-signing configuration (`iotdevicegateway`, STS omission): https://github.com/aws/aws-iot-device-sdk-python-v2/blob/main/awsiot/mqtt_connection_builder.py
- AWS C MQTT default `/mqtt` handshake and `mqtt` WebSocket subprotocol: https://github.com/awslabs/aws-c-mqtt/blob/main/include/aws/mqtt/client.h
- Kinesis Video Streams WebRTC signaling WebSocket API index and event lifecycle: https://docs.aws.amazon.com/kinesisvideostreams-webrtc-dg/latest/devguide/kvswebrtc-websocket-apis.html
- Kinesis Video Streams `ConnectAsMaster` endpoint and Channel ARN query contract: https://docs.aws.amazon.com/kinesisvideostreams-webrtc-dg/latest/devguide/ConnectAsMaster.html
- Kinesis Video Streams asynchronous SDP/ICE/status event schema: https://docs.aws.amazon.com/kinesisvideostreams-webrtc-dg/latest/devguide/async-message-reception-api.html
- Kinesis Video Streams signaling quotas, 10,000-byte payload and connection bounds: https://docs.aws.amazon.com/kinesisvideostreams-webrtc-dg/latest/devguide/kvswebrtc-limits.html
- Official Kinesis Video WebRTC JavaScript SDK 299-second SigV4 query signer and STS canonical-query rule: https://github.com/awslabs/amazon-kinesis-video-streams-webrtc-sdk-js/blob/master/src/SigV4RequestSigner.ts
- Official Kinesis Video WebRTC JavaScript SDK role, query and Base64 JSON message implementation: https://github.com/awslabs/amazon-kinesis-video-streams-webrtc-sdk-js/blob/master/src/SignalingClient.ts
- AWS AppSync Events WebSocket endpoints, IAM connection/subscription signing, protocol messages, channel and operation bounds: https://docs.aws.amazon.com/appsync/latest/eventapi/event-api-websocket-protocol.html
- AWS AppSync Events IAM actions and authorization model: https://docs.aws.amazon.com/appsync/latest/eventapi/configure-event-api-auth.html
- AWS AppSync GraphQL realtime WebSocket endpoints, IAM connection/subscription signing, dynamic auth subprotocol, and message lifecycle: https://docs.aws.amazon.com/appsync/latest/devguide/real-time-websocket-client.html
- Amazon Bedrock AgentCore Runtime bidirectional WebSocket path, SigV4 authentication, session header, message limit, and IAM action: https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-get-started-websocket.html
- Amazon Managed Blockchain Ethereum JSON-RPC HTTP/WSS endpoints, `managedblockchain` SigV4 service, and subscription messages: https://docs.aws.amazon.com/managed-blockchain/latest/ethereum-dev/json-rpc-api-examples.html
- Amazon Connect Health ambient documentation HTTP/2/WSS endpoints, 60-second SigV4 presigning, chained EventStream signing, raw audio, normal-close, consent, and S3 output requirements: https://docs.aws.amazon.com/connecthealth/latest/userguide/ambient-documentation.html
- Amazon Connect Health `StartMedicalScribeListeningSession` request headers, event shapes, values, limits, and errors: https://docs.aws.amazon.com/connecthealth/latest/APIReference/API_StartMedicalScribeListeningSession.html
- Amazon Connect Health IAM access level for `StartMedicalScribeListeningSession`: https://docs.aws.amazon.com/service-authorization/latest/reference/list_connecthealth.html
- Amazon IVS Chat control-plane `CreateChatToken` request, limits, capabilities, and response: https://docs.aws.amazon.com/ivs/latest/ChatAPIReference/API_CreateChatToken.html
- Amazon IVS Chat Messaging WebSocket connection and internal token subprotocol: https://docs.aws.amazon.com/ivs/latest/chatmsgapireference/welcome.html
- Amazon IVS Chat Messaging actions: https://docs.aws.amazon.com/ivs/latest/chatmsgapireference/actions.html
- Amazon IVS Chat `SendMessage` fields and 500-code-point bound: https://docs.aws.amazon.com/ivs/latest/chatmsgapireference/actions-sendmessage-publish.html
- Amazon IVS Chat `DeleteMessage` and `DisconnectUser` moderation messages: https://docs.aws.amazon.com/ivs/latest/chatmsgapireference/actions-deletemessage-publish.html and https://docs.aws.amazon.com/ivs/latest/chatmsgapireference/actions-disconnectuser-publish.html
- Amazon IVS Chat subscribed Message/Event shapes and asynchronous errors: https://docs.aws.amazon.com/ivs/latest/chatmsgapireference/actions-message-subscribe.html, https://docs.aws.amazon.com/ivs/latest/chatmsgapireference/actions-event-subscribe.html, and https://docs.aws.amazon.com/ivs/latest/chatmsgapireference/error-messages.html
- Amazon IVS Chat current regional HTTPS/WSS endpoints and messaging quotas: https://docs.aws.amazon.com/general/latest/gr/ivs.html
- Amazon Lex V2 runtime `StartConversation` HTTP/2 bidirectional event stream, request/response syntax, events, and errors: https://docs.aws.amazon.com/lexv2/latest/APIReference/API_runtime_StartConversation.html
- Amazon Lex V2 streaming API concepts, `ConfigurationEvent`, and text/audio/DTMF event ordering: https://docs.aws.amazon.com/lexv2/latest/dg/streaming-API.html
- Amazon Lex V2 `ConfigurationEvent`, `TextInputEvent`, and response event shapes: https://docs.aws.amazon.com/lexv2/latest/APIReference/API_runtime_ConfigurationEvent.html, https://docs.aws.amazon.com/lexv2/latest/APIReference/API_runtime_TextInputEvent.html, and https://docs.aws.amazon.com/lexv2/latest/APIReference/API_runtime_StartConversationResponseEventStream.html
- Amazon Lex V2 runtime endpoints and service signing name (`runtime-v2-lex`, `lex`): https://docs.aws.amazon.com/general/latest/gr/lex.html
- Amazon Chime SDK Messaging WebSocket overview and step order (IAM policy, retrieve endpoint, connect, prefetch, process events): https://docs.aws.amazon.com/chime-sdk/latest/dg/websockets.html
- Amazon Chime SDK Messaging connect API, SigV4 `GET /connect` query parameters (`X-Amz-Algorithm`, `X-Amz-Credential`, `X-Amz-Date`, `X-Amz-Expires`, `X-Amz-SignedHeaders`, `sessionId`, `userArn`), and the `chime` signing service: https://docs.aws.amazon.com/chime-sdk/latest/dg/connect-api.html
- Amazon Chime SDK Messaging `GetMessagingSessionEndpoint` request/response and `MessagingSessionEndpoint` URL shape: https://docs.aws.amazon.com/chime-sdk/latest/APIReference/API_GetMessagingSessionEndpoint.html and https://docs.aws.amazon.com/chime-sdk/latest/APIReference/API_messaging-chime_MessagingSessionEndpoint.html
- Amazon Chime SDK Messaging WebSocket message structures: `Headers` (`x-amz-chime-event-type`, `x-amz-chime-message-type`, `x-amz-chime-event-reason`), `Payload` JSON string, event types and payload formats: https://docs.aws.amazon.com/chime-sdk/latest/dg/message-structures.html
- Amazon Chime SDK Messaging `chime:Connect` and `chime:GetMessagingSessionEndpoint` IAM actions on AppInstanceUser resources: https://docs.aws.amazon.com/chime-sdk/latest/dg/define-iam-policy.html
- Amazon Chime SDK Messaging WebSocket close codes and reconnect guidance: https://docs.aws.amazon.com/chime-sdk/latest/dg/handle-disconnects.html
- Official Chime SDK JS messaging session implementation that retrieves the endpoint and SigV4-presigns `/connect` with service `chime`: https://github.com/aws/amazon-chime-sdk-js/tree/main/src/messagingsession
- MSK Kafka client authentication (IAM, SASL/SCRAM, mTLS) and the Kafka binary client protocol: https://docs.aws.amazon.com/msk/latest/developerguide/client-auth.html
- ElastiCache for Redis/ValKey and MemoryDB Redis AUTH token or IAM authentication through a RESP client: https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/auth.html
- MediaConnect source ports (Zixi push 2088, VPC 2090–2099, SRT, RTP) as the media transmission plane: https://docs.aws.amazon.com/mediaconnect/latest/ug/source-ports.html
- Kinesis Video Streams RTMP media ingest and HLS playback as the media plane, separate from the IAM-signed WebRTC signaling implemented here: https://docs.aws.amazon.com/kinesisvideostreams/latest/dg/what-is-kinesis-video.html and https://docs.aws.amazon.com/kinesisvideostreams/latest/dg/how-rtmp.html
- Amazon MQ ActiveMQ broker access protocols (AMQP, OpenWire, STOMP, MQTT, WebSocket) and RabbitMQ AMQP 0-9-1 with broker user/LDAP authentication as non-resource client line protocols: https://docs.aws.amazon.com/amazon-mq/latest/developer-guide/security-authentication-authorization.html
- MediaLive input classes and supported containers/codecs (RTMP push/pull, RTP, transport stream) as the media plane: https://docs.aws.amazon.com/medialive/latest/ug/inputs-supported-containers-and-codecs.html
- Amazon Connect `StartChatContact` (`PUT /contact/chat`, `connect` SigV4 signing name) and its ParticipantToken/ContactId response: https://docs.aws.amazon.com/connect/latest/APIReference/API_StartChatContact.html
- Connect Participant `CreateParticipantConnection` (`POST /participant/connection`, `X-Amz-Bearer` participant token, 100-second WebSocket URL expiry) with the official `{"topic":"aws/subscribe","content":{"topics":["aws/chat"]}}` subscription frame: https://docs.aws.amazon.com/connect-participant/latest/APIReference/API_CreateParticipantConnection.html
- Amazon Connect chat participant WebSocket topic framing (subscribe/heartbeat/ping and `aws/chat` events) in the open-source chat JS SDK: https://github.com/amazon-connect/amazon-connect-chatjs
- Connect Participant `SendMessage` (`POST /participant/message` with `X-Amz-Bearer` connection token, `Content`/`ContentType`/`ClientToken`): https://docs.aws.amazon.com/connect-participant/latest/APIReference/API_SendMessage.html
- Amazon IVS real-time Stage publish/subscribe over WebRTC/RTMPS as the media plane: https://docs.aws.amazon.com/ivs/latest/RealTimeUserGuide/getting-started-pub-sub.html

`auth_scheme=ecr` signs the provider-fixed private or public GetAuthorizationToken request internally, validates the base64 `AWS:password` material without exposing it, applies the documented Basic or Bearer Registry header, binds endpoint/service/region/path exactly, and follows private layer redirects only to the exact regional Starport S3 bucket with Registry Authorization removed.

The universal adapter signs exact HTTP requests with the official AWS SDK Go v2 SigV4 signer or its pure-Go implementation of the documented SigV4a NIST KDF and ECDSA P-256 process. For S3 SigV4 and SigV4a `PutObject` and `UploadPart`, `payload_mode=aws-chunked` implements the documented 64 KiB framing, seed signature and chained chunk signatures. SigV4a chunk signatures use the official 144-character padded DER-ECDSA wire format. `payload_mode=aws-chunked-trailer` additionally computes CRC32, CRC32C, CRC64NVME, SHA-1, or SHA-256 while streaming and signs the checksum trailer; callers cannot provide the checksum value. For finite or bidirectional HTTP/2 SigV4 event streams, `payload_mode=aws-eventstream` validates consecutive unsigned EventStream frames, optionally paces complete logical frames, and uses the public AWS SDK StreamSigner to add signing envelopes and a terminal frame while concurrently validating response lengths and CRCs before atomic output. `auth_scheme=sigv4-ws` signs a finite raw text/binary WebSocket handshake internally and forces the session through mutation approval. `auth_scheme=connect-health-ws` keeps the 60-second Medical Scribe URL internal, chains signed configuration/raw-audio/session-control EventStream frames, requires write approval, and waits for a normal close before atomic transcript output. `auth_scheme=transcribe-ws` keeps the five-minute presigned upgrade URL internal, covers standard, Medical, and Call Analytics WSS paths, and chains signed double-EventStream audio/configuration frames. `auth_scheme=iot-mqtt-ws` implements bounded read-only and mutation-only MQTT 3.1.1/5.0 clients, including AWS IoT's special STS-token presign rule, required `mqtt` subprotocol negotiation, clean or persistent sessions, subscribe/unsubscribe, QoS 0/1 publish/PUBACK, retained messages and Last Will, supported MQTT 5 application and session properties, broker capability/reason-code validation, server keepalive, and atomic Base64 NDJSON output. `auth_scheme=appsync-event-ws` and `appsync-graphql-ws` implement their distinct IAM-authenticated finite realtime subscription protocols while keeping connection and subscription authorization internal. All modes resolve credentials through the AWS SDK chain. It does not execute AWS CLI or expose presigning.

`auth_scheme=kinesisvideo-signaling-ws` implements the separate Kinesis Video
WebRTC signaling query-presigned WSS contract. It validates Master/Viewer role
and Channel ARN scope, signs the provider endpoint for 299 seconds with the STS
token inside the canonical query, Base64-encodes only bounded SDP/ICE JSON,
paces messages to provider quotas, correlates asynchronous status errors, and
atomically decodes supported events without exposing the signed URL.

`auth_scheme=chime-messaging-ws` implements the Amazon Chime SDK Messaging
WebSocket event-stream contract. The server signs the fixed
`GET https://messaging-chime.<region>.amazonaws.com/endpoints/messaging-session`
request with the AWS SDK chain and the documented `chime` signing name, rejects
any returned endpoint other than the official `wss://data-messaging.chime.aws`
host, then SigV4-presigns `GET /connect` with a bounded `X-Amz-Expires`, the
caller's official AppInstanceUser ARN as `userArn`, a unique `sessionId`, an
optional `prefetch-on=connect`, and the STS token only inside the canonical
query (mirroring the official Chime JS SDK `DefaultSigV4` process and verified
against the AWS SDK Go v2 presigner). Events are validated against the
documented `x-amz-chime-event-type` table and `STANDARD|CONTROL|SYSTEM` message
types, `Payload` JSON strings are decoded and bounded, and atomic NDJSON is
published without exposing the signed URL, STS token, or SigV4 material.
