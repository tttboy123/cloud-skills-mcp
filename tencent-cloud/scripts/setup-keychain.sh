#!/usr/bin/env bash
# setup-keychain.sh — 把腾讯云 SecretId/SecretKey 存到 macOS Keychain
# 用法: bash setup-keychain.sh
# 凭证位置: service=tencent-cloud, account=tccli-secretid / tccli-secretkey

set -euo pipefail

command -v security >/dev/null 2>&1 || { echo "security CLI is required (macOS only)" >&2; exit 1; }
command -v tccli >/dev/null 2>&1 || { echo "tccli is required; install and place it on PATH first" >&2; exit 1; }

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

# 3. Region
read -r -p "默认 Region [ap-shanghai]: " REGION
REGION="${REGION:-ap-shanghai}"
if [[ ! "${REGION}" =~ ^[a-z][a-z0-9-]{1,31}$ ]]; then
  echo "❌ Region 格式无效: ${REGION}" >&2
  exit 1
fi

# 4. 先用输入值验证。验证失败时不修改 Keychain。
echo ""
echo "🧪 验证凭证 (只读调用 DescribeInstances)..."
export TENCENTCLOUD_SECRET_ID="${SECRET_ID}"
export TENCENTCLOUD_SECRET_KEY="${SECRET_KEY}"
export TENCENTCLOUD_REGION="${REGION}"

TC_OUT=$(mktemp)
cleanup() {
  rm -f "${TC_OUT}"
  unset SECRET_KEY STORED_SECRET TENCENTCLOUD_SECRET_KEY
}
trap cleanup EXIT

if tccli cvm DescribeInstances --region "${REGION}" --Limit 1 > "${TC_OUT}" 2>&1; then
  echo "✅ 凭证验证通过"
else
  TC_CLI_EXIT=$?
  ERR_MSG=$(grep -oE "(secretId is invalid|AuthFailure|UnauthorizedOperation|InvalidParameter|RequestLimitExceeded|InternalError|ResourceNotFound|InvalidCredential)[^[:space:]]*" "${TC_OUT}" 2>/dev/null | head -1 || true)
  echo "❌ tccli 调用失败 (exit ${TC_CLI_EXIT}); Keychain 未修改" >&2
  if [[ -n "${ERR_MSG}" ]]; then
    echo "腾讯云错误: ${ERR_MSG}" >&2
  else
    echo "请检查 SecretId/SecretKey、Region、CAM 权限和账户状态。" >&2
  fi
  exit 1
fi

# 5. 安全写入 Keychain。security(1) 明确警告 `-w <value>` 会把密码
# 暴露在进程参数中，所以 SecretKey 由 security 自己再次静默提示。
echo ""
echo "请再次输入同一个 SecretKey，让 macOS security 直接写入 Keychain。"
while true; do
  security add-generic-password -s "tencent-cloud" -a "tccli-secretkey" -U -w
  STORED_SECRET=$(security find-generic-password -s "tencent-cloud" -a "tccli-secretkey" -w)
  if [[ "${STORED_SECRET}" == "${SECRET_KEY}" ]]; then
    break
  fi
  echo "❌ 两次输入的 SecretKey 不一致，请重试。" >&2
done

# SecretId 不是密码材料；region 也不是秘密。-U 会原地更新，无需先删除旧项。
security add-generic-password -s "tencent-cloud" -a "tccli-secretid" -w "${SECRET_ID}" -U
security add-generic-password -s "tencent-cloud" -a "tccli-region" -w "${REGION}" -U

echo ""
echo "✅ 凭证已验证并存到 Keychain"
echo "   service: tencent-cloud"
echo "   account: tccli-secretid / tccli-secretkey / tccli-region"
echo "   region:  ${REGION}"
echo ""
echo "下一步: bash ~/.codex/skills/tencent-cloud/scripts/cvm.sh list"
