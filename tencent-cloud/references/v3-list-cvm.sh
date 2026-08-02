#!/bin/bash
# 查 CVM 列表 (ListInstances) — 用修好的 v3 签名
set -euo pipefail

REFERENCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../scripts/_creds.sh
source "${REFERENCE_DIR}/../scripts/_creds.sh"
# shellcheck source=_tc3-safe.sh
source "${REFERENCE_DIR}/_tc3-safe.sh"
secret_id="${TENCENTCLOUD_SECRET_ID}"
secret_key="${TENCENTCLOUD_SECRET_KEY}"
region="${TENCENTCLOUD_REGION:-ap-shanghai}"
unset TENCENTCLOUD_SECRET_ID TENCENTCLOUD_SECRET_KEY

service="cvm"
host="cvm.tencentcloudapi.com"
version="2017-03-12"
algorithm="TC3-HMAC-SHA256"
timestamp=$(date +%s)
date=$(date -u -r "$timestamp" +"%Y-%m-%d" 2>/dev/null || date -u -d "@$timestamp" +"%Y-%m-%d")
action="DescribeInstances"
action_lower="describeinstances"
# DescribeInstances 返回全 region, 不接受 Region 顶层字段
# 用 Offset+Limit 分页, 默认 20/页
payload='{"Limit":50,"Offset":0}'

echo "🔑 secret_id: ${secret_id:0:8}...${secret_id: -4}"
echo "🌏 region: ${region}"
echo ""

hashed_request_payload=$(printf "%s" "$payload" | sha256_hex)

canonical_request=$(printf "%s\n%s\n%s\ncontent-type:application/json; charset=utf-8\nhost:%s\nx-tc-action:%s\n\n%s\n%s" \
  "POST" \
  "/" \
  "" \
  "$host" \
  "$action_lower" \
  "content-type;host;x-tc-action" \
  "$hashed_request_payload")

credential_scope="${date}/${service}/tc3_request"
hashed_canonical_request=$(printf "%s" "$canonical_request" | sha256_hex)
string_to_sign=$(printf "%s\n%s\n%s\n%s" \
  "$algorithm" \
  "$timestamp" \
  "$credential_scope" \
  "$hashed_canonical_request")

secret_date=$(hmac_sha256_hex "TC3${secret_key}" "${date}")
unset secret_key TENCENTCLOUD_SECRET_KEY
secret_service=$(hmac_sha256_hex "${secret_date}" "${service}" hex)
secret_signing=$(hmac_sha256_hex "${secret_service}" "tc3_request" hex)
signature=$(hmac_sha256_hex "${secret_signing}" "${string_to_sign}" hex)

authorization="${algorithm} Credential=${secret_id}/${credential_scope}, SignedHeaders=content-type;host;x-tc-action, Signature=${signature}"
headers_file=$(mktemp)
cleanup() {
  rm -f "${headers_file}"
  unset authorization secret_id secret_date secret_service secret_signing signature
  unset TENCENTCLOUD_SECRET_ID TENCENTCLOUD_SECRET_KEY
}
trap cleanup EXIT
write_tc3_headers "${headers_file}" "${authorization}" "${host}" "${action}" "${timestamp}" "${version}" "${region}"
unset authorization secret_id secret_date secret_service secret_signing signature
unset TENCENTCLOUD_SECRET_ID TENCENTCLOUD_SECRET_KEY

echo "📡 ${action} region=${region}"
echo ""
curl -sS -XPOST "https://${host}" -d "$payload" -H "@${headers_file}" | python3 -m json.tool
echo ""
echo "curl exit: $?"
