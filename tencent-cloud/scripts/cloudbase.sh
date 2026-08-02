#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# cloudbase.sh — 管理 CloudBase (Serverless) 环境
# 用法: cloudbase.sh envs|functions|databases|storage [args]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_creds.sh
source "${SCRIPT_DIR}/_creds.sh"

REGION="${TENCENTCLOUD_REGION:-ap-shanghai}"
if command -v security >/dev/null 2>&1; then
  REGION=$(security find-generic-password -s "tencent-cloud" -a "tccli-region" -w 2>/dev/null || echo "${REGION}")
fi

usage() {
  cat <<EOF
cloudbase.sh — 管理 CloudBase (Serverless) 环境

用法:
  cloudbase.sh envs                  # 列环境
  cloudbase.sh functions [env-id]    # 列云函数
  cloudbase.sh databases [env-id]    # 列数据库
  cloudbase.sh storage [env-id]      # 列存储桶

注意: CloudBase 实际推荐用官方 CLI: tcb (npm i -g @cloudbase/cli)
      这个脚本用 tccli tcb 子命令作为 backup.
EOF
}

cmd=${1:-help}
shift || true

case "${cmd}" in
  envs)
    echo "📋 列 CloudBase 环境..."
    tccli tcb DescribeEnvList 2>&1 | jq -r '.EnvList[]? | "\(.EnvId) [\(.Region)] status=\(.Status) \(.EnvName // "")"' 2>/dev/null || \
      echo "(如果失败, 请用 tcb cli: tcb env list)"
    ;;
  functions)
    ENV_ID="${1:-}"
    if [[ -z "${ENV_ID}" ]]; then
      echo "💡 自动选第一个环境 (用 envs 看 ID)..."
      ENV_ID=$(tccli tcb DescribeEnvList 2>&1 | jq -r '.EnvList[0].EnvId // empty')
      [[ -z "${ENV_ID}" ]] && { echo "❌ 没找到 CloudBase 环境" >&2; exit 1; }
    fi
    echo "📋 列 ${ENV_ID} 云函数..."
    tccli scf ListFunctions --region "${REGION}" --Namespace "cloudbase-${ENV_ID}" 2>&1 | jq -r '.Functions[]? | "\(.FunctionName) runtime=\(.Runtime) status=\(.Status // "?")"' 2>/dev/null || \
      echo "(如果失败, 试用 tcb fn list)"
    ;;
  databases)
    ENV_ID="${1:-}"
    [[ -z "${ENV_ID}" ]] && ENV_ID=$(tccli tcb DescribeEnvList 2>&1 | jq -r '.EnvList[0].EnvId // empty')
    [[ -z "${ENV_ID}" ]] && { echo "❌ 没找到 CloudBase 环境" >&2; exit 1; }
    echo "📋 列 ${ENV_ID} 数据库..."
    tccli tcb DescribeDatabase --EnvId "${ENV_ID}" 2>&1 | jq '.'
    ;;
  storage)
    ENV_ID="${1:-}"
    [[ -z "${ENV_ID}" ]] && ENV_ID=$(tccli tcb DescribeEnvList 2>&1 | jq -r '.EnvList[0].EnvId // empty')
    [[ -z "${ENV_ID}" ]] && { echo "❌ 没找到 CloudBase 环境" >&2; exit 1; }
    echo "📋 列 ${ENV_ID} 存储桶..."
    tccli tcb DescribeStorage --EnvId "${ENV_ID}" 2>&1 | jq '.'
    ;;
  help|*)
    usage
    ;;
esac
