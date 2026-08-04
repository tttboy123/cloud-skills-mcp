#!/usr/bin/env bash
# live-acceptance.sh runs the opt-in live gates against real provider
# credentials injected through the process environment. It refuses to run
# without an explicit gate flag and requires the mutation gate when a
# mutation-only gate is enabled. Skipped gates are reported, never hidden.
set -euo pipefail

GATES=(
  "CLOUD_SKILLS_LIVE_AWS_IOT_MQTT|TestLiveAWSIoTMQTTMutation|mutation"
  "CLOUD_SKILLS_LIVE_AWS_IVS_CHAT|TestLiveAWSIVSChatMutation|mutation"
  "CLOUD_SKILLS_LIVE_AWS_CHIME_MESSAGING|TestLiveAWSChimeMessagingSubscribeReadOnly|read"
  "CLOUD_SKILLS_LIVE_AWS_CONNECT_CHAT|TestLiveAWSConnectChatObserve|mutation"
  "CLOUD_SKILLS_LIVE_AWS_ECR|TestLiveAWSECRReadOnly|read"
  "CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC|TestLiveAWSECRPublicReadOnly|read"
  "CLOUD_SKILLS_LIVE_ALIBABA_MQ|TestLiveAlibabaMQMutation|mutation"
  "CLOUD_SKILLS_LIVE_ALIBABA_ACR|TestLiveAlibabaACRReadOnly|read"
  "CLOUD_SKILLS_LIVE_AZURE_ACR|TestLiveAzureACRReadOnly|read"
  "CLOUD_SKILLS_LIVE_AZURE_OPENAI_RESPONSES|TestLiveAzureOpenAIResponsesRead|read"
  "CLOUD_SKILLS_LIVE_AZURE_OPENAI_STREAM|TestLiveAzureOpenAIStreamRead|read"
  "CLOUD_SKILLS_LIVE_AZURE_SIGNALR|TestLiveAzureSignalRSubscribeReadOnly|read"
  "CLOUD_SKILLS_LIVE_BAIDU_CCR|TestLiveBaiduCCRReadOnly|read"
  "CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB|TestLiveBaiduIoTCoreHTTPPubMutation|mutation"
  "CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT|TestLiveBaiduIoTCoreMQTTReadOnly|read"
  "CLOUD_SKILLS_LIVE_BAIDU_RTC|TestLiveBaiduRTCAgentMutation|mutation"
  "CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY|TestLiveGCPArtifactRegistryReadOnly|read"
  "CLOUD_SKILLS_LIVE_TENCENT_CLS|TestLiveTencentCLSReadOnly|read"
  "CLOUD_SKILLS_LIVE_TENCENT_TCR|TestLiveTencentTCRReadOnly|read"
)

enabled=0
mutation_enabled=0
if [[ "${CLOUD_SKILLS_LIVE_TEST:-}" == "1" && -n "${CLOUD_SKILLS_LIVE_PROVIDERS:-}" ]]; then
  enabled=1
fi
for entry in "${GATES[@]}"; do
  env_flag=${entry%%|*}
  if [[ "${!env_flag:-}" == "1" ]]; then
    enabled=1
    kind=$(printf '%s' "${entry}" | cut -d'|' -f3)
    if [[ "${kind}" == "mutation" ]]; then
      mutation_enabled=1
    fi
  fi
done

if [[ "${CLOUD_SKILLS_LIVE_TEST:-}" != "1" ]]; then
  echo "live acceptance requires CLOUD_SKILLS_LIVE_TEST=1 plus provider credentials" >&2
  exit 2
fi
if [[ ${enabled} -ne 1 ]]; then
  echo "no CLOUD_SKILLS_LIVE_* gate is enabled; enable at least one (see docs/goal-completion-matrix.md live gate commands)" >&2
  exit 2
fi
if [[ ${mutation_enabled} -eq 1 && "${CLOUD_SKILLS_ALLOW_MUTATIONS:-}" != "1" ]]; then
  echo "mutation live gates require CLOUD_SKILLS_ALLOW_MUTATIONS=1 after explicit operator approval" >&2
  exit 2
fi

ROOT=${1:-$(pwd)}
cd "${ROOT}"
log=$(mktemp)
trap 'rm -f "${log}"' EXIT

go test -race -count=1 -v ./internal/mcp/cloud -run '^TestLive' 2>&1 | tee "${log}"
if grep -q -- '--- FAIL' "${log}"; then
  echo "live acceptance failed" >&2
  exit 1
fi
if ! grep -q -- '--- PASS' "${log}"; then
  echo "live acceptance ran but every gate was skipped; enable a CLOUD_SKILLS_LIVE_* gate and inject credentials" >&2
  exit 1
fi
echo "live acceptance: enabled gates passed; remaining gates skipped pending credentials"
