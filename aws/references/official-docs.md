# AWS official references

- AWS API reference index: https://docs.aws.amazon.com/index.html#lang/en_us
- Signature Version 4: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html
- Create a signed request: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html
- SigV4/SigV4a authentication scheme and region-set configuration: https://docs.aws.amazon.com/sdkref/latest/guide/feature-auth-scheme.html
- AWS SDK for Go v2 credential loading: https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html
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
- AWS IoT IAM query signing and the STS session-token canonical-query exception: https://docs.aws.amazon.com/iot/latest/developerguide/iam-users-groups-roles.html
- AWS IoT MQTT topic filters and wildcard rules: https://docs.aws.amazon.com/iot/latest/developerguide/topics.html
- AWS IoT MQTT packet, subscription, payload, keepalive, and WebSocket limits: https://docs.aws.amazon.com/general/latest/gr/iot-core.html
- AWS IoT Device SDK Python v2 query-signing configuration (`iotdevicegateway`, STS omission): https://github.com/aws/aws-iot-device-sdk-python-v2/blob/main/awsiot/mqtt_connection_builder.py
- AWS C MQTT default `/mqtt` handshake and `mqtt` WebSocket subprotocol: https://github.com/awslabs/aws-c-mqtt/blob/main/include/aws/mqtt/client.h
- AWS AppSync Events WebSocket endpoints, IAM connection/subscription signing, protocol messages, channel and operation bounds: https://docs.aws.amazon.com/appsync/latest/eventapi/event-api-websocket-protocol.html
- AWS AppSync Events IAM actions and authorization model: https://docs.aws.amazon.com/appsync/latest/eventapi/configure-event-api-auth.html
- AWS AppSync GraphQL realtime WebSocket endpoints, IAM connection/subscription signing, dynamic auth subprotocol, and message lifecycle: https://docs.aws.amazon.com/appsync/latest/devguide/real-time-websocket-client.html

The universal adapter signs exact HTTP requests with the official AWS SDK Go v2 SigV4 signer or its pure-Go implementation of the documented SigV4a NIST KDF and ECDSA P-256 process. For S3 SigV4 and SigV4a `PutObject` and `UploadPart`, `payload_mode=aws-chunked` implements the documented 64 KiB framing, seed signature and chained chunk signatures. SigV4a chunk signatures use the official 144-character padded DER-ECDSA wire format. `payload_mode=aws-chunked-trailer` additionally computes CRC32, CRC32C, CRC64NVME, SHA-1, or SHA-256 while streaming and signs the checksum trailer; callers cannot provide the checksum value. For finite SigV4 HTTP event streams, `payload_mode=aws-eventstream` validates consecutive unsigned EventStream frames and uses the public AWS SDK StreamSigner to add signing envelopes and a terminal frame. `auth_scheme=transcribe-ws` keeps the five-minute presigned upgrade URL internal, covers standard, Medical, and Call Analytics WSS paths, and chains signed double-EventStream audio/configuration frames. `auth_scheme=iot-mqtt-ws` implements a finite clean-session MQTT 3.1.1 subscriber, including AWS IoT's special STS-token presign rule, `mqtt` subprotocol negotiation, QoS 1 acknowledgements, and atomic Base64 NDJSON output. `auth_scheme=appsync-event-ws` and `appsync-graphql-ws` implement their distinct IAM-authenticated finite realtime subscription protocols while keeping connection and subscription authorization internal. All modes resolve credentials through the AWS SDK chain. It does not execute AWS CLI or expose presigning.
