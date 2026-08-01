#!/usr/bin/env bash
# cos.sh — 管理 COS (对象存储) 桶和文件
# 用法: cos.sh list|describe|put|get|rm [args]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/_creds.sh"

REGION="${TENCENTCLOUD_REGION:-ap-guangzhou}"
if command -v security >/dev/null 2>&1; then
  REGION=$(security find-generic-password -s "tencent-cloud" -a "tccli-region" -w 2>/dev/null || echo "${REGION}")
fi

usage() {
  cat <<EOF
cos.sh — 管理 COS (对象存储) 桶和文件

用法:
  cos.sh list                                       # 列桶
  cos.sh ls <bucket> [prefix]                       # 列对象
  cos.sh cat <bucket> <key>                         # 下载到 stdout
  cos.sh put <bucket> <local-file> <key>            # 上传 (默认 --force)
  cos.sh rm <bucket> <key>                          # 删除 (需 --force)

注意: 删除操作需 --force 二次确认
EOF
}

cmd=${1:-help}
shift || true

case "${cmd}" in
  list)
    echo "📋 列 COS 桶..."
    tccli cos GetService 2>&1 | jq -r '.ListAllMyBucketsResult.Buckets.Bucket[] | "\(.Name) [\(.Region)] created=\(.CreationDate)"'
    ;;
  ls)
    [[ $# -lt 1 ]] && { echo "usage: cos.sh ls <bucket> [prefix]" >&2; exit 1; }
    BUCKET="$1"; PREFIX="${2:-}"
    if [[ -n "${PREFIX}" ]]; then
      tccli cos ListObjects --Bucket "${BUCKET}-${APPID}" --Region "${REGION}" --Prefix "${PREFIX}" 2>&1 | jq -r '.Contents[]? | "\(.Key) \(.Size) \(.LastModified)"' | head -50
    else
      tccli cos ListObjects --Bucket "${BUCKET}-${APPID}" --Region "${REGION}" 2>&1 | jq -r '.Contents[]? | "\(.Key) \(.Size) \(.LastModified)"' | head -50
    fi
    ;;
  cat)
    [[ $# -lt 2 ]] && { echo "usage: cos.sh cat <bucket> <key>" >&2; exit 1; }
    BUCKET="$1"; KEY="$2"
    tccli cos GetObject --Bucket "${BUCKET}-${APPID}" --Region "${REGION}" --Key "${KEY}" --bin /tmp/cos_tmp_$$ 2>&1 | head -1
    cat /tmp/cos_tmp_$$
    rm -f /tmp/cos_tmp_$$
    ;;
  put)
    [[ $# -lt 3 ]] && { echo "usage: cos.sh put <bucket> <local-file> <key>" >&2; exit 1; }
    BUCKET="$1"; LOCAL="$2"; KEY="$3"
    [[ ! -f "${LOCAL}" ]] && { echo "❌ local file not found: ${LOCAL}" >&2; exit 1; }
    echo "⬆️  上传 ${LOCAL} → cos://${BUCKET}/${KEY}..."
    tccli cos PutObject --Bucket "${BUCKET}-${APPID}" --Region "${REGION}" --Key "${KEY}" --Body "$(cat "${LOCAL}")" 2>&1 | head -5
    ;;
  rm)
    [[ $# -lt 2 ]] && { echo "usage: cos.sh rm <bucket> <key> --force" >&2; exit 1; }
    BUCKET="$1"; KEY="$2"
    shift 2
    FORCE=""
    while [[ $# -gt 0 ]]; do
      case "$1" in --force) FORCE="1"; shift;; esac
    done
    if [[ -z "${FORCE}" ]]; then
      echo "🛑 删除需要 --force 二次确认" >&2
      exit 1
    fi
    echo "🗑  删除 cos://${BUCKET}/${KEY}..."
    tccli cos DeleteObject --Bucket "${BUCKET}-${APPID}" --Region "${REGION}" --Key "${KEY}"
    ;;
  help|*)
    usage
    ;;
esac
