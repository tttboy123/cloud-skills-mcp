# Alibaba Cloud official references

- V3 OpenAPI HTTP structure and ACS3 signature: https://help.aliyun.com/zh/sdk/product-overview/v3-request-structure-and-signature
- Legacy RPC V2 request structure, query/form parameter positions, HMAC-SHA1 algorithm, and fixed signature vector: https://www.alibabacloud.com/help/en/sdk/product-overview/rpc-mechanism
- Current BaaS HMAC-SHA1 common parameters and NAS STS `SecurityToken` parameter: https://www.alibabacloud.com/help/en/blockchain-as-a-service/latest/common-parameters and https://www.alibabacloud.com/help/en/nas/common-parameters
- OSS V4 Authorization signature: https://help.aliyun.com/en/oss/developer-reference/recommend-to-use-signature-version-4
- Simple Log Service v1 request signature: https://www.alibabacloud.com/help/en/sls/developer-reference/request-signatures
- Simple Log Service v1/v4 authentication selection and region requirement: https://www.alibabacloud.com/help/en/sls/developer-reference/initializing-the-sls-python-sdk
- Official SLS Go v1/v4 signer vectors: https://github.com/aliyun/aliyun-log-go-sdk/blob/master/signature_v4_test.go
- Simple Message Queue request protocol and MNS HMAC-SHA1 signature: https://www.alibabacloud.com/help/en/mns/developer-reference/request-protocol-description
- Official MNS Go signer implementation: https://github.com/aliyun/aliyun-mns-go-sdk/blob/master/credential.go
- Tablestore direct HTTP authentication/encoding requirement: https://www.alibabacloud.com/help/en/tablestore/support/can-i-use-http-methods-to-call-tablestore-api-operations
- Tablestore data-management API and published Protocol Buffer definitions: https://www.alibabacloud.com/help/en/tablestore/developer-reference/operation-summary-of-tablestore-api/ and https://www.alibabacloud.com/help/en/tablestore/developer-reference/table-store-protocolbuffer-message-definitions
- Tablestore V4 derived-key model: https://www.alibabacloud.com/help/en/tablestore/user-key-security-for-tablestore
- Official Tablestore Go v2/v4 signer and fixed vectors: https://github.com/aliyun/aliyun-tablestore-go-sdk/blob/master/tablestore/ots_header.go and https://github.com/aliyun/aliyun-tablestore-go-sdk/blob/master/tablestore/ots_header_test.go
- Official credentials-go provider chain: https://github.com/aliyun/credentials-go
- OpenAPI Explorer: https://api.aliyun.com/
- OpenAPI MCP Server guide: https://help.aliyun.com/en/openapi/user-guide/openapi-mcp-server-guide
- OSS GetObject and Range download: https://www.alibabacloud.com/help/en/oss/developer-reference/getobject

The universal adapter signs current general OpenAPI requests with ACS3, product APIs that still document legacy RPC V2 with HMAC-SHA1, OSS data-plane requests with OSS4, SLS data-plane requests with v1 or v4, MNS requests with its service-specific HMAC-SHA1 protocol, and Tablestore protobuf data-plane requests with OTS v2 or v4. It never executes Alibaba Cloud CLI.
