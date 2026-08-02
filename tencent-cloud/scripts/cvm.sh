#!/usr/bin/env bash
# cvm.sh — 管理 CVM (云服务器) 实例
# 用法: cvm.sh list|describe|start|stop|reboot|reset-pass [args]

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
cvm.sh — 管理 CVM (云服务器) 实例

用法:
  cvm.sh list [--filter <name>]                    # 列实例 (可按名字模糊匹配)
  cvm.sh describe <instance-id>                     # 查实例详情
  cvm.sh start <instance-id>                        # 开机
  cvm.sh stop <instance-id>                         # 关机
  cvm.sh reboot <instance-id>                       # 重启
  cvm.sh reset-pass <instance-id> --force           # 安全提示输入新密码
  cvm.sh destroy <instance-id>                      # 销毁 (需 --force, 默认拒绝)

示例:
  cvm.sh list
  cvm.sh list --filter web-prod
  cvm.sh describe ins-abc123
  cvm.sh stop ins-abc123
EOF
}

run_instance_action() {
  local action=$1
  local instance_id=$2
  local stop_type=${3:-}
  local payload exit_code

  if [[ ! "${instance_id}" =~ ^ins-[A-Za-z0-9]{8,64}$ ]]; then
    echo "invalid CVM instance id: ${instance_id}" >&2
    return 2
  fi
  payload=$(mktemp)
  if [[ -n "${stop_type}" ]]; then
    jq -n --arg id "${instance_id}" --arg stop_type "${stop_type}" \
      '{InstanceIds:[$id], StopType:$stop_type}' > "${payload}"
  else
    jq -n --arg id "${instance_id}" '{InstanceIds:[$id]}' > "${payload}"
  fi
  if tccli cvm "${action}" --region "${REGION}" --cli-input-json "file://${payload}"; then
    exit_code=0
  else
    exit_code=$?
  fi
  rm -f "${payload}"
  return "${exit_code}"
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
      echo "${RESULT}" | jq -r --arg filter "${FILTER}" '.InstanceSet[] | select((.InstanceName | ascii_downcase) | contains($filter | ascii_downcase)) | "[\(.InstanceState)] \(.InstanceId) \(.InstanceName) \(.PublicIpAddresses[0] // "no-ip") \(.InstanceType)"' 2>/dev/null || echo "(jq 解析失败, 见原始输出)"
    else
      echo "${RESULT}" | jq -r '.InstanceSet[] | "[\(.InstanceState)] \(.InstanceId) \(.InstanceName) \(.PublicIpAddresses[0] // "no-ip") \(.InstanceType) (\(.CPU)CPU/\(.Memory)MB)"' 2>/dev/null || echo "(jq 解析失败, 见原始输出)"
    fi
    echo ""
    echo "状态码: 0=创建中 1=运行中 2=开机中 3=关机中 4=已关机 5=已销毁"
    ;;

  describe)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh describe <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    run_instance_action DescribeInstances "${INSTANCE_ID}" | jq '.'
    ;;

  start)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh start <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "▶️  开机 ${INSTANCE_ID}..."
    run_instance_action StartInstances "${INSTANCE_ID}"
    echo "✅ 开机指令已发, 等几秒后用 'list' 看状态"
    ;;

  stop)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh stop <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "⏹  关机 ${INSTANCE_ID}..."
    run_instance_action StopInstances "${INSTANCE_ID}" SOFT
    echo "✅ 关机指令已发"
    ;;

  reboot)
    [[ $# -lt 1 ]] && { echo "usage: cvm.sh reboot <instance-id>" >&2; exit 1; }
    INSTANCE_ID="$1"
    echo "🔄 重启 ${INSTANCE_ID}..."
    run_instance_action RebootInstances "${INSTANCE_ID}"
    echo "✅ 重启指令已发"
    ;;

  reset-pass)
	[[ $# -lt 2 ]] && { echo "usage: cvm.sh reset-pass <instance-id> --force" >&2; exit 1; }
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
	tccli cvm ResetInstancesPassword --region "${REGION}" --cli-input-json "file://${PAYLOAD}"
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
    run_instance_action TerminateInstances "${INSTANCE_ID}"
    ;;

  help|*)
    usage
    ;;
esac
