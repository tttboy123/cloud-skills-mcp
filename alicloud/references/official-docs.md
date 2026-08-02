# Alibaba Cloud official references

- V3 OpenAPI HTTP structure and ACS3 signature: https://help.aliyun.com/zh/sdk/product-overview/v3-request-structure-and-signature
- Legacy RPC V2 request structure, query/form parameter positions, HMAC-SHA1 algorithm, and fixed signature vector: https://www.alibabacloud.com/help/en/sdk/product-overview/rpc-mechanism
- Legacy ROA V2 header/resource/body HMAC-SHA1 signature mechanism: https://www.alibabacloud.com/help/en/sdk/product-overview/roa-mechanism
- Current BaaS HMAC-SHA1 common parameters and NAS STS `SecurityToken` parameter: https://www.alibabacloud.com/help/en/blockchain-as-a-service/latest/common-parameters and https://www.alibabacloud.com/help/en/nas/common-parameters
- PDS AccessKey ROA endpoint and STS `x-acs-security-token` canonical-header behavior: https://www.alibabacloud.com/help/en/pds/drive-and-photo-service-dev/user-guide/call-api-operations-by-using-an-accesskey-pair
- DataHub resource operations, `DATAHUB` HMAC-SHA1 signature, and STS header: https://www.alibabacloud.com/help/en/datahub/developer-reference/nerbcz
- OpenSearch V3 search/push `OPENSEARCH` HMAC-SHA1 signature: https://www.alibabacloud.com/help/en/open-search/high-performance-searchedition/signature-method-of-opensearch-api-v3
- OpenSearch SDK STS `X-Opensearch-Security-Token` behavior: https://www.alibabacloud.com/help/en/open-search/high-performance-searchedition/sample-code-for-the-python-client
- MaxCompute current public, VPC, interconnected service and Tunnel endpoints: https://www.alibabacloud.com/help/en/maxcompute/user-guide/endpoints
- MaxCompute `MaxCompute/2022-01-04` control-plane OpenAPI overview: https://www.alibabacloud.com/help/en/maxcompute/user-guide/api-maxcompute-2022-01-04-overview
- Official PyODPS ODPS V2/V4 canonical request, derived-key, Authorization, and STS implementation pinned at the audited revision: https://github.com/aliyun/aliyun-odps-python-sdk/blob/558462b8d61b43c73016837f32c68b2ddfad2cdf/odps/accounts.py
- Official PyODPS release history documenting V4 signing as the default since 0.12.4: https://github.com/aliyun/aliyun-odps-python-sdk/releases
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

The universal adapter signs current general OpenAPI requests with ACS3, product APIs that still document legacy RPC or ROA V2 with HMAC-SHA1, MaxCompute project/data/Tunnel requests with ODPS V2 or V4, OSS data-plane requests with OSS4, SLS data-plane requests with v1 or v4, MNS requests with its service-specific HMAC-SHA1 protocol, and Tablestore protobuf data-plane requests with OTS v2 or v4. It never executes Alibaba Cloud CLI.
