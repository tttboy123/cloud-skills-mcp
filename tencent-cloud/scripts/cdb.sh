#!/usr/bin/env bash
# cdb.sh — 管理云数据库 (CDB MySQL) 实例
# 用法: cdb.sh list|describe|start|stop|restart [args]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/_creds.sh"

REGION="${TENCENTCLOUD_REGION:-ap-guangzhou}"
if command -v security >/dev/null 2>&1; then
  REGION=$(security find-generic-password -s "tencent-cloud" -a "tccli-region" -w 2>/dev/null || echo "${REGION}")
fi

usage() {
  cat <<EOF
cdb.sh — 管理云数据库 (CDB MySQL) 实例

用法:
  cdb.sh list [--filter <name>]                 # 列实例
  cdb.sh describe <instance-id>                  # 查详情
  cdb.sh start <instance-id>                     # 启动
  cdb.sh stop <instance-id>                      # 关闭
  cdb.sh restart <instance-id>                   # 重启
  cdb.sh query-log <instance-id> <start> <end>   # 查慢日志 (RFC3339)

状态码: 0=创建中 1=运行中 4=隔离中 5=已删除
EOF
}

cmd=${1:-help}
shift || true

case "${cmd}" in
  list)
    FILTER=""
    while [[ $# -gt 0 ]]; do
      case "$1" in --filter) FILTER="$2"; shift 2;; *) echo "unknown arg: $1" >&2; exit 1;; esac
    done
    echo "📋 列 CDB 实例 (region: ${REGION})..."
    RESULT=$(tccli cdb DescribeDBInstances --region "${REGION}" 2>&1)
    if [[ -n "${FILTER}" ]]; then
      echo "${RESULT}" | jq -r '.Items[] | select(.InstanceName | test("'"${FILTER}"'"; "i")) | "[\(.Status)] \(.InstanceId) \(.InstanceName) \(.Vip):\(.Vport)/\(.EngineVersion)"' 2>/dev/null || echo "${RESULT}" | head -50
    else
      echo "${RESULT}" | jq -r '.Items[] | "[\(.Status)] \(.InstanceId) \(.InstanceName) \(.Vip):\(.Vport)/\(.EngineVersion) mem=\(.Memory)MB"' 2>/dev/null || echo "${RESULT}" | head -50
    fi
    ;;
  describe)
    [[ $# -lt 1 ]] && { echo "usage: cdb.sh describe <instance-id>" >&2; exit 1; }
    tccli cdb DescribeDBInstances --region "${REGION}" --InstanceIds.0 "$1" 2>&1 | jq '.'
    ;;
  start)
    [[ $# -lt 1 ]] && { echo "usage: cdb.sh start <instance-id>" >&2; exit 1; }
    echo "▶️  启动 DB $1..."
    tccli cdb StartDBInstances --region "${REGION}" --InstanceIds.0 "$1"
    ;;
  stop)
    [[ $# -lt 1 ]] && { echo "usage: cdb.sh stop <instance-id>" >&2; exit 1; }
    echo "⏹  关闭 DB $1 (会断连所有应用)..."
    tccli cdb StopDBInstances --region "${REGION}" --InstanceIds.0 "$1"
    ;;
  restart)
    [[ $# -lt 1 ]] && { echo "usage: cdb.sh restart <instance-id>" >&2; exit 1; }
    echo "🔄 重启 DB $1..."
    tccli cdb RestartDBInstances --region "${REGION}" --InstanceIds.0 "$1"
    ;;
  query-log)
    [[ $# -lt 3 ]] && { echo "usage: cdb.sh query-log <instance-id> <start-RFC3339> <end-RFC3339>" >&2; exit 1; }
    tccli cdb DescribeSlowLogData --region "${REGION}" --InstanceId "$1" --StartTime "$2" --EndTime "$3" 2>&1 | jq '.'
    ;;
  help|*)
    usage
    ;;
esac
