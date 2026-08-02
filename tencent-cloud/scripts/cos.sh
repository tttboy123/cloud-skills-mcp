#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# cos.sh — 管理 COS (对象存储) 桶和文件
# 用法: cos.sh list|describe|put|get|rm [args]

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
cos.sh — 管理 COS (对象存储) 桶和文件

用法:
  cos.sh list                                       # 列桶
  cos.sh ls <bucket> [prefix]                       # 列对象
  cos.sh cat <bucket> <key>                         # 下载到 stdout
  cos.sh put <bucket> <local-text-file> <key>       # 上传文本；二进制/大文件请用 coscli
  cos.sh rm <bucket> <key>                          # 删除 (需 --force)

注意: 删除操作需 --force 二次确认
EOF
}

full_bucket_name() {
  local bucket=$1
  if [[ "${bucket}" =~ -[0-9]{5,}$ ]]; then
    printf '%s' "${bucket}"
    return
  fi
  if [[ -z "${APPID:-}" ]]; then
    echo "bucket must include its APPID suffix, or set APPID in the environment" >&2
    return 2
  fi
  printf '%s-%s' "${bucket}" "${APPID}"
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
    FULL_BUCKET=$(full_bucket_name "${BUCKET}")
    if [[ -n "${PREFIX}" ]]; then
      tccli cos ListObjects --Bucket "${FULL_BUCKET}" --Region "${REGION}" --Prefix "${PREFIX}" 2>&1 | jq -r '.Contents[]? | "\(.Key) \(.Size) \(.LastModified)"' | head -50
    else
      tccli cos ListObjects --Bucket "${FULL_BUCKET}" --Region "${REGION}" 2>&1 | jq -r '.Contents[]? | "\(.Key) \(.Size) \(.LastModified)"' | head -50
    fi
    ;;
  cat)
    [[ $# -lt 2 ]] && { echo "usage: cos.sh cat <bucket> <key>" >&2; exit 1; }
    BUCKET="$1"; KEY="$2"
    FULL_BUCKET=$(full_bucket_name "${BUCKET}")
    COS_OUTPUT=$(mktemp)
    if tccli cos GetObject --Bucket "${FULL_BUCKET}" --Region "${REGION}" --Key "${KEY}" --bin "${COS_OUTPUT}" >/dev/null; then
      cat "${COS_OUTPUT}"
      rm -f "${COS_OUTPUT}"
    else
      COS_EXIT=$?
      rm -f "${COS_OUTPUT}"
      exit "${COS_EXIT}"
    fi
    ;;
  put)
    [[ $# -lt 3 ]] && { echo "usage: cos.sh put <bucket> <local-file> <key>" >&2; exit 1; }
    BUCKET="$1"; LOCAL="$2"; KEY="$3"
    [[ ! -f "${LOCAL}" ]] && { echo "❌ local file not found: ${LOCAL}" >&2; exit 1; }
    FULL_BUCKET=$(full_bucket_name "${BUCKET}")
    PAYLOAD=$(mktemp)
    jq -n --arg bucket "${FULL_BUCKET}" --arg region "${REGION}" --arg key "${KEY}" --rawfile body "${LOCAL}" \
      '{Bucket:$bucket, Region:$region, Key:$key, Body:$body}' > "${PAYLOAD}"
    echo "⬆️  上传 ${LOCAL} → cos://${BUCKET}/${KEY}..."
    if tccli cos PutObject --cli-input-json "file://${PAYLOAD}"; then
      rm -f "${PAYLOAD}"
    else
      COS_EXIT=$?
      rm -f "${PAYLOAD}"
      exit "${COS_EXIT}"
    fi
    ;;
  rm)
    [[ $# -lt 2 ]] && { echo "usage: cos.sh rm <bucket> <key> --force" >&2; exit 1; }
    BUCKET="$1"; KEY="$2"
    FULL_BUCKET=$(full_bucket_name "${BUCKET}")
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
    tccli cos DeleteObject --Bucket "${FULL_BUCKET}" --Region "${REGION}" --Key "${KEY}"
    ;;
  help|*)
    usage
    ;;
esac
