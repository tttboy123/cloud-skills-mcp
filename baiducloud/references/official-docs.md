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

The adapter follows `bce-auth-v1/{accessKeyId}/{timestamp}/{expiration}/{signedHeaders}/{signature}`, signs `host` plus applicable standard and `x-bce-*` headers, and carries the IAM/STS session token only inside the signed request.

RTC large-model interaction control-plane resources at `rtc-aiagent.baidubce.com` remain addressable through BCE v1 HTTPS. Its interactive WebSocket is not exposed: the official flow requires a separately purchased/activated `licKey` in addition to AK/SK or the privately minted instance token. That product credential is outside the AKSK/IAM-only MCP entrypoint contract; the gateway also does not export the 24-hour instance token.
