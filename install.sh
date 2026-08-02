#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BIN_DIR=${CLOUD_SKILLS_BIN_DIR:-"${HOME}/.local/bin"}
SKILLS_DIR=""
FORCE=0

usage() {
  cat <<'USAGE'
Usage: ./install.sh [options]

Build and install the universal six-cloud MCP binary and Tencent compatibility binary.

Options:
  --bin-dir PATH      Binary destination (default: ~/.local/bin)
  --skills-dir PATH   Also copy all six cloud skill directories
  --force             Replace existing skill directories under --skills-dir
  -h, --help          Show this help

This installer never edits MCP client configuration or cloud credentials.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bin-dir)
      [[ $# -ge 2 ]] || { echo "--bin-dir requires a path" >&2; exit 2; }
      BIN_DIR=$2
      shift 2
      ;;
    --skills-dir)
      [[ $# -ge 2 ]] || { echo "--skills-dir requires a path" >&2; exit 2; }
      SKILLS_DIR=$2
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -n "${SKILLS_DIR}" ]]; then
  SKILLS=(aws azure google-cloud alicloud tencent-cloud baiducloud)
  for skill in "${SKILLS[@]}"; do
    target="${SKILLS_DIR}/${skill}"
    if [[ -e "${target}" && ${FORCE} -ne 1 ]]; then
      echo "refusing to replace existing skill: ${target} (pass --force)" >&2
      exit 3
    fi
  done
fi

BUILD_DIR=$(mktemp -d)
trap 'rm -rf "${BUILD_DIR}"' EXIT

(
  cd "${ROOT_DIR}"
  go build -trimpath -o "${BUILD_DIR}/cloud-skills-mcp" ./cmd/cloud-skills-mcp
  go build -trimpath -o "${BUILD_DIR}/tencent-cloud-mcp" ./cmd/tencent-cloud-mcp
)
mkdir -p "${BIN_DIR}"
install -m 0755 "${BUILD_DIR}/cloud-skills-mcp" "${BIN_DIR}/cloud-skills-mcp"
install -m 0755 "${BUILD_DIR}/tencent-cloud-mcp" "${BIN_DIR}/tencent-cloud-mcp"
echo "Installed binary: ${BIN_DIR}/cloud-skills-mcp"
echo "Installed binary: ${BIN_DIR}/tencent-cloud-mcp"

if [[ -n "${SKILLS_DIR}" ]]; then
  mkdir -p "${SKILLS_DIR}"
  for skill in "${SKILLS[@]}"; do
    target="${SKILLS_DIR}/${skill}"
    if [[ -e "${target}" ]]; then
      rm -rf "${target}"
    fi
    cp -R "${ROOT_DIR}/${skill}" "${target}"
    echo "Installed skill: ${target}"
  done
fi
