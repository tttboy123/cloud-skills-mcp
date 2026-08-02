#!/bin/bash
# 腾讯云 API 签名 v3 裸 bash + openssl + curl 实现
# 原始版本: https://cloud.tencent.com/document/product/213/30654
# 修复:
#   1. macOS BSD date 不支持 -d, 改用 -r @timestamp
#   2. macOS LibreSSL 不输出 "(stdin)= " 前缀, 用 awk '{print $NF}' 拿最后字段
#   3. bash echo 默认不解释 \n, canonical_request 改用 printf 一次性构造
#   4. canonical_headers 内部用真换行 (printf), 不用字面 \n

set -euo pipefail

REFERENCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../scripts/_creds.sh
source "${REFERENCE_DIR}/../scripts/_creds.sh"
# shellcheck source=_tc3-safe.sh
source "${REFERENCE_DIR}/_tc3-safe.sh"
secret_id="${TENCENTCLOUD_SECRET_ID}"
secret_key="${TENCENTCLOUD_SECRET_KEY}"
unset TENCENTCLOUD_SECRET_ID TENCENTCLOUD_SECRET_KEY

if [[ -z "${secret_id}" || -z "${secret_key}" ]]; then
  echo "❌ AKSK 缺失, 请先跑 setup-keychain.sh" >&2
  exit 1
fi

echo "🔑 secret_id: ${secret_id:0:8}...${secret_id: -4}"
echo ""

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
secret_date=$(hmac_sha256_hex "TC3${secret_key}" "${date}")
unset secret_key TENCENTCLOUD_SECRET_KEY
secret_service=$(hmac_sha256_hex "${secret_date}" "${service}" hex)
secret_signing=$(hmac_sha256_hex "${secret_service}" "tc3_request" hex)
signature=$(hmac_sha256_hex "${secret_signing}" "${string_to_sign}" hex)
echo "TC3 signing key chain generated without exposing key material in argv."

# ============= 步骤 4：拼接 Authorization =============
authorization="${algorithm} Credential=${secret_id}/${credential_scope}, SignedHeaders=content-type;host;x-tc-action, Signature=${signature}"

echo ""
echo "=== authorization ==="
echo "${algorithm} Credential=${secret_id:0:8}.../[scope redacted], SignedHeaders=content-type;host;x-tc-action, Signature=[redacted]"
echo ""

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

# ============= 步骤 5：发起请求 =============
echo "📡 请求: POST https://${host}  Action=${action}"
echo ""
curl -sS -XPOST "https://${host}" -d "$payload" -H "@${headers_file}"
echo ""
echo "curl exit: $?"
