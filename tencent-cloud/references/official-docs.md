# Tencent Cloud official references

- Tencent Cloud API reference: https://cloud.tencent.com/document/api
- API 3.0 common parameters: https://intl.cloud.tencent.com/document/product/1005/34677
- TC3-HMAC-SHA256 signature: https://intl.cloud.tencent.com/document/product/627/64494
- API 3.0 Signature v1 HmacSHA1/HmacSHA256 canonical request and fixed example: https://cloud.tencent.com/document/api/583/17239
- API 3.0 request structure documenting form-urlencoded POST as v1-only and JSON/multipart as TC3: https://cloud.tencent.com/document/product/1278/46713
- COS REST request signature: https://intl.cloud.tencent.com/document/product/436/7778
- CAM resource role and temporary credentials: https://cloud.tencent.com/document/product/598/85616
- Tencent Cloud API Explorer: https://console.cloud.tencent.com/api/explorer
- COS GET Object and Range download: https://intl.cloud.tencent.com/document/product/436/7753

The universal adapter signs API 3.0 requests with recommended TC3 or the still-documented v1 HmacSHA1/HmacSHA256 query/form protocol, and COS data-plane requests with the COS REST signature. It never executes TCCLI.
