#!/usr/bin/env bash
# setup-keychain.sh — 把腾讯云 SecretId/SecretKey 存到 macOS Keychain
# 用法: bash setup-keychain.sh
# 凭证位置: service=tencent-cloud, account=tccli-secretid / tccli-secretkey

set -euo pipefail

echo "🔐 腾讯云凭证 → macOS Keychain"
echo ""
echo "在 https://console.cloud.tencent.com/cam/capi 拿 SecretId + SecretKey"
echo "(建议建个子账号, 给 ReadOnlyAccess 或 QcloudCVMReadOnlyAccess 权限)"
echo ""

# 1. SecretId
read -r -p "SecretId (AKID...): " SECRET_ID
if [[ -z "${SECRET_ID}" ]]; then
  echo "❌ SecretId 不能为空" >&2
  exit 1
fi

# 2. SecretKey (silent input)
read -r -s -p "SecretKey: " SECRET_KEY
echo ""
if [[ -z "${SECRET_KEY}" ]]; then
  echo "❌ SecretKey 不能为空" >&2
  exit 1
fi

# 3. Region (默认 ap-guangzhou)
read -r -p "默认 Region [ap-guangzhou]: " REGION
REGION="${REGION:-ap-guangzhou}"

# 4. 存到 Keychain (更新已有或新增)
# 同时清掉废弃的 TENCENTCLOUD_SECRETID/SECRETKEY 老 env (如果以前写过)
unset TENCENTCLOUD_SECRETID TENCENTCLOUD_SECRETKEY 2>/dev/null || true
security delete-generic-password -s "tencent-cloud" -a "tccli-secretid" 2>/dev/null || true
security delete-generic-password -s "tencent-cloud" -a "tccli-secretkey" 2>/dev/null || true
security delete-generic-password -s "tencent-cloud" -a "tccli-region" 2>/dev/null || true

security add-generic-password -s "tencent-cloud" -a "tccli-secretid" -w "${SECRET_ID}" -U
security add-generic-password -s "tencent-cloud" -a "tccli-secretkey" -w "${SECRET_KEY}" -U
security add-generic-password -s "tencent-cloud" -a "tccli-region" -w "${REGION}" -U

echo ""
echo "✅ 凭证已存到 Keychain:"
echo "   service: tencent-cloud"
echo "   account: tccli-secretid / tccli-secretkey / tccli-region"
echo "   region:  ${REGION}"
echo ""

# 5. 验证 — 用 DescribeInstances 真正需要凭证的 API
#    ⚠️  tccli 读的是 TENCENTCLOUD_SECRET_ID / TENCENTCLOUD_SECRET_KEY (带下划线, 跟 SDK 一致)
#    用临时文件捕获输出, 不用 OUTPUT=$(...) 包, 因为后者会吞 tccli 的真实 exit code
echo "🧪 验证凭证 (调 DescribeInstances)..."
export PATH="$HOME/.local/bin:$PATH"
export TENCENTCLOUD_SECRET_ID="${SECRET_ID}"
export TENCENTCLOUD_SECRET_KEY="${SECRET_KEY}"

TC_OUT=$(mktemp)
# 同步跑 tccli, 拿真实 exit code (不经过子 shell 包装)
tccli cvm DescribeInstances --region "${REGION}" --Limit 1 > "${TC_OUT}" 2>&1
TC_CLI_EXIT=$?

# 显示前 12 行输出
head -12 "${TC_OUT}"
rm -f "${TC_OUT}"

if [[ ${TC_CLI_EXIT} -ne 0 ]]; then
  # 重新调一次专门拿错误 (因为已经清掉文件了, 用 set -o pipefail 抓 stderr)
  ERR_TMP=$(mktemp)
  tccli cvm DescribeInstances --region "${REGION}" --Limit 1 > "${ERR_TMP}" 2>&1
  ERR_MSG=$(grep -oE "(secretId is invalid|AuthFailure|UnauthorizedOperation|InvalidParameter|RequestLimitExceeded|InternalError|ResourceNotFound|InvalidCredential)[^[:space:]]*" "${ERR_TMP}" 2>/dev/null | head -1)
  rm -f "${ERR_TMP}"
  echo ""
  echo "❌ tccli 调用失败 (exit ${TC_CLI_EXIT})"
  echo ""
  if [[ -n "${ERR_MSG}" ]]; then
    echo "腾讯云错误: ${ERR_MSG}"
  else
    echo "可能原因:"
    echo "   - SecretId/SecretKey 是不是复制错了 (注意前后空格)"
    echo "   - 子账号有没有 QcloudCVMReadOnlyAccess 权限"
    echo "   - Region 有没有填错 (ap-guangzhou / ap-shanghai / ap-beijing)"
    echo "   - APPID 可能被风控/欠费冻结 — 查 https://console.cloud.tencent.com/expense"
  fi
  echo ""
  echo "⚠️  凭证已存到 Keychain 但验证未通过, 调 API 仍会失败"
  echo "   修好后再跑: bash $0"
  exit 1
fi

# 二次确认: 凭证存好
echo ""
echo "✅ 凭证验证通过 + 已存到 Keychain"
echo ""
echo "下一步:"
echo "  bash ~/.claude/skills/tencent-cloud/scripts/cvm.sh list"
