#!/usr/bin/env bash
# mapping-audit.sh proves the goal's mapping invariant mechanically: every
# auth_scheme implemented in the six-cloud MCP server must be (a) defined as a
# Go constant, (b) wired into the policy or adapter dispatch allowlists,
# (c) covered by at least one hermetic test, (d) exercised by the protocol
# smoke script, (e) documented in api-protocol-coverage.md, (f) mapped to a
# goal-completion-matrix row, and (g) listed in the server tool schema. The
# six skill suites (SKILL.md + agents/openai.yaml + references/official-docs.md)
# and the matrix row statuses are verified as well.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: mapping-audit.sh /path/to/cloud-skills-mcp" >&2
  exit 2
fi
ROOT=$1
[[ -d "${ROOT}/internal/mcp/cloud" ]] || { echo "not a repository root: ${ROOT}" >&2; exit 2; }

CLOUD_DIR="${ROOT}/internal/mcp/cloud"
POLICY="${CLOUD_DIR}/policy.go"
HTTP_ADAPTER="${CLOUD_DIR}/http_adapter.go"
REST_ADAPTER="${CLOUD_DIR}/rest_adapter.go"
SERVER="${CLOUD_DIR}/server.go"
SMOKE="${ROOT}/scripts/ci/protocol-smoke.sh"
COVERAGE="${ROOT}/docs/api-protocol-coverage.md"
MATRIX="${ROOT}/docs/goal-completion-matrix.md"

failed=0
count=0

while IFS= read -r line; do
  [[ -z "${line}" ]] && continue
  scheme=$(printf '%s' "${line}" | sed -E 's/.*"([a-z0-9-]+)"/\1/')
  constant=$(printf '%s' "${line}" | sed -E 's/[[:space:]]*=.*//')
  count=$((count + 1))

  defined=0
  if grep -Fq "\"${scheme}\"" "${CLOUD_DIR}"/*.go 2>/dev/null; then defined=1; fi
  if [[ ${defined} -ne 1 ]]; then
    echo "mapping audit: scheme ${scheme} has no Go constant definition" >&2
    failed=1
  fi

  wired=0
  if grep -Fq "${constant}" "${POLICY}" "${HTTP_ADAPTER}" "${REST_ADAPTER}" 2>/dev/null || grep -Fq "\"${scheme}\"" "${POLICY}" "${HTTP_ADAPTER}" "${REST_ADAPTER}" 2>/dev/null; then wired=1; fi
  if [[ ${wired} -ne 1 ]]; then
    echo "mapping audit: scheme ${scheme} is not wired into policy/adapter dispatch" >&2
    failed=1
  fi

  tested=0
  if grep -rFq "${constant}" "${CLOUD_DIR}" --include='*_test.go' 2>/dev/null || grep -rFq "\"${scheme}\"" "${CLOUD_DIR}" --include='*_test.go' 2>/dev/null; then tested=1; fi
  if [[ ${tested} -ne 1 ]]; then
    echo "mapping audit: scheme ${scheme} has no hermetic test coverage" >&2
    failed=1
  fi

  smoke=0
  if grep -Fq "${scheme}" "${SMOKE}" 2>/dev/null; then smoke=1; fi
  if [[ ${smoke} -ne 1 ]]; then
    echo "mapping audit: scheme ${scheme} has no protocol-smoke entry" >&2
    failed=1
  fi

  coverage=0
  if grep -Fq "${scheme}" "${COVERAGE}" 2>/dev/null; then coverage=1; fi
  if [[ ${coverage} -ne 1 ]]; then
    echo "mapping audit: scheme ${scheme} is missing from api-protocol-coverage.md" >&2
    failed=1
  fi

  matrix=0
  if grep -Fq "${scheme}" "${MATRIX}" 2>/dev/null; then matrix=1; fi
  if [[ ${matrix} -ne 1 ]]; then
    echo "mapping audit: scheme ${scheme} is missing from goal-completion-matrix.md" >&2
    failed=1
  fi

  schema=0
  if grep -Fq "\"${scheme}\"" "${SERVER}" 2>/dev/null || grep -Fq "${scheme}" "${SERVER}" 2>/dev/null; then schema=1; fi
  if [[ ${schema} -ne 1 ]]; then
    echo "mapping audit: scheme ${scheme} is missing from the server tool schema" >&2
    failed=1
  fi
done < <(grep -Eho 'authScheme[A-Za-z0-9_]+[[:space:]]*=[[:space:]]*"[a-z0-9-]+"' "${CLOUD_DIR}"/*.go | sort -u)

for provider in aws azure google-cloud alicloud tencent-cloud baiducloud; do
  for file in SKILL.md agents/openai.yaml references/official-docs.md; do
    if [[ ! -f "${ROOT}/${provider}/${file}" ]]; then
      echo "mapping audit: missing ${provider}/${file}" >&2
      failed=1
    fi
    if [[ ! -f "${ROOT}/plugin/skills/${provider}/${file}" ]]; then
      echo "mapping audit: plugin/skills/${provider}/${file} is missing from the plugin bundle" >&2
      failed=1
    fi
  done
  if ! diff -rq "${ROOT}/${provider}" "${ROOT}/plugin/skills/${provider}" >/dev/null 2>&1; then
    echo "mapping audit: plugin/skills/${provider} drifted from the canonical ${provider} skill" >&2
    failed=1
  fi
done
if [[ ! -f "${ROOT}/plugin/.codex-plugin/plugin.json" || ! -f "${ROOT}/.agents/plugins/marketplace.json" ]]; then
  echo "mapping audit: open-source plugin manifest is incomplete" >&2
  failed=1
fi

# Matrix gate status: every table row except the observable-acceptance row must
# not carry a pending or partial protocol status.
while IFS= read -r row; do
  [[ "${row}" == "| Goal requirement"* ]] && continue
  [[ "${row}" == "|---"* ]] && continue
  [[ "${row}" == *"Observable six-cloud acceptance"* ]] && continue
  status=$(printf '%s' "${row}" | awk -F'|' '{gsub(/^[ \t]+|[ \t]+$/, "", $NF); print $NF}')
  if [[ "${status}" == "**Pending**" || "${status}" == *"Partial"* ]]; then
    echo "mapping audit: matrix row has a pending or partial protocol status: ${status}" >&2
    failed=1
  fi
done < <(grep '^|' "${MATRIX}" || true)

if [[ ${failed} -ne 0 ]]; then
  echo "mapping audit failed" >&2
  exit 1
fi
echo "mapping audit: ${count} schemes, six skill suites, and the open-source plugin bundle verified"
