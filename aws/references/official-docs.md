# AWS official references

- AWS API reference index: https://docs.aws.amazon.com/index.html#lang/en_us
- Signature Version 4: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html
- Create a signed request: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html
- SigV4/SigV4a authentication scheme and region-set configuration: https://docs.aws.amazon.com/sdkref/latest/guide/feature-auth-scheme.html
- AWS SDK for Go v2 credential loading: https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html
- Cloud Control API resource operations: https://docs.aws.amazon.com/cloudcontrolapi/latest/userguide/resource-operations.html
- Resource Explorer: https://docs.aws.amazon.com/resource-explorer/latest/userguide/welcome.html
- S3 GetObject and Range download: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html

The universal adapter signs exact HTTP requests with the official AWS SDK Go v2 SigV4 signer or its pure-Go implementation of the documented SigV4a NIST KDF and ECDSA P-256 header-signing process. Both resolve credentials through the AWS SDK chain. It does not execute AWS CLI or expose presigning.
