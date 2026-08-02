#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SMOKE_DIR=$(mktemp -d)
trap 'rm -rf "${SMOKE_DIR}"' EXIT

"${ROOT_DIR}/install.sh" --bin-dir "${SMOKE_DIR}/bin" --skills-dir "${SMOKE_DIR}/skills" >/dev/null
test -x "${SMOKE_DIR}/bin/tencent-cloud-mcp"
test -x "${SMOKE_DIR}/bin/cloud-skills-mcp"
test -f "${SMOKE_DIR}/skills/aws/SKILL.md"
test -f "${SMOKE_DIR}/skills/azure/SKILL.md"
test -f "${SMOKE_DIR}/skills/tencent-cloud/SKILL.md"
test -f "${SMOKE_DIR}/skills/alicloud/SKILL.md"
test -f "${SMOKE_DIR}/skills/google-cloud/SKILL.md"
test -f "${SMOKE_DIR}/skills/baiducloud/SKILL.md"

set +e
"${ROOT_DIR}/install.sh" --bin-dir "${SMOKE_DIR}/bin2" --skills-dir "${SMOKE_DIR}/skills" > "${SMOKE_DIR}/collision.log" 2>&1
COLLISION_EXIT=$?
set -e
test "${COLLISION_EXIT}" -eq 3
test ! -e "${SMOKE_DIR}/bin2/cloud-skills-mcp"
test ! -e "${SMOKE_DIR}/bin2/tencent-cloud-mcp"

echo "installer smoke: install and collision preflight verified"
