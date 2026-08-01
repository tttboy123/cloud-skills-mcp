#!/bin/bash
# 腾讯云 API 签名 v3 裸 bash + openssl + curl 实现
# 原始版本: https://cloud.tencent.com/document/product/213/30654
# 修复:
#   1. macOS BSD date 不支持 -d, 改用 -r @timestamp
#   2. macOS LibreSSL 不输出 "(stdin)= " 前缀, 用 awk '{print $NF}' 拿最后字段
#   3. bash echo 默认不解释 \n, canonical_request 改用 printf 一次性构造
#   4. canonical_headers 内部用真换行 (printf), 不用字面 \n

set -euo pipefail

# 从 macOS Keychain 读凭证
if command -v security >/dev/null 2>&1; then
  secret_id=$(security find-generic-password -s "tencent-cloud" -a "tccli-secretid" -w 2>/dev/null || echo "")
  secret_key=$(security find-generic-password -s "tencent-cloud" -a "tccli-secretkey" -w 2>/dev/null || echo "")
fi
secret_id="${secret_id:-${TENCENTCLOUD_SECRET_ID:-}}"
secret_key="${secret_key:-${TENCENTCLOUD_SECRET_KEY:-}}"

if [[ -z "${secret_id}" || -z "${secret_key}" ]]; then
  echo "❌ AKSK 缺失, 请先跑 setup-keychain.sh" >&2
  exit 1
fi

echo "🔑 secret_id: ${secret_id:0:8}...${secret_id: -4}"
echo ""

# 通用工具
hash_extract() { awk '{print $NF}'; }
sha256_hex() { openssl sha256 -hex | hash_extract; }

# ============= 参数 =============
service="cvm"
host="cvm.tencentcloudapi.com"
region=""
action="DescribeRegions"
version="2017-03-12"
algorithm="TC3-HMAC-SHA256"
timestamp=$(date +%s)
date=$(date -u -r "$timestamp" +"%Y-%m-%d" 2>/dev/null || date -u -d "@$timestamp" +"%Y-%m-%d")
payload='{}'

action_lower=$(echo "$action" | awk '{print tolower($0)}')

echo "📅 UTC date: ${date}"
echo "🕐 timestamp: ${timestamp}"
echo ""

# ============= 步骤 1：拼接规范请求串（用 printf 一次性构造） =============
# CanonicalRequest 结构:
#   HTTPRequestMethod\n
#   CanonicalURI\n
#   CanonicalQueryString\n
#   CanonicalHeaders\n   <- 末尾自带 \n
#   SignedHeaders\n
#   HashedRequestPayload
hashed_request_payload=$(printf "%s" "$payload" | sha256_hex)

# 关键: 用 printf 一次性构造, 所有 \n 都会被解释成真换行
canonical_request=$(printf "%s\n%s\n%s\ncontent-type:application/json; charset=utf-8\nhost:%s\nx-tc-action:%s\n\n%s\n%s" \
  "POST" \
  "/" \
  "" \
  "$host" \
  "$action_lower" \
  "content-type;host;x-tc-action" \
  "$hashed_request_payload")

echo "=== canonical_request (真换行) ==="
printf "%s" "$canonical_request" | od -c | head -12
echo ""

# ============= 步骤 2：拼接待签名字符串 =============
credential_scope="${date}/${service}/tc3_request"
hashed_canonical_request=$(printf "%s" "$canonical_request" | sha256_hex)
string_to_sign=$(printf "%s\n%s\n%s\n%s" \
  "$algorithm" \
  "$timestamp" \
  "$credential_scope" \
  "$hashed_canonical_request")

echo "=== string_to_sign (真换行) ==="
printf "%s" "$string_to_sign" | od -c | head -8
echo ""

# ============= 步骤 3：计算签名 =============
secret_date=$(printf "%s" "$date" | openssl sha256 -hmac "TC3${secret_key}" | hash_extract)
echo "secret_date:    $secret_date"

secret_service=$(printf "%s" "$service" | openssl dgst -sha256 -mac hmac -macopt hexkey:"$secret_date" | hash_extract)
echo "secret_service: $secret_service"

secret_signing=$(printf "%s" "tc3_request" | openssl dgst -sha256 -mac hmac -macopt hexkey:"$secret_service" | hash_extract)
echo "secret_signing: $secret_signing"

signature=$(printf "%s" "$string_to_sign" | openssl dgst -sha256 -mac hmac -macopt hexkey:"$secret_signing" | hash_extract)
echo "signature:      $signature"

# ============= 步骤 4：拼接 Authorization =============
authorization="${algorithm} Credential=${secret_id}/${credential_scope}, SignedHeaders=content-type;host;x-tc-action, Signature=${signature}"

echo ""
echo "=== authorization ==="
echo "$authorization"
echo ""

# ============= 步骤 5：发起请求 =============
echo "📡 请求: POST https://${host}  Action=${action}"
echo ""
curl -sS -XPOST "https://${host}" -d "$payload" \
  -H "Authorization: ${authorization}" \
  -H "Content-Type: application/json; charset=utf-8" \
  -H "Host: ${host}" \
  -H "X-TC-Action: ${action}" \
  -H "X-TC-Timestamp: ${timestamp}" \
  -H "X-TC-Version: ${version}" \
  -H "X-TC-Region: ${region}" \
  -H "X-TC-Token: "
echo ""
echo "curl exit: $?"
