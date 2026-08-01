#!/usr/bin/env bash
# cvm.sh — 管理 CVM (云服务器) 实例
# 用法: cvm.sh list|describe|start|stop|reboot|reset-pass [args]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_creds.sh
source "${SCRIPT_DIR}/_creds.sh"

# 默认 region 从 keychain 读
REGION="${TENCENTCLOUD_REGION:-ap-guangzhou}"
if command -v security >/dev/null 2>&1; then
  REGION=$(security find-generic-password -s "tencent-cloud" -a "tccli-region" -w 2>/dev/null || echo "${REGION}")
fi

usage() {
  cat <<EOF
cvm.sh — 管理 CVM (云服务器) 实例

用法:
  cvm.sh list [--filter <name>]                    # 列实例 (可按名字模糊匹配)
  cvm.sh describe <instance-id>                     # 查实例详情
  cvm.sh start <instance-id>                        # 开机
  cvm.sh stop <instance-id>                         # 关机
  cvm.sh reboot <instance-id>                       # 重启
  cvm.sh reset-pass <instance-id> <new-pass>        # 重置密码 (需 --force)
  cvm.sh destroy <instance-id>                      # 销毁 (需 --force, 默认拒绝)

示例:
  cvm.sh list
  cvm.sh list --filter web-prod
  cvm.sh describe ins-abc123
  cvm.sh stop ins-abc123
EOF
}

cmd=${1:-help}
shift || true

case "${cmd}" in
  list)
    FILTER=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --filter) FILTER="$2"; shift 2;;
        *) echo "unknown arg: $1" >&2; exit 1;;
      esac
    done
    echo "📋 列 CVM 实例 (region: ${REGION})..."
    TC_OUT=$(mktemp)
    set +e
    tccli cvm DescribeInstances --region "${REGION}" > "${TC_OUT}" 2>&1
    TC_EXIT=$?
    set -e
    RESULT=$(cat "${TC_OUT}")
    rm -f "${TC_OUT}"
    if [[ $TC_EXIT -ne 0 ]]; then
      echo "${RESULT}" | head -10
      echo ""
      echo "❌ tccli exit ${TC_EXIT}, 见上方错误"
      ERR=$(grep -oE "(secretId is invalid|AuthFailure|UnauthorizedOperation|InvalidParameter|RequestLimitExceeded|InternalError|ResourceNotFound|InvalidCredential)[^[:space:]]*" <<< "${RESULT}" 2>/dev/null | head -1)
      [[ -n "${ERR}" ]] && echo "腾讯云错误: ${ERR}"
      exit 1
    fi
    if [[ -n "${FILTER}" ]]; then
      echo "${RESULT}" | jq -r '.InstanceSet[] | select(.InstanceName | test("'"${FILTER}"'"; "i")) | "[\(.InstanceState)] \(.InstanceId) \(.InstanceName) \(.PublicIpAddresses[0] // "no-ip") \(.InstanceType)"' 2>/dev/null || echo "(jq 解析失败, 见原始输出)"
    else
      echo "${RESULT}" | jq -r '.InstanceSet[] | "[\(.InstanceState)] \(.InstanceId) \(.InstanceName) \(.PublicIpAddresses[0] // "no-ip") \(.InstanceType) (\(.CPU)CPU/\(.Memory)MB)"' 2>/dev/null || echo "(jq 解析失败, 见原始输出)"
    fi
    echo ""
    echo "状态码: 0=创建中 1=运行中 2=开机中 3=关机中 4=已关机 5=已销毁"
    ;;

  describe)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh describe <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    tccli cvm DescribeInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}" 2>&1 | jq '.'
    ;;

  start)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh start <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "▶️  开机 ${INSTANCE_ID}..."
    tccli cvm StartInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}"
    echo "✅ 开机指令已发, 等几秒后用 'list' 看状态"
    ;;

  stop)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh stop <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "⏹  关机 ${INSTANCE_ID}..."
    tccli cvm StopInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}" --StopType "SOFT"
    echo "✅ 关机指令已发"
    ;;

  reboot)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh reboot <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "🔄 重启 ${INSTANCE_ID}..."
    tccli cvm RebootInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}"
    echo "✅ 重启指令已发"
    ;;

  reset-pass)
    [[ $# -lt 2 ]] && { echo "usage: cvm.sh reset-pass <instance-id> <new-pass> --force" >&2; exit 1; }
    INSTANCE_ID="$1"
    NEW_PASS="$2"
    shift 2
    FORCE=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --force) FORCE="1"; shift;;
      esac
    done
    if [[ -z "${FORCE}" ]]; then
      echo "❌ 重置密码需要 --force 二次确认" >&2
      exit 1
    fi
    echo "🔑 重置 ${INSTANCE_ID} 密码..."
    tccli cvm ResetInstancesPassword --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}" --Password "${NEW_PASS}"
    ;;

  destroy)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh destroy <instance-id> --force" >&2; exit 1; }
    INSTANCE_ID="$1"
    shift
    FORCE=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --force) FORCE="1"; shift;;
      esac
    done
    if [[ -z "${FORCE}" ]]; then
      echo "🛑 销毁是高危操作, 必须 --force 二次确认" >&2
      echo "   销毁后数据无法恢复, 请先 snapshot/镜像" >&2
      exit 1
    fi
    echo "💀 销毁 ${INSTANCE_ID}..."
    tccli cvm TerminateInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}"
    ;;

  help|*)
    usage
    ;;
esac
