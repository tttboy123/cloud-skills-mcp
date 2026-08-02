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
- S3 payload signing modes and trailing headers: https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sigv4-auth-using-authorization-header.html
- Amazon EventStream wire format: https://smithy.io/2.0/aws/amazon-eventstream.html
- Transcribe SigV4 event-stream signing: https://docs.aws.amazon.com/transcribe/latest/dg/streaming-setting-up.html

The universal adapter signs exact HTTP requests with the official AWS SDK Go v2 SigV4 signer or its pure-Go implementation of the documented SigV4a NIST KDF and ECDSA P-256 header-signing process. For S3 SigV4 `PutObject` and `UploadPart`, `payload_mode=aws-chunked` implements the documented 64 KiB framing, seed signature and chained chunk signatures. For finite SigV4 HTTP event streams, `payload_mode=aws-eventstream` validates consecutive unsigned EventStream frames and uses the public AWS SDK StreamSigner to add signing envelopes and a terminal frame. All modes resolve credentials through the AWS SDK chain. It does not execute AWS CLI or expose presigning.
