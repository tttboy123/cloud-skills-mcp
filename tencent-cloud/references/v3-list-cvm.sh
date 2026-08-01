#!/bin/bash
# 查 CVM 列表 (ListInstances) — 用修好的 v3 签名
set -euo pipefail

# 从 macOS Keychain 读凭证
secret_id=$(security find-generic-password -s "tencent-cloud" -a "tccli-secretid" -w 2>/dev/null || echo "")
secret_key=$(security find-generic-password -s "tencent-cloud" -a "tccli-secretkey" -w 2>/dev/null || echo "")
region=$(security find-generic-password -s "tencent-cloud" -a "tccli-region" -w 2>/dev/null || echo "ap-shanghai")

hash_extract() { awk '{print $NF}'; }
sha256_hex() { openssl sha256 -hex | hash_extract; }

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

secret_date=$(printf "%s" "$date" | openssl sha256 -hmac "TC3${secret_key}" | hash_extract)
secret_service=$(printf "%s" "$service" | openssl dgst -sha256 -mac hmac -macopt hexkey:"$secret_date" | hash_extract)
secret_signing=$(printf "%s" "tc3_request" | openssl dgst -sha256 -mac hmac -macopt hexkey:"$secret_service" | hash_extract)
signature=$(printf "%s" "$string_to_sign" | openssl dgst -sha256 -mac hmac -macopt hexkey:"$secret_signing" | hash_extract)

authorization="${algorithm} Credential=${secret_id}/${credential_scope}, SignedHeaders=content-type;host;x-tc-action, Signature=${signature}"

echo "📡 ${action} region=${region}"
echo ""
curl -sS -XPOST "https://${host}" -d "$payload" \
  -H "Authorization: ${authorization}" \
  -H "Content-Type: application/json; charset=utf-8" \
  -H "Host: ${host}" \
  -H "X-TC-Action: ${action}" \
  -H "X-TC-Timestamp: ${timestamp}" \
  -H "X-TC-Version: ${version}" \
  -H "X-TC-Region: ${region}" \
  -H "X-TC-Token: " | python3 -m json.tool
echo ""
echo "curl exit: $?"
