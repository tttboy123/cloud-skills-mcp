#!/usr/bin/env bash
# Build the universal HTTP-only MCP server.

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BIN_DIR=${CLOUD_SKILLS_BIN_DIR:-"${ROOT_DIR}/bin"}

mkdir -p "${BIN_DIR}"

echo "Building cloud-skills-mcp..."
(
  cd "${ROOT_DIR}"
  go build -trimpath -o "${BIN_DIR}/cloud-skills-mcp" ./cmd/cloud-skills-mcp
)

echo "Built ${BIN_DIR}/cloud-skills-mcp"
