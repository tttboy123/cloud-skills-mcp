# Baidu AI Cloud official references

- API center: https://cloud.baidu.com/doc/API/index.html
- API getting started: https://cloud.baidu.com/doc/APIGUIDE/s/1k1mysgan
- Generate authentication string: https://cloud.baidu.com/doc/Reference/s/njwvz1yfu
- Generate v2 authentication string: https://cloud.baidu.com/doc/Reference/s/hjwvz1y4f-en
- IAM STS: https://cloud.baidu.com/doc/IAM/s/Qjwvyc8ov
- SDK AK/SK and STS credentials: https://cloud.baidu.com/doc/IAM/s/ll9cep8sh
- BCC read-only instance list (`GET /v2/instance`): https://cloud.baidu.com/doc/BCC/s/Xjwvyodmx
- BOS endpoint and virtual-hosted object access: https://cloud.baidu.com/doc/BOS/s/Hjwvyri9y
- BOS GetObject and Range download: https://cloud.baidu.com/doc/BOS/s/xkc5pcmcj
- AK/SK and API Key are distinct credential types: https://cloud.baidu.com/doc/Reference/s/rm5qdjil5
- RTC large-model interaction WebSocket, AK/SK/token connection modes, and license activation: https://cloud.baidu.com/doc/RTC/s/Jmakuvimy
- RTC large-model interaction server APIs and BCE-authenticated instance lifecycle: https://cloud.baidu.com/doc/RTC/s/hm8zjic1q
- RTC WebSocket production best practice and required license/resource binding: https://cloud.baidu.com/doc/RTC/s/Umjcm4buh
- Realtime ASR WebSocket (`appid` + application `appkey`): https://cloud.baidu.com/doc/SPEECH/s/jlbxejt2i
- End-to-end realtime speech (API Key or OAuth access token only): https://cloud.baidu.com/doc/SPEECH/s/nmcytnwei
- Voice-clone streaming TTS (API Key or OAuth access token only): https://cloud.baidu.com/doc/SPEECH/s/qmjiax60m

The adapter follows `bce-auth-v1/{accessKeyId}/{timestamp}/{expiration}/{signedHeaders}/{signature}`, signs `host` plus applicable standard and `x-bce-*` headers, and carries the IAM/STS session token only inside the signed request.

RTC large-model interaction uses the official BCE v1 server API at `rtc-aiagent.baidubce.com` to create and stop an instance. The adapter takes the recommended private `context.token` from create and connects internally to `wss://rtc-aiotgw.exp.bcelive.com/v1/realtime`; it never exposes the alternative `ak`/`sk` query mode. The separately purchased/activated `licKey` is read only from `BCE_RTC_LICENSE_KEY` in the MCP server environment and is sent only after the provider's `[E]:[LIC]:[MUST]` event. Neither license nor the 24-hour instance token enters the MCP schema, result, or audit.
