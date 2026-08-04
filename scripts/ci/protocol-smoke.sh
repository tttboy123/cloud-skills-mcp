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
MUTATION_RESPONSES=$(mktemp)
trap 'rm -f "${RESPONSES}" "${MUTATION_RESPONSES}"' EXIT

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
  printf '%s\n' '{"jsonrpc":"2.0","id":24,"method":"tools/call","params":{"name":"tencent_api_read","arguments":{"auth_scheme":"cls","service":"cls","operation":"GetLogset","method":"GET","url":"https://nested.ap-beijing.cls.tencentcs.com/logset","parameters":{"logset_id":"example"}}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":25,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"acr","service":"acr","operation":"ListTags","method":"GET","url":"https://registry123.azurecr.io.attacker.example/v2/team/app/tags/list","acr_scope":"repository:team/app:pull"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":26,"method":"tools/call","params":{"name":"aws_api_read","arguments":{"auth_scheme":"ecr","service":"ecr","operation":"ListTags","region":"us-west-2","method":"GET","url":"https://123456789012.dkr.ecr.us-west-2.amazonaws.com.attacker.example/v2/team/app/tags/list"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":27,"method":"tools/call","params":{"name":"gcp_api_read","arguments":{"auth_scheme":"artifact-registry","service":"artifact-registry","operation":"ListTags","method":"GET","url":"https://us-docker.pkg.dev.attacker.example/v2/project/repository/app/tags/list"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":28,"method":"tools/call","params":{"name":"alicloud_api_read","arguments":{"auth_scheme":"acr-registry","service":"acr","operation":"ListTags","region":"cn-hangzhou","registry_instance_id":"cri-example123","method":"GET","url":"https://demo-registry.cn-hangzhou.cr.aliyuncs.com.attacker.example/v2/team/app/tags/list"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":29,"method":"tools/call","params":{"name":"tencent_api_read","arguments":{"auth_scheme":"tcr-registry","service":"tcr","operation":"ListTags","region":"ap-guangzhou","registry_instance_id":"tcr-example123","method":"GET","url":"https://demo-tcr.tencentcloudcr.com.attacker.example/v2/team/app/tags/list"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"baiducloud_api_read","arguments":{"auth_scheme":"ccr-registry","service":"ccr","operation":"ListTags","region":"bj","registry_instance_id":"ccr-example12","registry_user_id":"iam-user-123","method":"GET","url":"https://ccr-example12-pub.cnc.bd.bj.baidubce.com.attacker.example/v2/team/app/tags/list"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"baiducloud_api_read","arguments":{"auth_scheme":"iotcore-mqtt-ws","service":"iotcore","operation":"SubscribeMQTT","method":"GET","url":"wss://aop098js.iot.gz.baidubce.com.attacker.example/mqtt","body":{"client_id":"observer-1","subscriptions":[{"topic_filter":"sensors/#","qos":1}],"max_messages":1,"timeout_seconds":5},"response_file":"/tmp/baidu-iotcore.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"aws_api_read","arguments":{"auth_scheme":"iot-mqtt-ws","service":"iotdevicegateway","operation":"SubscribeMQTT","region":"us-west-2","method":"GET","url":"wss://account-ats.iot.us-west-2.amazonaws.com.attacker.example/mqtt","body":{"client_id":"observer-1","subscriptions":[{"topic_filter":"sensors/#","qos":1}],"max_messages":1,"timeout_seconds":5},"response_file":"/tmp/aws-iot.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":33,"method":"tools/call","params":{"name":"aws_api_read","arguments":{"auth_scheme":"ivs-chat-ws","service":"ivschat","operation":"SubscribeChat","region":"us-west-2","method":"GET","url":"wss://edge.ivschat.us-west-2.amazonaws.com.attacker.example","body":{"room_identifier":"arn:aws:ivschat:us-west-2:123456789012:room/test-room","user_id":"observer","max_messages":1,"timeout_seconds":5},"response_file":"/tmp/aws-ivs-chat.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":34,"method":"tools/call","params":{"name":"aws_api_mutate","arguments":{"auth_scheme":"lex-v2-conversation","service":"lex","operation":"StartConversation","region":"us-west-2","method":"POST","url":"https://runtime-v2-lex.us-west-2.amazonaws.com.attacker.example","body":{"bot_id":"ABCDEFGHIJ","bot_alias_id":"alias-1","locale_id":"en_US","session_id":"session-1","texts":[{"text":"hello","event_id":"lex-evt-1"}],"max_events":1,"timeout_seconds":5},"response_file":"/tmp/aws-lex.ndjson","force":true}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":35,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"signalr-ws","service":"signalr","operation":"Subscribe","method":"GET","url":"wss://demo.service.signalr.net.attacker.example/client/?hub=chat","body":{"max_messages":1,"timeout_seconds":5},"response_file":"/tmp/signalr.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":36,"method":"tools/call","params":{"name":"aws_api_read","arguments":{"auth_scheme":"chime-messaging-ws","service":"chime-messaging","operation":"SubscribeMessages","region":"us-east-1","method":"GET","url":"wss://data-messaging.chime.aws.attacker.example/connect","body":{"user_arn":"arn:aws:chime:us-east-1:123456789012:app-instance/app-1/user/observer","session_id":"session-1","max_messages":1,"timeout_seconds":5},"response_file":"/tmp/chime-messaging.ndjson"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":37,"method":"tools/call","params":{"name":"azure_api_read","arguments":{"auth_scheme":"openai-chat-stream","service":"openai","operation":"StreamChatCompletions","method":"POST","url":"https://demo.openai.azure.com.attacker.example/openai/deployments/gpt-4o-deployment/chat/completions","api_version":"2024-06-01","body":{"model":"gpt-4o-deployment","messages":[{"role":"user","content":"Hello"}],"max_events":1,"timeout_seconds":5},"response_file":"/tmp/chat-stream.ndjson"}}}'
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
      (.inputSchema.properties | has("method") and has("url") and has("body_file") and has("response_file") and has("audience") and has("registry_instance_id") and has("registry_user_id") and has("acr_scope") and has("acr_source_scope") and has("auth_scheme") and has("region_set") and has("auth_version") and has("api_version") and has("payload_mode") and has("checksum_algorithm") and has("stream_chunk_bytes") and has("stream_interval_ms") and has("stream_user_id") and has("stream_format") and (has("arguments") | not))] | all) and
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
    ($responses | map(select(.id == 23))[0].result.content[0].text | contains("mutation approval path")) and
    ($responses | map(select(.id == 24))[0].result.isError == true) and
    ($responses | map(select(.id == 24))[0].result.content[0].text | contains("exact regional")) and
    ($responses | map(select(.id == 25))[0].result.isError == true) and
    ($responses | map(select(.id == 25))[0].result.content[0].text | contains("exact public login endpoint")) and
    ($responses | map(select(.id == 26))[0].result.isError == true) and
    ($responses | map(select(.id == 26))[0].result.content[0].text | contains("exact private or public Registry endpoint")) and
    ($responses | map(select(.id == 27))[0].result.isError == true) and
    ($responses | map(select(.id == 27))[0].result.content[0].text | contains("exact docker.pkg.dev or supported gcr.io endpoint")) and
    ($responses | map(select(.id == 28))[0].result.isError == true) and
    ($responses | map(select(.id == 28))[0].result.content[0].text | contains("exact Enterprise Edition public or VPC endpoint")) and
    ($responses | map(select(.id == 29))[0].result.isError == true) and
    ($responses | map(select(.id == 29))[0].result.content[0].text | contains("exact Enterprise Edition public, VPC, or operator-pinned custom endpoint")) and
    ($responses | map(select(.id == 30))[0].result.isError == true) and
    ($responses | map(select(.id == 30))[0].result.content[0].text | contains("exact Personal or Enterprise public, VPC, or operator-pinned custom endpoint")) and
    ($responses | map(select(.id == 31))[0].result.isError == true) and
    ($responses | map(select(.id == 31))[0].result.content[0].text | contains("exact single-instance official endpoint")) and
    ($responses | map(select(.id == 32))[0].result.isError == true) and
    ($responses | map(select(.id == 32))[0].result.content[0].text | contains("region or endpoint allowlist")) and
    ($responses | map(select(.id == 33))[0].result.isError == true) and
    ($responses | map(select(.id == 33))[0].result.content[0].text | contains("official region endpoint")) and
    ($responses | map(select(.id == 34))[0].result.isError == true) and
    ($responses | map(select(.id == 34))[0].result.content[0].text | contains("official")) and
    ($responses | map(select(.id == 35))[0].result.isError == true) and
    ($responses | map(select(.id == 35))[0].result.content[0].text | contains("resource.service.signalr.net host")) and
    ($responses | map(select(.id == 36))[0].result.isError == true) and
    ($responses | map(select(.id == 36))[0].result.content[0].text | contains("exact official data-messaging.chime.aws host")) and
    ($responses | map(select(.id == 37))[0].result.isError == true) and
    ($responses | map(select(.id == 37))[0].result.content[0].text | contains("exact public-cloud resource.openai.azure.com host"))
' "${RESPONSES}" >/dev/null

printf '%s\n' '{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"baiducloud_api_mutate","arguments":{"auth_scheme":"iotcore-http-pub","service":"iotcore","operation":"PublishHTTP","method":"POST","url":"https://aop098js.iot.gz.baidubce.com.attacker.example/pub","body":{"topic":"commands/device-1","qos":1,"payload_base64":"dHVybi1vbg=="},"force":true}}}' |
  env -i HOME=/nonexistent PATH=/usr/bin:/bin CLOUD_SKILLS_ALLOW_MUTATIONS=1 "${BINARY}" > "${MUTATION_RESPONSES}"

jq -e '
  .id == 32 and
  .result.isError == true and
  (.result.content[0].text | contains("exact single-instance official endpoint"))
' "${MUTATION_RESPONSES}" >/dev/null

echo "protocol smoke: tool contract, mutation gate and pre-credential validation verified"
