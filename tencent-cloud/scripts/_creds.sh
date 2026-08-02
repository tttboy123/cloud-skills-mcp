#!/usr/bin/env bash
# _creds.sh — 从 macOS Keychain 读 tccli 凭证, 导出 TENCENTCLOUD_SECRET_ID/SECRET_KEY
# 用法: source scripts/_creds.sh
# 安全: 凭证不写日志, 不进 history
#
# ⚠️ 重要: tccli 读的是 TENCENTCLOUD_SECRET_ID / TENCENTCLOUD_SECRET_KEY
#          (带下划线, 跟 SDK 一致), 不是 TENCENTCLOUD_SECRETID / SECRETKEY

set -euo pipefail

# 优先从 Keychain 读, 失败回退到环境变量
if command -v security >/dev/null 2>&1; then
  SECRET_ID=$(security find-generic-password -s "tencent-cloud" -a "tccli-secretid" -w 2>/dev/null || echo "")
  SECRET_KEY=$(security find-generic-password -s "tencent-cloud" -a "tccli-secretkey" -w 2>/dev/null || echo "")
fi

# 回退: 环境变量 (带下划线, 跟 tccli 期望的一致)
: "${TENCENTCLOUD_SECRET_ID:=${SECRET_ID:-}}"
: "${TENCENTCLOUD_SECRET_KEY:=${SECRET_KEY:-}}"

# 兼容老的无下划线写法 (有就转成新的, 警告)
if [[ -n "${TENCENTCLOUD_SECRETID:-}" && -z "${TENCENTCLOUD_SECRET_ID:-}" ]]; then
  echo "⚠️  警告: TENCENTCLOUD_SECRETID (无下划线) 已废弃, 请用 TENCENTCLOUD_SECRET_ID" >&2
  export TENCENTCLOUD_SECRET_ID="${TENCENTCLOUD_SECRETID}"
fi
if [[ -n "${TENCENTCLOUD_SECRETKEY:-}" && -z "${TENCENTCLOUD_SECRET_KEY:-}" ]]; then
  echo "⚠️  警告: TENCENTCLOUD_SECRETKEY (无下划线) 已废弃, 请用 TENCENTCLOUD_SECRET_KEY" >&2
  export TENCENTCLOUD_SECRET_KEY="${TENCENTCLOUD_SECRETKEY}"
fi

if [[ -z "${TENCENTCLOUD_SECRET_ID}" || -z "${TENCENTCLOUD_SECRET_KEY}" ]]; then
  echo "❌ 没找到 tccli 凭证" >&2
  echo "" >&2
  echo "首次使用请跑:" >&2
  echo "  bash ~/.codex/skills/tencent-cloud/scripts/setup-keychain.sh" >&2
  echo "" >&2
  echo "或临时用环境变量:" >&2
  echo "  export TENCENTCLOUD_SECRET_ID=AKIDxxxxxx" >&2
  echo "  export TENCENTCLOUD_SECRET_KEY=xxxxxx" >&2
  exit 1
fi

export TENCENTCLOUD_SECRET_ID
export TENCENTCLOUD_SECRET_KEY
