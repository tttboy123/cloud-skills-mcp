#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# lighthouse.sh — 管理 Lighthouse (轻量应用服务器) 实例
# 用法: lighthouse.sh list|describe|start|stop|reboot|reset-pass [args]
#
# ⚠️  Lighthouse ≠ CVM:
#   - CVM (云服务器)        → tccli cvm    / cvm.tencentcloudapi.com / 2017-03-12
#   - Lighthouse (轻量应用) → tccli lighthouse / lighthouse.tencentcloudapi.com / 2020-03-24
#   走不同的 API endpoint, 不能混用
#
# Region 限制: Lighthouse 只在部分 region 可用 (ap-shanghai / ap-guangzhou / ap-beijing 等)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_creds.sh
source "${SCRIPT_DIR}/_creds.sh"

# 默认 region 从 keychain 读
REGION="${TENCENTCLOUD_REGION:-ap-shanghai}"
if command -v security >/dev/null 2>&1; then
  REGION=$(security find-generic-password -s "tencent-cloud" -a "tccli-region" -w 2>/dev/null || echo "${REGION}")
fi

usage() {
  cat <<EOF
lighthouse.sh — 管理 Lighthouse (轻量应用服务器) 实例

用法:
  lighthouse.sh list [--filter <name>]                    # 列实例 (可按名字模糊匹配)
  lighthouse.sh describe <instance-id>                     # 查实例详情
  lighthouse.sh start <instance-id>                        # 开机
  lighthouse.sh stop <instance-id>                         # 关机
  lighthouse.sh reboot <instance-id>                       # 重启
  lighthouse.sh reset-pass <instance-id> --force           # 安全提示输入新密码
  lighthouse.sh destroy <instance-id>                      # 销毁 (需 --force, 默认拒绝)
  lighthouse.sh firewall <instance-id>                     # 查防火墙规则
  lighthouse.sh packages [--region <region>]                # 查可买套餐

示例:
  lighthouse.sh list
  lighthouse.sh list --filter OpenClaw
  lighthouse.sh describe lhins-abc123
  lighthouse.sh stop lhins-abc123
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
    echo "📋 列 Lighthouse 实例 (region: ${REGION})..."
    TC_OUT=$(mktemp)
    set +e
    tccli lighthouse DescribeInstances --region "${REGION}" > "${TC_OUT}" 2>&1
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
      echo "${RESULT}" | jq -r '.InstanceSet[] | select(.InstanceName | test("'"${FILTER}"'"; "i")) | "[\(.InstanceState)] \(.InstanceId) \(.InstanceName) \(.PublicAddresses[0] // "no-ip") \(.CPU)C/\(.Memory)GB"' 2>/dev/null || echo "(jq 解析失败, 见原始输出)"
    else
      echo "${RESULT}" | jq -r '.InstanceSet[] | "[\(.InstanceState)] \(.InstanceId) \(.InstanceName) \(.PublicAddresses[0] // "no-ip") \(.CPU)C/\(.Memory)GB (\(.OsName))"' 2>/dev/null || echo "(jq 解析失败, 见原始输出)"
    fi
    echo ""
    echo "状态码: PENDING/RUNNING/STOPPED/TERMINATING/TERMINATED/REBOOTING/STARTING/STOPPING/RENEWING/ISOLATED/ERROR"
    ;;

  describe)
    [[ $# -lt 1 ]] && { echo "usage: lighthouse.sh describe <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "🔍 ${INSTANCE_ID} 详情..."
    TC_OUT=$(mktemp)
    set +e
    tccli lighthouse DescribeInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}" > "${TC_OUT}" 2>&1
    TC_EXIT=$?
    set -e
    RESULT=$(cat "${TC_OUT}")
    rm -f "${TC_OUT}"
    if [[ $TC_EXIT -ne 0 ]]; then
      echo "${RESULT}" | head -10
      echo "❌ tccli exit ${TC_EXIT}"
      exit 1
    fi
    echo "${RESULT}" | python3 -m json.tool 2>/dev/null || echo "${RESULT}"
    ;;

  start)
    [[ $# -lt 1 ]] && { echo "usage: lighthouse.sh start <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "▶️  开机 ${INSTANCE_ID}..."
    tccli lighthouse StartInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}"
    echo "✅ 开机指令已发, 等几秒后用 'list' 看状态"
    ;;

  stop)
    [[ $# -lt 1 ]] && { echo "usage: lighthouse.sh stop <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "⏹  关机 ${INSTANCE_ID}..."
    tccli lighthouse StopInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}"
    echo "✅ 关机指令已发"
    ;;

  reboot)
    [[ $# -lt 1 ]] && { echo "usage: lighthouse.sh reboot <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "🔄 重启 ${INSTANCE_ID}..."
    tccli lighthouse RebootInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}"
    echo "✅ 重启指令已发"
    ;;

  reset-pass)
    [[ $# -lt 2 ]] && { echo "usage: lighthouse.sh reset-pass <instance-id> --force" >&2; exit 1; }
    INSTANCE_ID="$1"
    shift
    FORCE=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --force) FORCE="1"; shift;;
        *) echo "unknown arg: $1" >&2; exit 1;;
      esac
    done
    if [[ -z "${FORCE}" ]]; then
      echo "❌ 重置密码需要 --force 二次确认" >&2
      exit 1
    fi
    read -r -s -p "新密码: " NEW_PASS
    echo ""
    read -r -s -p "再次输入新密码: " CONFIRM_PASS
    echo ""
    if [[ -z "${NEW_PASS}" || "${NEW_PASS}" != "${CONFIRM_PASS}" ]]; then
      echo "❌ 两次输入的密码不一致或为空" >&2
      exit 1
    fi
    PAYLOAD=$(mktemp)
    cleanup_reset_payload() {
      rm -f "${PAYLOAD}"
      unset NEW_PASS CONFIRM_PASS
    }
    trap cleanup_reset_payload EXIT
    printf '%s' "${NEW_PASS}" | jq -n --arg id "${INSTANCE_ID}" --rawfile password /dev/stdin \
      '{InstanceIds:[$id], Password:$password}' > "${PAYLOAD}"
    unset NEW_PASS CONFIRM_PASS
    echo "🔑 重置 ${INSTANCE_ID} 密码..."
    tccli lighthouse ResetInstancesPassword --region "${REGION}" --cli-input-json "file://${PAYLOAD}"
    ;;

  destroy)
    [[ $# -lt 1 ]] && { echo "usage: lighthouse.sh destroy <instance-id> --force" >&2; exit 1; }
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
    tccli lighthouse TerminateInstances --region "${REGION}" --InstanceIds.0 "${INSTANCE_ID}"
    ;;

  firewall)
    [[ $# -lt 1 ]] && { echo "usage: lighthouse.sh firewall <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "🛡  查 ${INSTANCE_ID} 防火墙规则..."
    tccli lighthouse DescribeFirewallRules --region "${REGION}" --InstanceId "${INSTANCE_ID}" 2>&1 | python3 -m json.tool 2>/dev/null
    ;;

  packages)
    echo "📦 查 Lighthouse 套餐 (region: ${REGION})..."
    tccli lighthouse DescribeBundles --region "${REGION}" --BundleIds.0 "" --Limit 20 2>&1 | jq -r '.BundleSet[]? | "\(.BundleId)  \(.CPU)C/\(.Memory)GB  \(.SystemDiskType) \(.SystemDiskSize)GB  \(.MonthlyTraffic // 0)GB/月  ¥\(.Price // 0)/月"' 2>/dev/null | head -20
    ;;

  help|*)
    usage
    ;;
esac
