#!/bin/bash
# 腾讯云 Lighthouse API 签名 v3 (macOS 兼容版)
# 原始版本: 用户给 (跟 cvm 一样的 v3 文档代码)
# 修复:
#   1. macOS BSD date 不支持 -d, 改用 -r @timestamp
#   2. macOS LibreSSL 不输出 "(stdin)= " 前缀, 用 awk '{print $NF}' 拿最后字段
#   3. bash echo 默认不解释 \n, canonical_request 改用 printf 一次性构造
# 4. service: cvm -> lighthouse, host: cvm.tencentcloudapi.com -> lighthouse.tencentcloudapi.com
# 5. version: 2017-03-12 -> 2020-03-24

set -euo pipefail

# 从当前 skill 的 scripts 目录读取凭证，不依赖某个用户的安装路径。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../scripts" && pwd)"
REFERENCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../scripts/_creds.sh
source "${SCRIPT_DIR}/_creds.sh"
# shellcheck source=_tc3-safe.sh
source "${REFERENCE_DIR}/_tc3-safe.sh"

# ⚠️ 重要: tccli 读的是 TENCENTCLOUD_SECRET_ID (带下划线, 跟 SDK 一致)
secret_id="${TENCENTCLOUD_SECRET_ID}"
secret_key="${TENCENTCLOUD_SECRET_KEY}"
unset TENCENTCLOUD_SECRET_ID TENCENTCLOUD_SECRET_KEY

if [[ -z "${secret_id}" || -z "${secret_key}" ]]; then
  echo "❌ AKSK 缺失, 请先跑 setup-keychain.sh" >&2
  exit 1
fi

echo "🔑 secret_id: ${secret_id:0:8}...${secret_id: -4}"
echo ""

# ============= 参数 (Lighthouse) =============
service="lighthouse"
host="lighthouse.tencentcloudapi.com"
region="ap-shanghai"
action="DescribeInstances"
version="2020-03-24"
algorithm="TC3-HMAC-SHA256"
timestamp=$(date +%s)
# macOS = BSD date, 不用 -d; 兼容写法
date=$(date -u -r "$timestamp" +"%Y-%m-%d" 2>/dev/null || date -u -d "@$timestamp" +"%Y-%m-%d")
payload='{}'
action_lower=$(echo "$action" | awk '{print tolower($0)}')

echo "📅 UTC date: ${date}"
echo "🕐 timestamp: ${timestamp}"
echo "🌏 region: ${region}"
echo "🎯 action: ${action}"
echo ""

# ============= 步骤 1：拼接规范请求串 =============
hashed_request_payload=$(printf "%s" "$payload" | sha256_hex)

canonical_request=$(printf "%s\n%s\n%s\ncontent-type:application/json; charset=utf-8\nhost:%s\nx-tc-action:%s\n\n%s\n%s" \
  "POST" \
  "/" \
  "" \
  "$host" \
  "$action_lower" \
  "content-type;host;x-tc-action" \
  "$hashed_request_payload")

echo "=== canonical_request ==="
printf "%s" "$canonical_request" | od -c | head -10
echo ""

# ============= 步骤 2：拼接待签名字符串 =============
credential_scope="${date}/${service}/tc3_request"
hashed_canonical_request=$(printf "%s" "$canonical_request" | sha256_hex)
string_to_sign=$(printf "%s\n%s\n%s\n%s" \
  "$algorithm" \
  "$timestamp" \
  "$credential_scope" \
  "$hashed_canonical_request")

# ============= 步骤 3：计算签名 =============
secret_date=$(hmac_sha256_hex "TC3${secret_key}" "${date}")
unset secret_key TENCENTCLOUD_SECRET_KEY
echo "secret_date:    $secret_date"
secret_service=$(hmac_sha256_hex "${secret_date}" "${service}" hex)
echo "secret_service: $secret_service"
secret_signing=$(hmac_sha256_hex "${secret_service}" "tc3_request" hex)
echo "secret_signing: $secret_signing"
signature=$(hmac_sha256_hex "${secret_signing}" "${string_to_sign}" hex)
echo "signature:      $signature"

# ============= 步骤 4：拼接 Authorization =============
authorization="${algorithm} Credential=${secret_id}/${credential_scope}, SignedHeaders=content-type;host;x-tc-action, Signature=${signature}"

# curl headers may be visible in process arguments. Keep the short-lived
# Authorization value in a mode-0600 file and pass only the file path.
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
echo ""
echo "📡 请求: POST https://${host}  Action=${action}  Region=${region}"
echo ""
curl -sS -XPOST "https://${host}" -d "$payload" -H "@${headers_file}" | python3 -m json.tool
echo ""
echo "curl exit: $?"
