# cloud-skills-mcp

面向 AI Agent 的六云全资源 Skill + MCP 服务。统一 stdio MCP server 仅通过各云官方 HTTPS/WSS API 访问 AWS、Azure、Google Cloud、Alibaba Cloud、Tencent Cloud 和 Baidu AI Cloud 的已公开资源 API；新增服务或资源不需要再增加一个 Go handler，也不会启动云厂商 CLI 子进程。

![CI](https://github.com/tttboy123/cloud-skills-mcp/actions/workflows/ci.yml/badge.svg)
![License](https://img.shields.io/github/license/tttboy123/cloud-skills-mcp)
![Go](https://img.shields.io/badge/Go-1.25.12-blue)
![Release](https://img.shields.io/github/v/release/tttboy123/cloud-skills-mcp)

## 快速开始

**用户 3 步上手（约 3 分钟，无需任何云凭证即可看到工具）**

```bash
git clone https://github.com/tttboy123/cloud-skills-mcp.git
cd cloud-skills-mcp
./install.sh --bin-dir "$HOME/.local/bin" --skills-dir "$HOME/.codex/skills"
codex mcp add cloud-skills -- "$HOME/.local/bin/cloud-skills-mcp"
```

然后在任意 Codex 会话里让 Agent 执行第一条只读调用：

```text
调用 cloud_provider_status 查看六个云提供商的 adapter 状态。
```

你会看到六家 `available=true`、`credential_status=unverified`——说明 server 已接入，
真实凭证只在首次需要签名的调用时从 server 进程环境解析。注入凭证后即可按
`## 凭证入口` 开始真实只读调用（`<provider>_api_read`）。

**Agentic 快速使用（给 Agent 的一句话）**

本项目以“六套 Skill + 19 个 MCP 工具”的形式供 Agent 直接使用。每种 Agent 只需一条
提示即可进入受控流程，后续由对应 Skill 的 SKILL.md 自动引导：

```text
用 aws 技能只读查看我的云资源，先 cloud_provider_status 再按 SKILL.md 的 Workflow 执行。
```

```text
按 azure 技能的 MCP 参数说明，列出某资源组下的虚拟机（只读，不写、不碰凭证）。
```

每种 Agent 的完整提示词、安全规则与 live gate 清单见
[docs/agentic-quickstart.md](docs/agentic-quickstart.md)。六套 Skill 也可作为
Codex 插件一键安装（见 `## 安装`），安装后 Agent 会自动发现对应 SKILL.md。

## 能力模型

服务暴露 19 个工具：

- `cloud_provider_status`
- 每个云一个 `<provider>_api_discover`
- 每个云一个 `<provider>_api_read`
- 每个云一个 `<provider>_api_mutate`

provider 前缀为 `aws`、`azure`、`gcp`、`alicloud`、`tencent`、`baiducloud`。

`cloud_provider_status.available` 表示 HTTP adapter 可用，不表示云端认证已经成功。`credential_status=unverified` 表示身份链会在首次 API 调用时延迟解析；缺少明确本地材料时可报告 `missing-local-material`。只有成功的只读 live API 调用才证明凭证和 IAM 权限可用。

| 云 | 通用访问层 | 官方身份链 |
|---|---|---|
| AWS | 任意官方 endpoint 的 SigV4 HTTPS；多区域 API 的纯 Go SigV4a；ECR/ECR Public Docker/OCI Registry HTTP；有限原始帧 SigV4 WSS；Connect Health Medical Scribe、Transcribe 双层 EventStream、IoT MQTT、AppSync Events 与 AppSync GraphQL subscriptions、Chime SDK Messaging WebSocket 事件流的受控 IAM WSS | AWS SDK credential chain：IAM Role、Web Identity、profile/SSO、AKSK/STS；ECR Registry token 仅在 server 内部派生 |
| Azure | Azure Identity Bearer Token + ARM/Graph/数据面 HTTPS + OpenAI Realtime、Voice Live WSS + Web PubSub 标准/可靠 JSON/Protobuf/MQTT + SignalR Service Serverless JSON-hub WSS + Event Grid MQTT v5 + Service Bus/Event Hubs AMQP 1.0 WSS | 非 CLI 的 Service Principal、Workload Identity、Managed Identity |
| Google Cloud | Google Auth ADC + `googleapis.com` REST / Artifact Registry 与 `gcr.io` OCI Distribution 1.1 HTTP / raw 或 FileDescriptorSet 驱动的 ProtoJSON gRPC HTTP/2 / Discovery Service + Vertex/Gemini Live WSS + Firebase Realtime Database SSE | ADC、Workload Identity、Service Account、Impersonation、Metadata Identity |
| Alibaba Cloud | ACS3；旧版 RPC/ROA V2；DataHub；OpenSearch V3；MaxCompute ODPS v2/v4；Function Compute 三类 Trigger；OSS v1/v4；SLS v1/v4；MNS；RocketMQ 4.x；ACR 企业版 Docker/OCI Registry；OTS v2/v4；NLS REST/WSS | 官方 credentials-go：AKSK/STS、RAM/OIDC、ECS RAM Role；ACR 与 NLS 临时 token 仅在 server 内部派生 |
| Tencent Cloud | API 3.0 TC3 与 v1 HmacSHA1/HmacSHA256 HTTPS；仍在运行的旧版 qcloud API 2017；COS/CLS 数据面 signed HTTPS；TCR 企业版 Docker/OCI Registry；ASR、虚拟号真人判定、口语评测、实时语音翻译、音色变换、MPS 识别/翻译、MPS TTS、标准实时 TTS、流式文本 TTS 与大模型播客 signed WSS 内部流 | SecretId/SecretKey 或 CAM/STS 临时三元组；TCR 临时 Registry 凭证仅在 server 内部派生；ASR WSS 支持官网 SDK 的临时 token，其余 WSS 按各自文档使用长期 SecretId/SecretKey |
| Baidu AI Cloud | `baidubce.com`/BOS `bcebos.com` signed HTTPS，支持 `bce-auth-v1` 与按 API 选择 v2；CCR 企业版/个人版 Docker/OCI Registry；IoT Core HTTP Publish 与 MQTT 3.1.1/5.0 WSS；RTC AI Agent 由 BCE v1 控制面创建后用内部实例 token 建立 raw/raw16k/PCMA/PCMU/G.722/Opus 双工 WSS | BCE AK/SK、IAM/STS temporary AK/SK/session token；CCR 临时登录与 Bearer token 仅在 server 内部派生；IoT Core 应用权限仅用 IAM AK/SK，派生凭证留在 server 内部；RTC 产品 license 仅由 server 环境注入 |

## 安全边界

- 默认只读。Azure/Baidu 和 GCP REST read 工具只允许 `GET`/`HEAD`/`OPTIONS`；GCP gRPC 以及 AWS/Alibaba/Tencent 的 POST 查询按官方 RPC/action 名称保守分类，无法确认的操作必须走写门。
- 写操作必须同时满足宿主已取得人类批准、server 环境设置 `CLOUD_SKILLS_ALLOW_MUTATIONS=1`、单次调用包含 `force=true`。
- secret/password/token/credential/access-key 类操作还要求 `CLOUD_SKILLS_ALLOW_SENSITIVE=1`。
- 所有六云请求都由进程内 HTTP adapter 构造和签名；统一 Server 不执行 `aws`、`az`、`gcloud`、`aliyun` 或 `tccli`。
- REST 只允许官方 HTTPS 域名和 443 端口；通用调用禁止 redirect。Firebase SSE 仅按官方要求手动接受最多三次 307，且每一跳仍须是 Firebase Database 官方域名并保持数据库路径和查询不变；ACR GET/HEAD 只跟随 provider-owned Azure data/Blob 307；私有 ECR layer GET/HEAD 只跟随与 registry region 精确一致的 `prod-<region>-starport-layer-bucket` S3 307。所有跨数据面跳转都移除 Authorization，签名 Location 不进入输出。调用方不能提供 Authorization、API key、cookie、SAS/signed URL 或 session-token 参数。Azure 非常见数据面可提供公开的 Entra `audience` 标识，但不能提供 token。
- 新发布且尚未进入内置域名表的官方 endpoint，只能由 operator 通过 `CLOUD_SKILLS_<PROVIDER>_ALLOWED_ENDPOINT_HOSTS` 追加逗号分隔的精确 hostname；不接受 URL、端口、子域 wildcard 或 MCP 参数。
- 六云数据面上传均可使用 `body_file`；本地文件只允许位于 `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` 下，且会解析 symlink 后再判断。单文件默认上限 64 MiB，更大对象使用厂商 multipart/chunk API。
- 六云成功响应均可使用 `response_file` 流式写入批准目录中的新文件，正文不会进入 MCP/模型上下文。目标文件绝不覆盖，临时文件以 mode `0600` 写完并同步后才原子发布；默认单次上限 1 GiB，可用 `CLOUD_SKILLS_MAX_RESPONSE_FILE_BYTES` 收紧或调大，更大对象使用厂商 `Range` API 分段下载。
- HTTP 响应有大小上限；结果会做 credential-field redaction。
- `CLOUD_SKILLS_AUDIT_LOG` 写入 mode `0600` JSONL。审计不记录请求 body、headers、response 或 URL query；写操作的预执行审计失败时 fail closed。
- MCP server 不创建、不更新、不导出凭证。凭证只来自各云官方身份链或 operator 注入的 AKSK/IAM 环境。
- STS AssumeRole/session token、登录 token、AccessKey/API key 创建、Service Account key、Graph `addPassword`、云资源 `listKeys` 等凭证签发或导出操作会被硬拒绝，不能用 mutation/sensitive 开关绕过；普通 IAM/RAM/CAM 角色、策略和成员资源管理仍可调用。

`force=true` 和环境开关只是 server 技术门，不能替代 MCP 宿主的人类批准。

## 安装

要求 Go 1.25.12+。统一 Server 不要求安装任何云厂商 CLI；身份由 server 环境、官方 SDK 凭证文件、Workload/Managed Identity 或实例 Metadata 提供。

```bash
git clone https://github.com/tttboy123/cloud-skills-mcp.git
cd cloud-skills-mcp

./install.sh \
  --bin-dir "$HOME/.local/bin" \
  --skills-dir "$HOME/.codex/skills"
```

安装器只构建通过官方 HTTPS/WSS 直连的 `cloud-skills-mcp`，并复制六份 Skill；不会修改 MCP 客户端配置、云凭证或云资源。已有 Skill 默认不覆盖，只有显式传 `--force` 才替换。

只构建仓库内二进制：

```bash
./build-mcp-servers.sh
```

六套 Skill 也打包为开源 Codex 插件（`plugin/`，marketplace 清单在
`.agents/plugins/marketplace.json`），可直接从本仓库安装：

```bash
codex plugin marketplace add https://github.com/tttboy123/cloud-skills-mcp.git
codex plugin add cloud-skills-mcp@cloud-skills-mcp
```

插件包由 `scripts/ci/mapping-audit.sh` 校验：`plugin/skills/<provider>` 与仓库根
的 `<provider>` 技能目录逐文件一致，漂移即失败。插件只包含技能说明；
MCP server 二进制仍按上面的 `install.sh` 安装并注册。

## 注册统一 MCP server

Codex CLI：

```bash
codex mcp add cloud-skills -- "$HOME/.local/bin/cloud-skills-mcp"
```

兼容 stdio MCP 的其他宿主：

```json
{
  "mcpServers": {
    "cloud-skills": {
      "command": "/absolute/path/to/cloud-skills-mcp",
      "env": {}
    }
  }
}
```

把凭证/IAM 环境只注入 server 进程，不要放在 MCP tool arguments 或 prompt 中。

## 凭证入口

```text
AWS       AWS_PROFILE / AWS_ROLE_ARN + AWS_WEB_IDENTITY_TOKEN_FILE /
          AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY (+ AWS_SESSION_TOKEN)
Azure     managed identity / workload identity /
          AZURE_TENANT_ID + AZURE_CLIENT_ID + AZURE_CLIENT_SECRET
GCP       ADC / GOOGLE_APPLICATION_CREDENTIALS / workload identity / metadata identity
Alibaba   RAM/OIDC/ECS role / ALIBABA_CLOUD_ACCESS_KEY_ID + ALIBABA_CLOUD_ACCESS_KEY_SECRET
          (+ ALIBABA_CLOUD_SECURITY_TOKEN for STS)
Tencent   TENCENTCLOUD_SECRET_ID + TENCENTCLOUD_SECRET_KEY
          (+ TENCENTCLOUD_SESSION_TOKEN or TENCENTCLOUD_TOKEN for CAM/STS)
Baidu     BCE_ACCESS_KEY_ID + BCE_SECRET_ACCESS_KEY (+ BCE_SESSION_TOKEN)
          (+ BCE_RTC_LICENSE_KEY only for an entitled RTC AI Agent instance)
```

优先使用最小权限 IAM/RAM/CAM role 或临时凭证；不要给日常只读 server 长期写权限。

## MCP 调用示例

AWS 查询：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"sigv4","service":"ec2","operation":"describe-instances","region":"us-east-1","method":"POST","url":"https://ec2.us-east-1.amazonaws.com/","headers":{"Content-Type":"application/x-www-form-urlencoded"},"body":"Action=DescribeInstances&Version=2016-11-15&MaxResults=20"}}
```

Amazon ECR 私有和公共 Docker/OCI Registry 数据面使用 `auth_scheme=ecr`。调用方仍只提供 AWS SDK identity chain；server 内部对私有 `ecr:GetAuthorizationToken` 或公共 `ecr-public:GetAuthorizationToken` 发起 SigV4 JSON 请求、校验并保留 Registry token，再调用 `/v2`。私有 Registry 使用官方 `Basic <authorizationToken>`，ECR Public HTTP API 使用官方 `Bearer <authorizationToken>`；调用方不能请求、看到或覆盖该 token。私有 classic/FIPS/dual-stack、China classic 以及 Public classic/dual-stack endpoint 都按 account、region、service 精确绑定。ECR Public 按官网限制拒绝 Registry tags API，并固定在 `us-east-1` 取得 token：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"ecr","service":"ecr","operation":"ListTags","region":"us-west-2","method":"GET","url":"https://123456789012.dkr.ecr.us-west-2.amazonaws.com/v2/team/app/tags/list","parameters":{"n":20}}}
```

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"ecr","service":"ecr-public","operation":"GetManifest","region":"us-east-1","method":"GET","url":"https://public.ecr.aws/v2/<registry-alias>/<repository>/manifests/latest"}}
```

Manifest、blob、upload、delete、mount 和 referrers 走同一 Registry HTTP 入口；非 GET/HEAD 方法仍必须通过 mutation gate。私有 layer GET/HEAD 的 307 只允许进入同 region 官方 Starport S3 bucket，请求会剥离 Registry Authorization；可配合 `response_file` 原子下载，预签名 S3 URL 永不返回 MCP。

AWS S3 Multi-Region Access Point 使用 `auth_scheme=sigv4a` 和官方 region set；`region_set` 只定义签名可用区域，不是凭证：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"sigv4a","service":"s3","operation":"get-object","region_set":"us-east-1,us-west-*","method":"GET","url":"https://<alias>.accesspoint.s3-global.amazonaws.com/object","response_file":"/approved/downloads/object.bin"}}
```

S3 `PutObject`/`UploadPart` 的 SigV4 或 SigV4a 流式上传使用 `payload_mode=aws-chunked`。服务端固定使用官方建议的 64 KiB chunk，并生成对应的 HMAC 或定长 DER-ECDSA 链式签名；签名头不能由 MCP 调用方传入：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"sigv4","payload_mode":"aws-chunked","service":"s3","operation":"put-object","region":"us-east-1","method":"PUT","url":"https://<bucket>.s3.us-east-1.amazonaws.com/object","body_file":"/approved/uploads/object.bin","force":true}}
```

需要 S3 签名尾随校验和时使用 `payload_mode=aws-chunked-trailer`，并从 `crc32`、`crc32c`、`crc64nvme`、`sha1`、`sha256` 中选择 `checksum_algorithm`。校验和值由服务端在流式读取时计算，调用方不能提交或覆盖它；`crc64nvme` 是当前 S3 的默认完整性算法：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"sigv4","payload_mode":"aws-chunked-trailer","checksum_algorithm":"crc64nvme","service":"s3","operation":"put-object","region":"us-east-1","method":"PUT","url":"https://<bucket>.s3.us-east-1.amazonaws.com/object","body_file":"/approved/uploads/object.bin","force":true}}
```

多区域 S3 endpoint 使用相同两个 `payload_mode`，把 `auth_scheme` 改为 `sigv4a` 并提供官网定义的 `region_set`；流式请求的 seed、每个 chunk 与可选 trailer 都由同一个服务端派生密钥链签名。

需要 SigV4 EventStream 请求签名的有限或双向 HTTP/2 流使用 `payload_mode=aws-eventstream`。`body_file` 是一个或多个连续的、CRC 有效但尚未添加签名外层的 Amazon EventStream 帧；服务端验证单帧边界后添加 `:date`、链式 `:chunk-signature` 和终止帧。`stream_interval_ms` 可在完整逻辑帧之间节流，不能用 `stream_chunk_bytes` 切断预编码帧；启用节流时必须提供 `response_file`，server 会在上传仍继续时并发读取下行 EventStream，逐帧验证长度和两层 CRC，全部成功后才原子发布。交互式 WebSocket 会话仍属于独立传输族。

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"sigv4","payload_mode":"aws-eventstream","service":"health-agent","operation":"StartMedicalScribeListeningSession","region":"us-west-2","method":"POST","url":"https://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream/","headers":{"x-amzn-medscribe-session-id":"<uuid>","x-amzn-medscribe-domain-id":"<domain-id>","x-amzn-medscribe-subscription-id":"<subscription-id>","x-amzn-medscribe-language-code":"en-US","x-amzn-medscribe-media-encoding":"pcm","x-amzn-medscribe-sample-rate":"16000"},"body_file":"/approved/streams/medical-scribe.events","response_file":"/approved/streams/transcript.events","stream_interval_ms":100,"force":true}}
```

Amazon Transcribe 标准、Medical 和 Call Analytics 的交互式 WebSocket 使用 `auth_scheme=transcribe-ws`。server 内部生成最多 300 秒有效的 SigV4 Upgrade URL，把原始音频编码为 `AudioEvent`，再添加 `:date` 和链式 `:chunk-signature` 外层；下行双层 EventStream 的 CRC 和 JSON 都会验证，预签名 URL 永不返回 MCP：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"transcribe-ws","service":"transcribe","operation":"StartStreamTranscriptionWebSocket","region":"us-west-2","method":"GET","url":"wss://transcribestreaming.us-west-2.amazonaws.com:8443/stream-transcription-websocket","parameters":{"language-code":"en-US","media-encoding":"pcm","sample-rate":16000,"session-id":"session-1"},"body_file":"/approved/audio/input.pcm","response_file":"/approved/results/transcript.ndjson"}}
```

PCM 默认按 100ms 分片；压缩音频或特殊采样布局可显式设置 `stream_chunk_bytes`/`stream_interval_ms`。Medical 改用 `/medical-stream-transcription-websocket`、`StartMedicalStreamTranscriptionWebSocket` 并提供 `specialty`/`type`；Call Analytics 改用 `/call-analytics-stream-transcription-websocket` 和对应 operation。需要 `ConfigurationEvent` 时把配置对象放在 `body`，音频仍放在 `body_file`；它和后述 Connect Health 是允许二者并用的两个受控协议。

AWS IoT Core 的 IAM/SigV4 MQTT 3.1.1/5.0 使用 `auth_scheme=iot-mqtt-ws`。只读 `SubscribeMQTT` 由 server 内部生成五分钟 WSS 签名、协商 `mqtt`、发送 clean-start CONNECT/SUBSCRIBE、验证对应版本的 CONNACK/SUBACK reason code、确认 QoS 1 下行消息，并在消息数或超时边界到达后原子发布 Base64 NDJSON。`protocol_version` 可为 `4`（默认）或 `5`：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"iot-mqtt-ws","service":"iotdevicegateway","operation":"SubscribeMQTT","region":"us-west-2","method":"GET","url":"wss://<account>-ats.iot.us-west-2.amazonaws.com/mqtt","body":{"protocol_version":5,"client_id":"observer-1","subscriptions":[{"topic_filter":"sensors/+/temperature","qos":1}],"max_messages":10,"timeout_seconds":30},"response_file":"/approved/results/mqtt.ndjson"}}
```

需要 WSS 发布、retained/Last Will、退订或持久会话时使用 mutation-only `ClientMQTT`。MQTT 3.1.1 用 `clean_start=false` 恢复/创建 broker 账户配额控制的持久会话；MQTT 5 再用最长 7 天的 `session_expiry_seconds`，并可通过 `disconnect_session_expiry_seconds` 在 DISCONNECT 更新或清理会话。恢复持久会话时即使不新增订阅，AWS 也可能在 CONNACK 后立即下发已存 QoS 1 消息，因此必须同时声明 `max_messages`、`timeout_seconds` 和 `response_file`。`publishes`、`will` 均支持 QoS 0/1、retained、Base64 payload；MQTT 5 还支持 Payload Format、Content Type、Message Expiry、Response Topic、Correlation Data 和保持顺序的 User Properties。QoS 1 出站逐条等待 PUBACK，普通 publish 按单连接 500/s 自动节流，retained publish 按默认账户 50/s 且同 topic 1/s 的官网配额节流；最多 64 条 publish，订阅和退订各受官方单包 8 条配额约束：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"iot-mqtt-ws","service":"iotdevicegateway","operation":"ClientMQTT","region":"us-west-2","method":"GET","url":"wss://<account>-ats.iot.us-west-2.amazonaws.com/mqtt","body":{"protocol_version":5,"client_id":"device-1","clean_start":false,"session_expiry_seconds":3600,"disconnect_session_expiry_seconds":0,"subscriptions":[{"topic_filter":"devices/device-1/in","qos":1}],"unsubscriptions":["devices/device-1/legacy"],"publishes":[{"topic":"devices/device-1/status","qos":1,"retain":true,"payload_base64":"b25saW5l","payload_format":1,"content_type":"text/plain","message_expiry_seconds":60}],"will":{"topic":"devices/device-1/status","qos":1,"retain":true,"payload_base64":"b2ZmbGluZQ==","payload_format":1,"content_type":"text/plain"},"max_messages":1,"timeout_seconds":30},"response_file":"/approved/results/mqtt-client.ndjson","force":true}}
```

MQTT 5 CONNECT 显式限制 Receive Maximum 和 128 KiB Maximum Packet Size，并把 Topic Alias Maximum 保持为 0；收发两侧都支持上述 AWS 官方属性，客户端还验证 broker 的 Maximum QoS、Retain Available、Maximum Packet Size、Receive Maximum、Session Present、Server Keep Alive 和版本化 ACK/DISCONNECT reason code。AWS 不支持 QoS 2 和 Subscription Identifier，官方支持属性表也未列出 Will Delay，因此这些输入会 fail closed。STS session token 遵循 AWS IoT 官网的特殊规则：只在 canonical query 签名完成后追加；签名 URL、Token 和 MQTT 握手控制都不进入 MCP 输出。调用方不能提交 query/header、凭证或无边界会话。

真实数据面 mutation gate 会在同一条精确 topic 上完成 MQTT 5 持久会话、Will、QoS 1 订阅和发布，收到自身发布后用 DISCONNECT expiry 0 清理 session；不会留下 retained message：

```bash
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT=1 \
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_ENDPOINT=wss://<account>-ats.iot.<region>.amazonaws.com/mqtt \
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_REGION=<region> \
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_TOPIC='cloud-skills/live/<unique>' \
CLOUD_SKILLS_LIVE_AWS_IOT_MQTT_CLIENT_ID='cloud-skills-live-unique' \
go test ./internal/mcp/cloud -run TestLiveAWSIoTMQTTMutation -v
```

Kinesis Video Streams WebRTC Signaling 使用 `auth_scheme=kinesisvideo-signaling-ws`。先通过普通 SigV4 HTTPS `GetSignalingChannelEndpoint` 取得 WSS endpoint；随后 mutation 调用只提交 Channel ARN、Master/Viewer 角色和有限的 SDP/ICE JSON 消息。server 使用官方 299 秒 `kinesisvideo` SigV4 查询算法，把 Channel ARN、Viewer Client ID 以及可选 STS token 全部纳入 canonical query，签名 URL 永不返回 MCP：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"kinesisvideo-signaling-ws","service":"kinesisvideo","operation":"ConnectAsViewer","region":"us-west-2","method":"GET","url":"wss://<endpoint-id>.kinesisvideo.us-west-2.amazonaws.com/","body":{"role":"VIEWER","channel_arn":"arn:aws:kinesisvideo:us-west-2:123456789012:channel/demo/1700000000000","client_id":"viewer-1","messages":[{"action":"SDP_OFFER","payload":{"type":"offer","sdp":"v=0..."},"correlation_id":"offer-1"}],"max_messages":32,"timeout_seconds":30},"response_file":"/approved/results/kvs-signaling.ndjson","force":true}}
```

只允许 `SDP_OFFER`、`SDP_ANSWER`、`ICE_CANDIDATE`，server 内部把 JSON payload 编码为官网要求的 Base64，并按 5 TPS 上限节流；Master 的每条消息必须指定 Viewer recipient，Viewer 禁止伪造 recipient。下行只接受官网六种事件，解码 Base64 JSON 到 mode-0600 NDJSON；未关联或失败的 `STATUS_RESPONSE`、畸形/未知事件、二进制帧和超界会话都会中止原子输出。该入口不实现 WebRTC RTP/媒体平面。

AWS AppSync Events 的 IAM 频道订阅使用 `auth_scheme=appsync-event-ws`。server 从标准 realtime host 推导官方 HTTP `/event` host（自定义域名必须由 operator allowlist 批准），分别对连接正文 `{}` 和订阅频道正文执行 `appsync` SigV4，把授权对象只放入内部 `header-<Base64URL>` 子协议和 `subscribe.authorization`，然后按消息数或超时收集并显式退订：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"appsync-event-ws","service":"appsync","operation":"EventSubscribe","region":"us-east-1","method":"GET","url":"wss://<api-id>.appsync-realtime-api.us-east-1.amazonaws.com/event/realtime","body":{"channel":"/news/latest","max_messages":10,"timeout_seconds":30},"response_file":"/approved/results/appsync-events.ndjson"}}
```

调用方不能提交 API key、JWT、Authorization、连接 header 或 subscription ID；后者由 server 随机生成。输出只保留已验证的 `data` 事件，不包含握手/订阅签名或 STS token。WebSocket 发布已有等价的官方 SigV4 HTTPS `/event` 路径，仍走普通写入审批门。

传统 AWS AppSync GraphQL subscription 使用独立的 `auth_scheme=appsync-graphql-ws`。标准域名连接 `/graphql`，自定义域名连接 `/graphql/realtime` 且必须在 operator allowlist 中。server 分别签名官方 HTTPS `/graphql/connect` 的 `{}` 和 HTTPS `/graphql` 的精确 GraphQL request，完成 `connection_init`、`start`、`start_ack`、`data`、`stop`、`complete` 生命周期，只接受单个 subscription operation：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"appsync-graphql-ws","service":"appsync","operation":"GraphQLSubscribe","region":"us-east-1","method":"GET","url":"wss://<api-id>.appsync-realtime-api.us-east-1.amazonaws.com/graphql","body":{"query":"subscription OnMessage($room: ID!) { onMessage(room: $room) { id text } }","variables":{"room":"room-1"},"max_messages":10,"timeout_seconds":30},"response_file":"/approved/results/appsync-graphql.ndjson"}}
```

IAM Authorization 和 STS token 只进入内部动态 `header-<Base64URL>` 子协议及 `start.extensions.authorization`。调用方不能提交 query/header、API key、JWT、operation ID、mutation/query operation 或无边界会话；成功后才原子发布经过验证的 `data` payload NDJSON。

Amazon IVS Chat 消息数据面使用 `auth_scheme=ivs-chat-ws`，不是普通 SigV4 Upgrade。server 先用标准 AWS IAM/AKSK 链对固定 `POST /CreateChatToken` 执行 `ivschat` SigV4，再把一次性 token 仅作为内部 WebSocket subprotocol。只读 `SubscribeChat` 获得零显式 capability 的观察 token；`ClientChat` 只获得 `SEND_MESSAGE`；`ModerateChat` 只获得实际需要的 `DELETE_MESSAGE`/`DISCONNECT_USER`，且后者还要求 sensitive gate：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"ivs-chat-ws","service":"ivschat","operation":"ClientChat","region":"us-west-2","method":"GET","url":"wss://edge.ivschat.us-west-2.amazonaws.com","body":{"room_identifier":"arn:aws:ivschat:us-west-2:123456789012:room/demo","user_id":"agent","session_duration_minutes":1,"messages":[{"action":"SEND_MESSAGE","content":"hello","request_id":"message-1"}],"max_messages":1,"timeout_seconds":30},"response_file":"/approved/results/ivs-chat.ndjson","force":true}}
```

入口只接受官网当前列出的 7 个区域 endpoint，执行每连接 10 请求/秒节流，并验证 room ARN、用户、会话时长、1 KiB attributes、500 code points 消息、MESSAGE/EVENT/ERROR 帧及原子输出。调用方不能提交 token、capabilities、header/query 或任意 action；一次性 token、SigV4 Authorization 与 STS token 都不会进入 MCP 返回、错误或审计。

Amazon Lex V2 流式会话使用 `auth_scheme=lex-v2-conversation`，不是普通 HTTPS 请求：server 先校验官方 `https://runtime-v2-lex.<region>.amazonaws.com` origin 与 bot/alias/locale/session URI，再用 AWS IAM/AKSK 链对请求和每个 `STREAMING-AWS4-HMAC-SHA256-EVENTS` 帧做内部签名，首个事件固定为 `ConfigurationEvent`。TEXT 模式随后发送 1–64 条 `TextInputEvent`；AUDIO 模式支持官方 8 kHz lpcm `audio_base64`（≤256 KiB）、单字符 `dtmf`（`A-D0-9#*`）与可选文本：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"lex-v2-conversation","service":"lex","operation":"StartConversation","region":"us-west-2","method":"POST","url":"https://runtime-v2-lex.us-west-2.amazonaws.com","body":{"bot_id":"ABCDEFGHIJ","bot_alias_id":"alias-1","locale_id":"en_US","session_id":"session-1","conversation_mode":"AUDIO","audio_base64":"<base64 lpcm>","dtmf":"12#","texts":[{"text":"hello","event_id":"lex-evt-1"}],"max_events":64,"timeout_seconds":60},"response_file":"/approved/results/lex.ndjson","force":true}}
```

响应只接受官方事件（TextResponse/Transcript/Heartbeat/IntentResult，AUDIO 模式另有 AudioResponse/PlaybackInterruption），未知事件、异常帧、含凭据字段的帧与模式不一致的音频帧一律 fail closed；EOF 与超时只发布已到达内容，失败不落盘。调用方不能提交签名、token、capabilities、header/query 或任意 action；签名材料与 STS token 不会进入 MCP 返回、错误或审计。

Amazon Chime SDK Messaging 事件流使用 `auth_scheme=chime-messaging-ws`，是纯只读订阅：server 先用 AWS IAM/AKSK 链对固定 `GET https://messaging-chime.<region>.amazonaws.com/endpoints/messaging-session` 做 `chime` SigV4，校验返回的官方 `wss://data-messaging.chime.aws` endpoint，再对 `/connect` 用 `chime` 服务做内部 SigV4 预签名（bounded `X-Amz-Expires`、必填 `userArn`/`sessionId`、可选 `prefetch-on=connect`，STS token 仅在签名 query 内部），最后连接并收集事件。调用方只需提供官方 AppInstanceUser ARN、唯一 session id、可选 prefetch 与 expires、消息数/超时边界；`chime:GetMessagingSessionEndpoint` 与 `chime:Connect` 权限由 operator IAM 提供。事件信封（`Headers` + `Payload`）按官方 `x-amz-chime-event-type` 表（SESSION_ESTABLISHED、CHANNEL_DETAILS、*_CHANNEL_MESSAGE、*_CHANNEL_MEMBERSHIP、*_CHANNEL 等 18 类）与 `x-amz-chime-message-type`（STANDARD/CONTROL/SYSTEM）严格校验，Payload 解码为 JSON 后原子写入 NDJSON；未知事件、异常信封、非法 payload 与含签名材料的输出全部 fail closed：

```json
{"name":"aws_api_read","arguments":{"auth_scheme":"chime-messaging-ws","service":"chime-messaging","operation":"SubscribeMessages","region":"us-east-1","method":"GET","url":"wss://data-messaging.chime.aws/connect","body":{"user_arn":"arn:aws:chime:us-east-1:123456789012:app-instance/694d2099-cb1e-463e-9d64-697ff5b8950e/user/johndoe","session_id":"session-1","prefetch_on_connect":true,"connect_expires_seconds":60,"max_messages":32,"timeout_seconds":30},"response_file":"/approved/results/chime-messaging.ndjson"}}
```

其他使用标准 SigV4 Upgrade、而消息帧本身不需要额外 AWS 链式签名的官方双向 WebSocket 使用 `auth_scheme=sigv4-ws`。通用入口最多收发 256 帧或运行 300 秒；Bedrock AgentCore 目标还执行其官方 32 KiB 单帧上限。例如 `InvokeAgentRuntimeWithWebSocketStream`：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"sigv4-ws","service":"bedrock-agentcore","operation":"InvokeAgentRuntimeWithWebSocketStream","region":"us-west-2","method":"GET","url":"wss://bedrock-agentcore.us-west-2.amazonaws.com/runtimes/<percent-encoded-runtime-arn>/ws?qualifier=prod","headers":{"X-Amzn-Bedrock-AgentCore-Runtime-Session-Id":"session-123456789012345678901234567890"},"body":{"messages":[{"type":"json","data":{"inputText":"hello"}}],"max_messages":10,"timeout_seconds":30},"response_file":"/approved/results/agentcore.ndjson","force":true}}
```

该通用模式在进程内对精确 host/path/query 与非凭证 header 执行 SigV4，最多发送 256 个 `json|text|binary` 帧并收集 256 个响应或 300 秒；文本和 Base64 二进制响应写入原子 NDJSON。由于任意双向帧可能产生副作用，它永远不允许走 read 工具。Transcribe、IoT、AppSync 以及要求每个 EventStream 帧继续链式签名的协议仍使用各自专用模式。

Amazon Connect Health ambient documentation 使用专用 `auth_scheme=connect-health-ws`，不会误走原始帧通道。server 只接受官网列出的 `us-east-1|us-west-2` endpoint，把六个会话参数放进内部 60 秒 SigV4 预签名 Upgrade，然后依次发送链式签名的 `configurationEvent`、原始 PCM/FLAC `binaryAudioEvent` 和 `END_OF_SESSION`；只有服务端以 WebSocket 1000 正常关闭后，才原子发布合法 `transcriptEvent` NDJSON：

```json
{"name":"aws_api_mutate","arguments":{"auth_scheme":"connect-health-ws","service":"health-agent","operation":"StartMedicalScribeListeningSession","region":"us-west-2","method":"GET","url":"wss://streaming.health-agent.us-west-2.api.aws/medical-scribe-stream-websocket","parameters":{"session-id":"<uuid>","domain-id":"<dom-or-hai-id>","subscription-id":"<sub-id>","language-code":"en-US","sample-rate":16000,"media-encoding":"pcm"},"body":{"postStreamActionSettings":{"outputS3Uri":"s3://<bucket>/<prefix>","clinicalNoteGenerationSettings":{"noteTemplateSettings":{"managedTemplate":{"templateType":"PHYSICAL_SOAP"}}}}},"body_file":"/approved/audio/visit.pcm","response_file":"/approved/results/medical-scribe.ndjson","force":true}}
```

该 API 的 IAM action 是 Write，并会生成会话与 S3 临床文档产物，所以固定要求 mutation 审批。音频和 encounter context 可能包含 PHI；调用前必须按组织政策取得并记录合法同意，输出还必须由受训医疗人员复核。AKSK/STS、预签名 URL和逐帧签名不会进入 MCP 输出。

Azure 查询：

```json
{"name":"azure_api_read","arguments":{"method":"GET","url":"https://management.azure.com/subscriptions/<id>/resources?api-version=2021-04-01","subscription":"<id>"}}
```

Azure Container Registry 数据面使用 `auth_scheme=acr`，不把 Entra token 直接发送给 Registry API。server 先用非 CLI Azure Identity 获取 `https://containerregistry.azure.net/.default`，再按 ACR 官方 OAuth2 合同内部完成 Entra access token → ACR refresh token → 最小作用域 access token；三类 token 都不会进入 MCP 参数、结果、错误或审计。`acr_scope` 必须与 URL 中的 repository 精确一致，并按当前官方权限族绑定路径和方法：catalog 为 `registry:catalog:*`，软删除 catalog 为 `registry:deleted_catalog:*`，Docker/OCI 内容读取、写入、删除分别为 `pull`、`push`（也接受官方组合 `pull,push`）和 `delete`，`/acr/v1` 元数据读取、更新、删除分别为 `metadata_read`、`metadata_write,metadata_read` 和 `delete`，软删除清单读取与 manifest 恢复分别为 `deleted_read` 和 `deleted_read,deleted_restore`。跨 repository blob mount 额外使用与 `from` 精确匹配的 `acr_source_scope=repository:<source>:pull`，作为第二个内部 OAuth scope。公共登录端点仅接受 `<registry>.azurecr.io` 或官方 regional login endpoint；Private Endpoint 仍使用公共登录名并由 VNet DNS 私下解析。Blob GET/HEAD 的官方 307 可在内部跟随到同一 registry 的 dedicated data endpoint 或 Azure Blob，跳转请求会移除 Authorization，签名 Location 永不返回 MCP：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"acr","service":"acr","operation":"ListTags","method":"GET","url":"https://<registry>.azurecr.io/v2/<repository>/tags/list","parameters":{"n":100},"acr_scope":"repository:<repository>:pull"}}
```

Azure OpenAI Realtime 使用 `auth_scheme=realtime-ws`，server 通过非 CLI Azure Identity 获取官网推荐的 `https://ai.azure.com/.default` Entra token，并且只把它放入内部 WSS Upgrade 的 Authorization header。当前官网仅发布公共 Azure 的单标签 `wss://<resource>.openai.azure.com` Realtime 合同；Azure Government 官网说明其模型清单包含该云提供的全部 Azure OpenAI 模型，而清单中没有 Realtime 型号，Azure China 也没有发布对应的 Foundry Realtime 端点合同，因此 `.azure.us`、`.azure.cn`、多级子域和相似域名都会被拒绝。调用方提供官方 client event 对象/数组，或用 `body_file` 提供逐行 JSON 事件；server 会按 `response.create`/`response.done` 和 audio commit/transcription completed 数量等待有限终态，成功后才原子发布 NDJSON：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"realtime-ws","service":"openai","operation":"RealtimeResponse","method":"GET","url":"wss://<resource>.openai.azure.com/openai/v1/realtime","parameters":{"model":"<deployment>"},"body":[{"type":"conversation.item.create","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"Please assist the user."}]}},{"type":"response.create"}],"response_file":"/approved/results/realtime.ndjson"}}
```

Azure Voice Live 使用 `auth_scheme=voice-live-ws`，直接连接官网公共 Azure 的单标签 `<resource>.services.ai.azure.com/voice-live/realtime` 或旧版 Cognitive Services WSS。Azure Speech 主权云官网明确把 Voice Live 同时列为 Azure Government 和 21Vianet China 的不支持功能，因此 `.azure.us`、`.azure.cn`、多级子域和相似域名都会在取凭证前失败。server 根据公共 host 获取 `https://ai.azure.com/.default` 或 `https://cognitiveservices.azure.com/.default` Entra token；model 会话使用只读工具，带 `agent_id/project_id` 的配置型 Agent 会话固定走 mutation gate。调用参数必须包含 `api-version` 和 `model`，或 Agent ID/Project ID 二元组；response、transcription、session 操作分别要求对应的 `response.create`、audio commit 或 `session.update` 终态。Avatar WebRTC 会产生媒体面 ICE credential，MCP 不暴露该媒体平面，若服务端事件携带 credential 会在原子落盘前失败。

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"voice-live-ws","service":"voice-live","operation":"VoiceLiveResponse","method":"GET","url":"wss://<resource>.services.ai.azure.com/voice-live/realtime","parameters":{"api-version":"2026-04-10","model":"gpt-realtime"},"body":[{"type":"session.update","session":{"voice":{"name":"en-US-AvaNeural","type":"azure-standard"},"modalities":["text","audio"]}},{"type":"response.create"}],"response_file":"/approved/results/voice-live.ndjson"}}
```

Azure OpenAI Chat Completions 流式输出使用 `auth_scheme=openai-chat-stream`。server 用非 CLI Azure Identity 获取官网要求的 `https://cognitiveservices.azure.com/.default` Entra token，把它只放进内部 POST 的 Authorization header，并强制注入 `"stream": true`，因此响应按官方合同是 `text/event-stream` 的 `chat.completion.chunk` 事件，以 `data: [DONE]` 结束。调用方提供精确公共云的 `https://<resource>.openai.azure.com/openai/deployments/<deployment>/chat/completions` URL、日期结构的 `api_version`，以及有限 body plan：`model`（必须等于 URL 中的 deployment）、1–128 条 bounded `messages`（`system|user|assistant|developer` role，字符串或 text part 内容）、可选 `max_tokens`/`temperature`、`max_events` 和 `timeout_seconds`。caller 的 `stream`、api-key、认证字段与 query/header 都会被拒绝；`.azure.us`、`.azure.cn`、`privatelink`、多级子域和相似域名在取凭证前失败。每个 chunk 都按官方形状校验（object、id、choices/delta、finish_reason），未知事件或 credential-bearing chunk 整体失败，达到 `max_events` 或超时后原子发布 NDJSON：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"openai-chat-stream","service":"openai","operation":"StreamChatCompletions","method":"POST","url":"https://<resource>.openai.azure.com/openai/deployments/<deployment>/chat/completions","api_version":"2024-06-01","body":{"model":"<deployment>","messages":[{"role":"user","content":"Summarize cloud skills in one sentence."}],"max_events":32,"timeout_seconds":30},"response_file":"/approved/results/chat-stream.ndjson"}}
```

Azure OpenAI Responses 流式输出使用 `auth_scheme=openai-responses-stream`（Responses API 已 GA）。server 同样用非 CLI Azure Identity 获取 REST 参考页声明的 `https://cognitiveservices.azure.com/.default` scope，把它只放进内部 POST 的 Authorization header，并强制注入 `"stream": true` 与 `"store": false`（不保留任何服务端响应状态），因此响应按官方合同是 `text/event-stream` 的 `ResponseStreamEvent` 事件，以 `response.completed`/`response.incomplete` 结束。调用方提供精确公共云的 `https://<resource>.openai.azure.com/openai/v1/responses` URL、可选的 `api_version`（`v1`、`preview`、日期结构版，或省略走 v1 GA 默认），以及有限 body plan：`model`（一个有界 deployment 名）、`input`（1–65536 字节字符串或 1–64 条 `user|system|developer|assistant` message item，字符串或 `input_text` part 内容）、可选 `max_output_tokens`/`temperature`、`max_events` 和 `timeout_seconds`。caller 的 `stream`/`store`、api-key、认证字段与 query/header 都会被拒绝；`.azure.us`、`.azure.cn`、`privatelink`、多级子域和相似域名在取凭证前失败。每个事件按官方 `ResponseStreamEvent` 联合类型校验（type 白名单、必需 `sequence_number`、bounded delta/text 与终止事件 `response.id`/`object`），`error`/`response.failed` 终止、未知或 credential-bearing 事件、空流、缺终止事件都会原子失败：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"openai-responses-stream","service":"openai","operation":"StreamResponses","method":"POST","url":"https://<resource>.openai.azure.com/openai/v1/responses","api_version":"v1","body":{"model":"<deployment>","input":"Summarize cloud skills in one sentence.","max_events":32,"timeout_seconds":30},"response_file":"/approved/results/responses-stream.ndjson"}}
```

Azure Web PubSub 使用 `auth_scheme=webpubsub-ws`。Private Endpoint 按官网要求仍使用同一个 `<resource>.webpubsub.azure.com` 公共资源名，由 VNet DNS 解析到私网地址；禁止直连 `privatelink.webpubsub.azure.com` 或 `<resource>.privatelink.webpubsub.azure.com`。server 先用 Entra scope `https://webpubsub.azure.com/.default` 调固定 `:generateToken` 数据面 API，再把五分钟 client token 仅放进内部 WSS Authorization header。`body.protocol` 可选 `json`（默认）、`json-reliable`、`protobuf` 或 `protobuf-reliable`，并精确协商对应的 `*.webpubsub.azure.v1` 子协议。Protobuf 模式继续接收 JSON 逻辑消息，由 server 按官方 proto3 字段号转换成二进制帧并把下行帧解码成安全 NDJSON；支持 join/leave、group publish、event、ping、text/binary/Protobuf Any、stream start/data/end，以及 ack/message/system/pong/stream ack/nack/closed。可靠模式自动发送高精度 `uint64` sequence ack，丢弃已确认重复消息，并在断线后用内部 recovery state 重连、重发未确认 publisher 消息。所有会话都有消息数和时间边界并固定走 mutation gate：

```json
{"name":"azure_api_mutate","arguments":{"auth_scheme":"webpubsub-ws","service":"webpubsub","operation":"ClientConnect","method":"GET","url":"wss://<resource>.webpubsub.azure.com/client/hubs/<hub>","body":{"roles":["webpubsub.joinLeaveGroup.<group>"],"groups":["<group>"],"messages":[{"type":"joinGroup","group":"<group>","ackId":1}],"max_messages":32,"timeout_seconds":30},"response_file":"/approved/results/webpubsub.ndjson","force":true}}
```

要启用可靠模式，在同一 body 使用 `"protocol":"json-reliable"` 或 `"protobuf-reliable"`，并为支持 acknowledgement 的 join/leave/publish/event 请求提供唯一正整数 `ackId`。Ping 和 stream 控制使用各自的 pong/stream acknowledgement。Protobuf `dataType="protobuf"` 的 `data` 形如 `{"typeUrl":"type.googleapis.com/<package.Message>","value":"<base64 protobuf>"}`；binary data 也是规范 Base64。Entra token、临时 client token、reconnection token 和 token API 响应都不会进入 MCP 结果或审计；caller 不能提供 access token、Authorization、recovery query、非官方 host/path 或无界会话。

Protobuf 可靠会话示例：

```json
{"name":"azure_api_mutate","arguments":{"auth_scheme":"webpubsub-ws","service":"webpubsub","operation":"ClientConnect","method":"GET","url":"wss://<resource>.webpubsub.azure.com/client/hubs/<hub>","body":{"protocol":"protobuf-reliable","roles":["webpubsub.sendToGroup.<group>"],"messages":[{"type":"sendToGroup","group":"<group>","ackId":1,"dataType":"protobuf","data":{"typeUrl":"type.googleapis.com/example.Message","value":"CAE="}}],"max_messages":32,"timeout_seconds":30},"response_file":"/approved/results/webpubsub-protobuf.ndjson","force":true}}
```

Azure Web PubSub MQTT 使用 `auth_scheme=webpubsub-mqtt-ws`，支持 MQTT 3.1.1（`protocol_version=4`，默认）和 MQTT 5.0（`protocol_version=5`）。只读入口 `SubscribeMQTT` 自动从 1–8 个精确 topic 推导最小 `joinLeaveGroup` 权限，支持 QoS 0/1/2、有限消息数、broker keepalive 和原子 Base64 NDJSON：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"webpubsub-mqtt-ws","service":"webpubsub","operation":"SubscribeMQTT","method":"GET","url":"wss://<resource>.webpubsub.azure.com/clients/mqtt/hubs/<hub>","body":{"protocol_version":5,"client_id":"Observer123","subscriptions":[{"topic_filter":"room/temperature","qos":2}],"subscription_identifier":7,"keep_alive_seconds":30,"max_messages":32,"timeout_seconds":30},"response_file":"/approved/results/webpubsub-mqtt.ndjson"}}
```

需要发布、持久会话或 Last Will 时，使用 mutation 入口 `ClientMQTT`。它可组合订阅与最多 8 条初始 Base64 publish，完成 QoS 1 PUBACK 和 QoS 2 PUBREC/PUBREL/PUBCOMP 双向状态机；MQTT 5 还支持 `session_expiry_seconds`（本服务恢复保证窗口内最多 30 秒）、subscription identifier、payload format/content type/message expiry、Will delay，以及 broker 的 Receive Maximum、Maximum Packet Size、Maximum QoS、Server Keep Alive 协商：

```json
{"name":"azure_api_mutate","arguments":{"auth_scheme":"webpubsub-mqtt-ws","service":"webpubsub","operation":"ClientMQTT","method":"GET","url":"wss://<resource>.webpubsub.azure.com/clients/mqtt/hubs/<hub>","body":{"protocol_version":5,"client_id":"Publisher123","clean_start":false,"session_expiry_seconds":30,"subscriptions":[{"topic_filter":"room/in","qos":2}],"publishes":[{"topic":"room/out","qos":2,"payload_base64":"aGVsbG8=","payload_format":1,"content_type":"text/plain","message_expiry_seconds":30}],"will":{"topic":"room/status","qos":1,"payload_base64":"b2ZmbGluZQ==","payload_format":1,"content_type":"text/plain","delay_seconds":1},"keep_alive_seconds":30,"max_messages":1,"timeout_seconds":30},"response_file":"/approved/results/webpubsub-mqtt-client.ndjson","force":true}}
```

Wildcard、retained、shared subscription 和 topic alias 是 Azure Web PubSub 本身未支持的 MQTT 特性。调用方 token、username/password 与客户端证书不作为 MCP 参数；server 只用 operator 配置的 Entra 身份生成五分钟 `clientType=MQTT` token，并将临时 token 保留在内部 WSS Authorization header。

Azure Event Grid Namespace MQTT broker 使用独立的 `auth_scheme=eventgrid-mqtt-ws`，仅接受 `wss://<namespace>.<region>.eventgrid.azure.net/mqtt` 或 operator 明确固定的 custom domain。server 用非 CLI Azure Identity 请求 `https://eventgrid.azure.net/.default`，将 JWT 只编码进 MQTT v5 CONNECT 的 `OAUTH2-JWT` Authentication Data；WebSocket URL、headers、NDJSON、MCP 结果和审计均不携带 token。只读订阅示例：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"eventgrid-mqtt-ws","service":"eventgrid","operation":"SubscribeMQTT","method":"GET","url":"wss://<namespace>.<region>.eventgrid.azure.net/mqtt","body":{"client_id":"observer-1","username":"workload-app","subscriptions":[{"topic_filter":"$share/processors/orders/+","qos":1}],"subscription_identifier":7,"receive_maximum":64,"maximum_packet_size":524288,"topic_alias_maximum":10,"keep_alive_seconds":30,"max_messages":32,"timeout_seconds":30},"response_file":"/approved/results/eventgrid-mqtt.ndjson"}}
```

发布、持久会话或 Last Will 使用 `ClientMQTT` mutation。该入口实现 Event Grid 当前公开的 MQTT v5 QoS 0/1、最长 8 小时 session expiry、retained/Will、PUBLISH user properties、response topic/correlation data、message expiry、topic alias、flow control、assigned client ID、shared subscriptions、subscription identifiers、negative acknowledgement/server disconnect，以及可选的内部 AUTH reason 25 JWT 续期；最多 512 KiB packet、topic alias 10、keepalive 1160 秒，所有输出仍原子写入 mode-0600 Base64 NDJSON：

```json
{"name":"azure_api_mutate","arguments":{"auth_scheme":"eventgrid-mqtt-ws","service":"eventgrid","operation":"ClientMQTT","method":"GET","url":"wss://<namespace>.<region>.eventgrid.azure.net/mqtt","body":{"client_id":"publisher-1","username":"workload-app","clean_start":false,"session_expiry_seconds":3600,"publishes":[{"topic":"orders/42","qos":1,"retain":true,"payload_base64":"Y3JlYXRlZA==","payload_format":1,"content_type":"text/plain","message_expiry_seconds":60,"response_topic":"orders/42/reply","correlation_data_base64":"cmVxdWVzdC00Mg==","user_properties":{"kind":"created"},"topic_alias":1}],"keep_alive_seconds":30,"max_messages":0,"timeout_seconds":30},"response_file":"/approved/results/eventgrid-publish.ndjson","force":true}}
```

Azure Service Bus 数据面使用 `auth_scheme=servicebus-amqp-ws`，固定连接精确的 `wss://<namespace>.<servicebus-suffix>/$servicebus/websocket` 并协商 `amqp`。当前允许的现役官方后缀只有公共云 `servicebus.windows.net`、美国政府云 `servicebus.usgovcloudapi.net` 和由世纪互联运营的中国云 `servicebus.chinacloudapi.cn`；私有链接别名、自定义域名、相似后缀和已退役的德国云均拒绝。server 通过与目标云匹配的非 CLI Azure Identity authority 获取固定 Service Bus scope，Azure SDK 在进程内完成 SASL anonymous、CBS claim 与 AMQP 1.0 connection/session/link；调用方不能提供 bearer、SAS、connection string、WebSocket header 或 query。`PeekMessages` 是只读操作（最多 250 条）；发送、计划/取消、receive/settlement、deferred message 和 session state 都走 mutation gate。PeekLock 消息必须在同一次调用中显式 `complete|abandon|defer|dead_letter`，或选择破坏性的 `receive_and_delete`；lock token 和 session lock 永不输出：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"servicebus-amqp-ws","service":"servicebus","operation":"PeekMessages","method":"GET","url":"wss://<namespace>.servicebus.windows.net/$servicebus/websocket","body":{"topic":"orders","subscription":"analytics","sub_queue":"dead_letter","max_messages":25,"from_sequence_number":1,"timeout_seconds":30},"response_file":"/approved/results/servicebus-peek.ndjson"}}
```

```json
{"name":"azure_api_mutate","arguments":{"auth_scheme":"servicebus-amqp-ws","service":"servicebus","operation":"ReceiveMessages","method":"GET","url":"wss://<namespace>.servicebus.windows.net/$servicebus/websocket","body":{"queue":"orders","max_messages":10,"timeout_seconds":30,"settlement":"complete"},"response_file":"/approved/results/servicebus-receive.ndjson","force":true}}
```

Azure Event Hubs 数据面使用 `auth_scheme=eventhubs-amqp-ws` 和相同的三套现役官方 Service Bus namespace AMQP-over-WSS endpoint；token 只进入内部 CBS。`GetProperties` 与按 partition 的 `ReceiveEvents` 是只读，`SendEvents` 是 mutation。receive 必须指定唯一的 earliest/latest/offset/sequence/enqueued-time 起点、1–256 条上限和 1–300 秒超时；owner/epoch capability 不向 MCP 暴露。发送最多 100 个 Base64 event，可指定 `partition_id` 或 `partition_key` 二选一：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"eventhubs-amqp-ws","service":"eventhubs","operation":"ReceiveEvents","method":"GET","url":"wss://<namespace>.servicebus.windows.net/$servicebus/websocket","body":{"event_hub":"telemetry","consumer_group":"$Default","partition_id":"0","start_position":{"earliest":true},"max_events":25,"timeout_seconds":30,"prefetch":25},"response_file":"/approved/results/eventhubs.ndjson"}}
```

Azure SignalR Service Serverless 使用 `auth_scheme=signalr-ws`，只接受精确的公共云 `wss://<resource>.service.signalr.net/client/?hub=<hub>`。server 用非 CLI Azure Identity 请求 `https://signalr.azure.com/.default`，调用官方固定的 `POST /api/hubs/<hub>/:generateToken?api-version=2022-11-01&minutesToExpire=5&userId=...`，再把 client token 仅放进内部 WSS 的 `access_token` query 参数；Entra token、client token 与握手帧永不进入输出。hub 名遵循 swagger 契约（字母开头、仅字母数字和下划线），`privatelink`、嵌套、数字开头、lookalike 与主权云猜测全部在凭证解析前拒绝。只读 `Subscribe` 自动完成 `{"protocol":"json","version":1}` 握手、收集 server-to-client Invocation，并对每个带 `invocationId` 的调用回发 void Completion；mutation-only `Invoke` 发送 1–64 个带唯一 `id`/`target`/`arguments` 的 type-1 Invocation，按 `id` 关联 StreamItem/Completion，error Completion 或缺失终态整体失败。所有输出原子写入 mode-0600 NDJSON：

```json
{"name":"azure_api_read","arguments":{"auth_scheme":"signalr-ws","service":"signalr","operation":"Subscribe","method":"GET","url":"wss://<resource>.service.signalr.net/client/?hub=<hub>","body":{"user_id":"observer","max_messages":32,"timeout_seconds":30},"response_file":"/approved/results/signalr-subscribe.ndjson"}}
```

```json
{"name":"azure_api_mutate","arguments":{"auth_scheme":"signalr-ws","service":"signalr","operation":"Invoke","method":"GET","url":"wss://<resource>.service.signalr.net/client/?hub=<hub>","body":{"invocations":[{"id":"inv-1","target":"SendMessage","arguments":[{"text":"hello"}]}],"max_messages":4,"timeout_seconds":30},"response_file":"/approved/results/signalr-invoke.ndjson","force":true}}
```

```json
{"name":"azure_api_mutate","arguments":{"auth_scheme":"eventhubs-amqp-ws","service":"eventhubs","operation":"SendEvents","method":"GET","url":"wss://<namespace>.servicebus.windows.net/$servicebus/websocket","body":{"event_hub":"telemetry","partition_key":"device-1","events":[{"body_base64":"eyJ2YWx1ZSI6MX0=","content_type":"application/json","message_id":"event-1","properties":{"device":"device-1"}}]},"response_file":"/approved/results/eventhubs-send.ndjson","force":true}}
```

GCP 查询：

```json
{"name":"gcp_api_read","arguments":{"method":"GET","url":"https://compute.googleapis.com/compute/v1/projects/<project>/aggregated/instances","project":"<project>"}}
```

Google Artifact Registry 与仍由 Artifact Registry 承载的 `gcr.io` 仓库使用 `auth_scheme=artifact-registry`。server 直接从 ADC 取得短期 OAuth access token，并只把 `Bearer` header 发送给精确的 `<location>-docker.pkg.dev`、`gcr.io`、`us.gcr.io`、`eu.gcr.io` 或 `asia.gcr.io` Registry origin；不调用 `gcloud`、Docker 或 credential helper，也不向 MCP 返回 token。入口覆盖 OCI Distribution 1.1 的 version、manifest、blob、tag、referrer、cross-repository mount 与 monolithic upload 路径，并按 Google 官方限制拒绝 Docker chunked `PATCH` upload：

```json
{"name":"gcp_api_read","arguments":{"auth_scheme":"artifact-registry","service":"artifact-registry","operation":"ListTags","method":"GET","url":"https://us-west1-docker.pkg.dev/v2/<project>/<repository>/<image>/tags/list","parameters":{"n":20},"project":"<quota-project>"}}
```

写 manifest、删除内容、跨仓库 mount 或上传 blob 仍走 `gcp_api_mutate(force=true)`。monolithic blob upload 使用 `/blobs/uploads/`、`digest` 参数和受控 `body_file`；若 Registry 先返回 `202 + Location`，server 会把同一完整文件 PUT 到严格同源的内部 upload location，并隐藏该 capability。调用方不能访问 `/v2/token`、提供 Authorization、OAuth/query token 或 Registry credential。

Firebase Realtime Database 的长连接读取使用 `auth_scheme=firebase-sse`。server 通过 ADC 单独请求官方要求的 `firebase.database` 与 `userinfo.email` scopes，把 Bearer token 只放在内部 Authorization header，并把有限 `put`/`patch` 事件原子写入 NDJSON。仅接受官方 `firebaseio.com`、`europe-west1.firebasedatabase.app` 和 `asia-southeast1.firebasedatabase.app` 数据库 URL；调用方不能提供 `auth`、`access_token`、header 或内联 query：

```json
{"name":"gcp_api_read","arguments":{"auth_scheme":"firebase-sse","service":"firebase-database","operation":"Listen","method":"GET","url":"https://<database>.europe-west1.firebasedatabase.app/messages.json","parameters":{"orderBy":"\"createdAt\"","limitToLast":10},"body":{"max_events":32,"timeout_seconds":30},"response_file":"/approved/results/firebase-events.ndjson"}}
```

`max_events` 为 1–256，`timeout_seconds` 为 1–300；可选 `include_keep_alive=true` 会把 provider keep-alive 也写入并计数。`cancel`、`auth_revoked`、未知/超限事件和凭证形态的数据会使整个临时输出失败且不发布目标文件。保留的 `.settings` 路径不通过该流式入口暴露。

GCP 没有 REST transcoding 的 gRPC 方法使用 `auth_scheme=grpc`，仍然是 server 直接发起官方 HTTP/2 Request，不调用 `gcloud`、`grpcurl`、`protoc` 或其他 CLI。原始模式的 `body_file` 是由官方 protobuf schema 编码的一条或多条消息，每条前面添加标准的 `0x00 + 4-byte big-endian length`；`response_file` 保存校验过 framing 且 `grpc-status=0` 的原始 framed protobuf，成功前不会发布目标文件：

```json
{"name":"gcp_api_read","arguments":{"auth_scheme":"grpc","service":"speech","operation":"StreamingRecognize","method":"POST","url":"https://speech.googleapis.com/google.cloud.speech.v2.Speech/StreamingRecognize","project":"<project>","headers":{"x-goog-request-params":"recognizer=projects/<project>/locations/global/recognizers/_"},"body_file":"/approved/grpc/speech-request.grpc","response_file":"/approved/grpc/speech-response.grpc","stream_interval_ms":100}}
```

同一个入口也支持 `payload_mode=protobuf-json`。operator 把由官方 proto 生成、包含 imports 的二进制 `google.protobuf.FileDescriptorSet` 放在批准目录；server 从 URL 的 `/fully.qualified.Service/Method` 精确解析方法和 input/output 类型，把 unary/server-streaming 的单个 ProtoJSON object 或 client/bidirectional 的 1–256 个 object 转成确定性 protobuf frame，并把响应原子写成 NDJSON。descriptor 声明为 server-streaming 的方法必须设置 `stream_max_messages=1..256` 与 `stream_timeout_seconds=1..300`；达到消息数，或在已建立流中等待下一条完整 frame 时达到总时限，都会完成有限观察；半帧截断或 provider 已返回非零 `grpc-status` 时整次失败。它不猜测 JSON schema；未知请求字段、未知或嵌套（包括 `google.protobuf.Any`）响应 wire 字段、缺失/重复 unary 响应都会整次失败：

```json
{"name":"gcp_api_read","arguments":{"auth_scheme":"grpc","payload_mode":"protobuf-json","service":"speech","operation":"StreamingRecognize","method":"POST","url":"https://speech.googleapis.com/google.cloud.speech.v2.Speech/StreamingRecognize","project":"<project>","headers":{"x-goog-request-params":"recognizer=projects/<project>/locations/global/recognizers/_"},"protobuf_descriptor_file":"/approved/schemas/speech-v2.protoset","body":[{"recognizer":"projects/<project>/locations/global/recognizers/_","streamingConfig":{"config":{"autoDecodingConfig":{},"languageCodes":["en-US"]}}},{"audio":"<base64-audio-chunk>"}],"response_file":"/approved/grpc/speech-response.ndjson","stream_interval_ms":100,"stream_max_messages":64,"stream_timeout_seconds":60}}
```

原始和 schema-driven 两种模式都覆盖 unary、client-streaming、server-streaming 和有限 bidirectional-streaming；消息顺序和产品级大小仍必须遵守具体 RPC contract，例如 Speech-to-Text v2 的首条配置/后续音频规则和 15 KB 音频消息限制。64 位整数按官方 ProtoJSON 规则保持为十进制字符串，descriptor 内容不会发送给 Google 或进入 MCP 输出。

Vertex AI / Gemini Enterprise Agent Platform Live API 使用 `auth_scheme=vertex-live-ws`。server 只连接官方 global、regional 或 `us|eu` multi-region aiplatform WSS endpoint，通过 ADC 获取 `cloud-platform` OAuth token，并且只把 token 放进内部 Upgrade header。`body.messages` 第一条必须是与 `project`/`region` 一致的完整 `setup.model`，收到 `setupComplete` 后才会发送 `clientContent`、`realtimeInput` 或 caller 预备的 `toolResponse`；可选 `tool_handlers` 会根据服务端 `functionCalls[].name` 选择有限、无凭证的响应模板，并把服务端生成的 `id` 精确回填到 `functionResponses[]`。未知函数、重复 ID 或超过 `max_calls` 都会使本次会话失败且不发布输出。Live session 会产生云端推理成本并可携带工具响应，因此固定走 mutation gate：

```json
{"name":"gcp_api_mutate","arguments":{"auth_scheme":"vertex-live-ws","service":"aiplatform","operation":"BidiGenerateContent","project":"<project>","region":"global","method":"GET","url":"wss://aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent","body":{"messages":[{"setup":{"model":"projects/<project>/locations/global/publishers/google/models/<live-model>","generationConfig":{"responseModalities":["TEXT"]},"tools":[{"functionDeclarations":[{"name":"lookup_weather","description":"Look up approved weather data","parameters":{"type":"OBJECT","properties":{"city":{"type":"STRING"}}}}]}]}},{"clientContent":{"turns":[{"role":"user","parts":[{"text":"Weather in Singapore?"}]}],"turnComplete":true}}],"tool_handlers":[{"name":"lookup_weather","response":{"temperature":31,"unit":"celsius"},"max_calls":1}],"resume_on_go_away":true,"max_reconnects":2,"max_messages":32,"timeout_seconds":30},"response_file":"/approved/results/vertex-live.ndjson","force":true}}
```

`resume_on_go_away=true` 会由 server 注入 `sessionResumption.transparent=true`；收到 `sessionResumptionUpdate` 后只在内存保留 `newHandle`，按 `lastConsumedClientMessageIndex` 丢弃已确认缓冲，并在 `goAway` 或意外连接中断后重取 ADC、最多按 `max_reconnects=1..8` 重连及只重放未确认消息。调用方不能提供 session handle；输出中的 `newHandle` 会被剥离。所有服务端 JSON 在达到 `turnComplete`、`max_messages` 或 `timeout_seconds` 边界后原子发布为 NDJSON。caller 不能提供 Authorization、API key、query token、非官方 host/path 或无界会话；ADC token、session handle 和握手信息不会进入 MCP 结果或审计。

Alibaba Intelligent Speech Interaction 使用 `auth_scheme=nls-ws`。调用方只提供非秘密的 NLS 项目 AppKey、官方业务 payload、有限音频文件或文本段；server 从 credentials-go 的 RAM AKSK/STS 链内部签名固定 `CreateToken` RPC，缓存临时 Token，并只在已校验的 WSS 握手中使用。实时/短句识别分别使用 `SpeechTranscriber`、`SpeechRecognizer`，事件原子写入 NDJSON：

```json
{"name":"alicloud_api_read","arguments":{"auth_scheme":"nls-ws","service":"nls","operation":"SpeechTranscriber","method":"GET","url":"wss://nls-gateway-ap-southeast-1.aliyuncs.com/ws/v1","parameters":{"appkey":"<project-appkey>"},"body":{"format":"pcm","sample_rate":16000,"enable_intermediate_result":true},"body_file":"/approved/audio/input.pcm","response_file":"/approved/results/transcript.ndjson"}}
```

流式文本、单次和长文本合成分别使用 `FlowingSpeechSynthesizer`、`SpeechSynthesizer`、`SpeechLongSynthesizer`；音频只在收到成功终态后发布，事件以有界元数据返回：

```json
{"name":"alicloud_api_read","arguments":{"auth_scheme":"nls-ws","service":"nls","operation":"FlowingSpeechSynthesizer","method":"GET","url":"wss://nls-gateway-cn-beijing.aliyuncs.com/ws/v1","parameters":{"appkey":"<project-appkey>"},"body":{"start":{"voice":"xiaoyun","format":"mp3","sample_rate":16000},"texts":["第一段。","第二段。"]},"response_file":"/approved/results/speech.mp3"}}
```

任务 ID、消息 ID、Start/Stop 命令和 Token 都由 server 生成；caller-supplied `token`、`X-NLS-Token`、AKSK 或 `CreateToken` 调用仍会被公共边界拒绝。

同一内部 Token 也用于 `auth_scheme=nls-rest`：`ShortSentenceRecognition` 把有限音频作为 `body_file` POST 到官方 `/stream/v1/asr`；`SpeechSynthesisREST` 用 GET/query 或 POST/JSON 调用 `/stream/v1/tts` 并把音频原子写入 `response_file`。Token 只注入 `X-NLS-Token` header，不进入 URL 或 MCP 输出。

Alibaba Cloud ApsaraMQ for RocketMQ 4.x 的 AKSK HTTP 数据面使用 `auth_scheme=mq`，server 直接执行官方 `MQ` HMAC-SHA1 HTTPS 协议并内部生成 XML；不调用 CLI。四个受控 operation `PublishMessage|ConsumeMessages|ConsumeOrderly|ConsumeHalfMessages` 全部走 mutation gate，因为 pull 本身会改变消息的不可见窗口。普通消费必须在同一次调用选择 `settlement=acknowledge|release`；事务半消息必须选择 `transaction_outcome=commit|rollback`。server 只在 ACK/commit/rollback 全部成功后原子发布脱敏 NDJSON，`ReceiptHandle`、Authorization 和 STS token 永不进入 MCP 参数、结果、文件或审计，也不暴露独立 ACK/commit/rollback 入口：

```json
{"name":"alicloud_api_mutate","arguments":{"force":true,"auth_scheme":"mq","service":"rocketmq","operation":"ConsumeMessages","method":"GET","url":"https://<account-id>.mqrest.<region>.aliyuncs.com/topics/orders/messages","parameters":{"consumer":"orders-worker","ns":"<instance-id>","numOfMessages":16,"waitseconds":30,"tag":"created"},"body":{"settlement":"acknowledge"},"response_file":"/approved/results/rocketmq.ndjson"}}
```

普通、顺序、定时消息通过 `PublishMessage` 的 `message_body`、可选 `message_tag` 和官方 properties（例如 `KEYS`、`__SHARDINGKEY`、`__STARTDELIVERTIME`）发送。事务消息同时提供 `__TransCheckT=10..300`、`producer_group` 和预批准 outcome；publish 返回内部 handle 时 server 会立即完成对应 commit/rollback，再发布不含 handle 的结果：

```json
{"name":"alicloud_api_mutate","arguments":{"force":true,"auth_scheme":"mq","service":"rocketmq","operation":"PublishMessage","method":"POST","url":"https://<account-id>.mqrest.<region>.aliyuncs.com/topics/orders/messages","parameters":{"ns":"<instance-id>"},"body":{"message_body":"order created","message_tag":"created","properties":{"KEYS":"order-42","__TransCheckT":"30"},"producer_group":"transaction-producer","transaction_outcome":"commit"},"response_file":"/approved/results/rocketmq-publish.ndjson"}}
```

该入口只接受 HTTPS、官方 `mqrest` host（或 operator 固定的精确 endpoint host）、精确 topic path、单次 1–16 条和 0–30 秒长轮询；所有 headers 与 `body_file` 均禁止。RocketMQ 5.x 控制面仍可经 ACS3 调用；其公开消息收发协议要求实例 username/password/ACL user，而不是本项目允许的 RAM AKSK/IAM 身份入口，因此不会把这类密码凭证扩展到 MCP surface。4.x HTTP API 仍是官方维护的 AKSK 数据面。

Alibaba Cloud ACR 企业版 Docker/OCI 数据面使用 `auth_scheme=acr-registry`。调用方只提供 RAM identity chain、精确 `registry_instance_id`、region 和官方 `<instance-name>-registry[-vpc].<region>.cr.aliyuncs.com` URL；官方自定义域名必须先由 operator 固定到 `CLOUD_SKILLS_ALIBABA_ALLOWED_ENDPOINT_HOSTS`。server 内部用固定 RPC V2 `GetAuthorizationToken` 换取临时用户名/密码，再从 Registry 返回的精确 region-bound `dockerauth`、`dockerauth-ee`、对应 VPC 变体（以及张家口官方连字符形式）或同 Registry 接管域名 challenge 获取按 repository、路径和方法收敛的 Bearer token；这些凭证与 challenge capability 都不进入 MCP：

```json
{"name":"alicloud_api_read","arguments":{"auth_scheme":"acr-registry","service":"acr","operation":"ListTags","region":"cn-hangzhou","registry_instance_id":"cri-xxxxxxxx","method":"GET","url":"https://demo-registry.cn-hangzhou.cr.aliyuncs.com/v2/team/app/tags/list","parameters":{"n":20}}}
```

入口覆盖 Registry version/catalog、manifest、blob、tag、referrer、分块/整体 upload 和 cross-repository mount；写入、删除与 mount 仍走 `alicloud_api_mutate(force=true)`。跨仓库 mount 的 target `pull,push` 和 source `pull` scopes 由 server 内部生成。Blob GET/HEAD 最多跟随三次同地域官方 OSS 307，移除 Authorization 并隐藏带签名 Location。个人版不支持 `GetAuthorizationToken`、只接受固定 Registry 密码，因此按 AKSK/IAM-only 约束不暴露。

真实数据面验收使用显式 mutation probe。先在目标 topic 预置一条匹配消息，再注入 credentials-go 支持的 AKSK/STS；endpoint 即使控制台显示 `http://` 也必须改用同 host 的 `https://` 形式。默认 `release` 不会 ACK，只在 broker 可见性超时后重投；只有另行设置 `CLOUD_SKILLS_LIVE_ALIBABA_MQ_SETTLEMENT=acknowledge` 才确认消费：

```bash
CLOUD_SKILLS_ALLOW_MUTATIONS=1 \
CLOUD_SKILLS_LIVE_ALIBABA_MQ=1 \
CLOUD_SKILLS_LIVE_ALIBABA_MQ_ENDPOINT=https://<account-id>.mqrest.<region>.aliyuncs.com \
CLOUD_SKILLS_LIVE_ALIBABA_MQ_TOPIC=<topic> \
CLOUD_SKILLS_LIVE_ALIBABA_MQ_CONSUMER=<group-id> \
CLOUD_SKILLS_LIVE_ALIBABA_MQ_INSTANCE=<instance-id> \
go test ./internal/mcp/cloud -run TestLiveAlibabaMQMutation -v
```

Azure Web PubSub MQTT 3.1.1/5.0 使用 `auth_scheme=webpubsub-mqtt-ws`；Event Grid Namespace MQTT v5 使用 `auth_scheme=eventgrid-mqtt-ws` 和内部 Entra `OAUTH2-JWT` CONNECT/AUTH；Service Bus/Event Hubs 数据面使用 `servicebus-amqp-ws|eventhubs-amqp-ws` 和内部 Entra/CBS AMQP 1.0 over WSS；Azure SignalR Service Serverless JSON-hub WSS 使用 `auth_scheme=signalr-ws` 和内部 Entra `:generateToken`。它们都把凭证、临时 token、CBS claim、client token 和消息/会话锁留在 server 内部。

Azure OpenAI Realtime、Voice Live、Chat Completions 流式 SSE、Responses 流式 SSE 与 Web PubSub JSON/Protobuf WSS 分别使用 `auth_scheme=realtime-ws|voice-live-ws|openai-chat-stream|openai-responses-stream|webpubsub-ws`，GCP Artifact Registry OCI HTTP 与原生 HTTP/2 分别使用 `auth_scheme=artifact-registry|grpc`，AWS 有限原始帧 SigV4 WSS、Connect Health Medical Scribe、Transcribe、IoT MQTT、Kinesis Video Signaling、AppSync Events、AppSync GraphQL WebSocket、Chime SDK Messaging 事件流与 Amazon Connect chat participant WebSocket 使用 `sigv4-ws|connect-health-ws|transcribe-ws|iot-mqtt-ws|kinesisvideo-signaling-ws|appsync-event-ws|appsync-graphql-ws|chime-messaging-ws|connect-chat-ws`（connect-chat-ws 走 mutation gate，内部 SigV4 StartChatContact + X-Amz-Bearer CreateParticipantConnection，订阅 aws/chat 观察事件；可选 messages 经内部 SendMessage REST 发送并统计 sent_messages，token/URL 不外泄）。Alibaba ACS3 使用 `auth_scheme=acs3`；旧版 RPC/ROA V2 使用 `rpc|roa`；DataHub 和 OpenSearch 分别使用 `datahub|opensearch`；MaxCompute 项目/数据/Tunnel API 使用当前 `odps4` 或旧端点 `odps`；Function Compute 经典资源/旧 `/proxy` Trigger、新 `fcapp.run` Trigger、自定义域名分别使用 `fc|fc3|fc-custom`；OSS Header 签名使用 `oss|oss4`（V4 推荐），SLS 使用 `sls|sls4`，MNS 使用 `mns`，RocketMQ 4.x HTTP 数据面使用 `mq`，ACR 企业版 Registry 使用 `acr-registry`，Tablestore 使用 `ots|ots4`，NLS 使用 `nls-rest|nls-ws`。Tencent API 3.0 推荐使用 `tc3`；仍要求 GET/query 或 `application/x-www-form-urlencoded` 的 v1 调用使用 `tc1|tc1-sha256`；仍保留在 `*.api.qcloud.com/v2/index.php` 的旧版资源 API 使用 `qcloud|qcloud-sha256`；COS 使用 `cos`；CLS 旧版数据面使用独立的 `cls` q-sign 入口；TCR 企业版 Registry 使用 `tcr-registry`；实时 ASR、虚拟号真人判定、口语评测、实时语音翻译、实时音色变换、MPS 私有音频识别/翻译、MPS 流式语音合成、标准实时语音合成、流式文本语音合成和大模型播客分别使用 `asr-ws|virtual-number-ws|soe-ws|speech-translate-ws|voice-convert-ws|mps-ws|mps-tts-ws|tts-ws|tts-stream-ws|podcast-ws`，MCP server 内部完成 WSS Upgrade、帧传输和签名，绝不返回带签名连接 URL。Baidu REST 使用 `auth_version=v1|v2`；CCR 企业版/个人版 Registry 使用 `auth_scheme=ccr-registry`；IoT Core HTTPS 发布与 MQTT 3.1.1/5.0 WSS 分别使用 `iotcore-http-pub|iotcore-mqtt-ws`，server 内部从 IAM AK/SK 派生应用权限凭证，并把 HTTP 短期 token 或 MQTT CONNECT 凭证留在内部；RTC AI Agent 使用 `auth_scheme=rtc-aiagent-ws`，server 内部完成 BCE v1 create、license 激活、双工 WSS，并在所有 create 后路径尝试签名 stop，禁止 caller 使用 AK/SK query 或接触实例 token。六云二进制或媒体 request body 都可使用受控 `body_file`；大响应、gRPC 原始响应、SSE chunk 或 WebSocket 输出使用 `response_file`。OSS POST policy 和预签名 URL 会生成可转交的临时授权，不作为 MCP 通用代签出口。

Tencent CLS 当前管理面与新增功能优先走 API 3.0 `tc3`。仍公开的旧版 CLS 数据面签名必须显式使用 `auth_scheme=cls`，server 只接受精确的 `<region>.cls.tencentcs.com` 公网域名或 `<region>.cls.tencentyun.com` 同地域内网域名，按官网 `q-sign-algorithm=sha1` 规则签名，并在 CAM 临时凭证场景把 token 仅放入内部 `X-Cls-Token`。例如读取旧版 logset：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"cls","service":"cls","operation":"GetLogset","method":"GET","url":"https://ap-shanghai.cls.tencentcs.com/logset","parameters":{"logset_id":"<logset-id>"}}}
```

旧版写日志、修改配置等非 GET/HEAD/OPTIONS 请求必须走 `tencent_api_mutate(force=true)`；protobuf/压缩上传可用受控 `body_file` 和非凭证的 `Content-Type`、`Content-MD5`、`X-Cls-Compress-Type` headers。调用方不能提供 `Authorization`、`X-Cls-Token` 或任何 q-sign 凭证查询参数。

Tencent Cloud TCR 企业版 Docker/OCI 数据面使用 `auth_scheme=tcr-registry`。调用方只提供 CAM AKSK/STS 环境身份、精确 `registry_instance_id`、region 和官方 `<instance>.tencentcloudcr.com`、`<instance>-vpc.tencentcloudcr.com` 或 operator 固定的官方自定义域名。server 内部用 TC3 调固定 `tcr/2019-09-24 CreateInstanceToken` 且强制 `TokenType=temp`，只把返回的一小时用户名/JWT 作为 Registry Basic 认证使用；长期 TokenId、临时密码和 Authorization 都不进入 MCP：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"tcr-registry","service":"tcr","operation":"ListTags","region":"ap-guangzhou","registry_instance_id":"tcr-xxxxxxxx","method":"GET","url":"https://demo-tcr.tencentcloudcr.com/v2/team/app/tags/list","parameters":{"n":20}}}
```

入口覆盖 Registry version/catalog、manifest、blob、tag、referrer、分块/整体 upload 和 cross-repository mount；写入、删除与 mount 仍走 `tencent_api_mutate(force=true)`。个人版及个人版域名兼容模式依赖用户设置的个人版用户名/密码，不属于 CAM AKSK/IAM-only 入口，因此不在此 scheme 中混用。

真实 CLS q-sign 验收是显式 opt-in 的只读旧版 `GetLogset`，需要一个现存 logset ID；当前 API 3.0 的通用 Tencent live probe 不能替代它：

```bash
CLOUD_SKILLS_LIVE_TENCENT_CLS=1 \
CLOUD_SKILLS_LIVE_TENCENT_CLS_ENDPOINT=https://ap-shanghai.cls.tencentcs.com \
CLOUD_SKILLS_LIVE_TENCENT_CLS_LOGSET_ID=<logset-id> \
go test ./internal/mcp/cloud -run TestLiveTencentCLSReadOnly -v
```

TCR 企业版只读 live gate 需要现存可读 repository、实例 ID、region 和精确公网/VPC Registry origin：

```bash
CLOUD_SKILLS_LIVE_TENCENT_TCR=1 \
CLOUD_SKILLS_LIVE_TENCENT_TCR_ENDPOINT=https://demo-tcr.tencentcloudcr.com \
CLOUD_SKILLS_LIVE_TENCENT_TCR_INSTANCE_ID=tcr-xxxxxxxx \
CLOUD_SKILLS_LIVE_TENCENT_TCR_REGION=ap-guangzhou \
CLOUD_SKILLS_LIVE_TENCENT_TCR_REPOSITORY=team/app \
go test ./internal/mcp/cloud -run TestLiveTencentTCRReadOnly -v
```

Baidu CCR 企业版与个人版 Docker/OCI 数据面使用 `auth_scheme=ccr-registry`，调用方仍只注入 BCE AK/SK 或 IAM/STS 临时三元组。企业版要求精确的 `registry_instance_id`、`registry_user_id`、region 与官方 `<instance>-pub.cnc[.bd].<region>.baidubce.com`、`<instance>-vpc.cnc[.bd].<region>.baidubce.com` 或 operator 固定的官方自定义域名；server 内部用固定 BCE v1 用户查询和一小时临时密码 API。个人版只接受 `registry.baidubce.com`，并内部调用固定用户查询和一小时临时 token API。两条路径都先验证 Registry 资源计划，再获取临时登录、解析同源 `/service/token` challenge 并换取最小 repository scope Bearer；临时用户名、密码/token、Basic 与 Bearer 均不会进入 MCP、审计或错误：

```json
{"name":"baiducloud_api_read","arguments":{"auth_scheme":"ccr-registry","service":"ccr","operation":"ListTags","region":"bj","registry_instance_id":"ccr-xxxxxxxx","registry_user_id":"<iam-user-id>","method":"GET","url":"https://ccr-xxxxxxxx-pub.cnc.bj.baidubce.com/v2/team/app/tags/list","parameters":{"n":20}}}
```

```json
{"name":"baiducloud_api_read","arguments":{"auth_scheme":"ccr-registry","service":"ccr","operation":"ListTags","method":"GET","url":"https://registry.baidubce.com/v2/team/app/tags/list","parameters":{"n":20}}}
```

入口覆盖 Registry version/catalog、manifest、blob、tag、referrer、分块/整体 upload 与 cross-repository mount；写入、删除与 mount 仍走 `baiducloud_api_mutate(force=true)`。当前没有找到可用于安全收敛的官方 blob 重定向合同，因此所有重定向 fail closed。

个人版只读 live gate：

```bash
CLOUD_SKILLS_LIVE_BAIDU_CCR=1 \
CLOUD_SKILLS_LIVE_BAIDU_CCR_ENDPOINT=https://registry.baidubce.com \
CLOUD_SKILLS_LIVE_BAIDU_CCR_REPOSITORY=team/app \
go test ./internal/mcp/cloud -run TestLiveBaiduCCRReadOnly -v
```

企业版在此基础上使用企业 Registry origin，并增加 `CLOUD_SKILLS_LIVE_BAIDU_CCR_REGION`、`CLOUD_SKILLS_LIVE_BAIDU_CCR_INSTANCE_ID` 与 `CLOUD_SKILLS_LIVE_BAIDU_CCR_USER_ID`。官方自定义域名还必须由 operator 固定到 `CLOUD_SKILLS_BAIDU_ALLOWED_ENDPOINT_HOSTS`。

Baidu IoT Core 的 IAM 应用权限 MQTT 3.1.1/5.0 数据面使用 `auth_scheme=iotcore-mqtt-ws`，只接受精确的 `wss://<iot-core-id>.iot.gz.baidubce.com/mqtt` 和 WebSocket `mqtt` 子协议。调用方仅声明无凭证的 client/topic/QoS/消息上限计划；server 按官网双重 HMAC-SHA256-HEX 公式从 `BCE_ACCESS_KEY_ID`/`BCE_SECRET_ACCESS_KEY` 派生 CONNECT username/password。`protocol_version` 为 `4`（默认）或 `5`；两版都支持 QoS 0/1/2，单连接最多 100 个订阅并按每包 8 个批处理，支持官方 `$share/<group>/<filter>`；`ClientMQTT` 还可用同样边界的 `unsubscriptions` 清理持久订阅并验证 UNSUBACK。MQTT 5 还覆盖 reason code、server DISCONNECT、Payload Format、Content Type、Message Expiry、Response Topic、Correlation Data、User Property、Will Delay 和 Receive Maximum；QoS 2 双向执行 duplicate-safe PUBREC/PUBREL/PUBCOMP。消息默认上限为 32 KiB；实例经官网流程获批后可显式设置 `max_payload_bytes`，但不得超过 128 KiB，收发两侧都执行同一边界；publish 由 server 按官网 QoS 速率限制自动节流。派生凭证不会进入 MCP、审计或错误。官网应用权限格式没有 STS session-token 字段，因此此路径在配置 `BCE_SESSION_TOKEN`/`BCE_SECURITY_TOKEN` 时 fail closed。订阅是 read tool；publish、持久订阅变更和 Will 必须走 mutation gate：

```json
{"name":"baiducloud_api_read","arguments":{"auth_scheme":"iotcore-mqtt-ws","service":"iotcore","operation":"SubscribeMQTT","method":"GET","url":"wss://<iot-core-id>.iot.gz.baidubce.com/mqtt","body":{"protocol_version":5,"client_id":"observer-1","subscriptions":[{"topic_filter":"$share/observers/sensors/+/temperature","qos":2}],"max_messages":1,"timeout_seconds":60},"response_file":"/approved/results/baidu-iotcore.ndjson"}}
```

同一 IAM 应用权限也支持官网 HTTP Publish。`auth_scheme=iotcore-http-pub` 只接受精确的 `POST https://<iot-core-id>.iot.gz.baidubce.com/pub`；server 内部调用固定 `/auth` 获得 60 秒 token，再以 `token` header 调 `/pub?topic=...&qos=0|1`。调用方只能提交 topic、QoS 与 Base64 或受控 `body_file` payload。默认 32 KiB、获批实例最多 128 KiB，并按官网 50 QPS/IP 节流；短期 token、派生 username/password、AK/SK 都不会进入 MCP、审计或错误。发布必须走 mutation gate：

```json
{"name":"baiducloud_api_mutate","arguments":{"auth_scheme":"iotcore-http-pub","service":"iotcore","operation":"PublishHTTP","method":"POST","url":"https://<iot-core-id>.iot.gz.baidubce.com/pub","body":{"topic":"commands/device-1","qos":1,"payload_base64":"dHVybi1vbg=="},"force":true}}
```

真实 read-only gate 需要 operator 先把同一 BCE IAM AK/SK 绑定为该实例的应用权限，并在订阅启动后向匹配 topic 投递一条消息：

```bash
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT=1 \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_ENDPOINT=wss://<iot-core-id>.iot.gz.baidubce.com/mqtt \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_TOPIC='sensors/test' \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_MQTT_CLIENT_ID='cloud-skills-live-unique' \
go test ./internal/mcp/cloud -run TestLiveBaiduIoTCoreMQTTReadOnly -v
```

该 gate 原子读取一条 NDJSON 消息、检查成功审计事件和凭证不泄露；尚未注入 operator 凭证与消息时，不能把 hermetic 官方签名向量当作真实云端通过。

HTTP Publish 的真实 mutation gate 使用同一 IAM 应用权限；清空 BCE session-token 变量并选择一次性 topic/payload：

```bash
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB=1 \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_ENDPOINT=https://<iot-core-id>.iot.gz.baidubce.com/pub \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_TOPIC='commands/test' \
CLOUD_SKILLS_LIVE_BAIDU_IOTCORE_HTTP_PUB_PAYLOAD_BASE64='Y2xvdWQtc2tpbGxzLWxpdmU=' \
go test ./internal/mcp/cloud -run TestLiveBaiduIoTCoreHTTPPubMutation -v
```

该测试仅在显式 opt-in 时启用 mutation gate，校验官网 `{"message":"ok"}` 和成功审计事件，不打印 payload 或凭证。

Baidu RTC AI Agent 文本、音频、图片与 Function Call 双工会话必须走 mutation gate。`app_id`、非敏感 `config`、device/user 标识、终止条件和 `audio_codec` 放在 body。`messages`/`final_messages` 分别在音频前/后发送，合计最多 64 条，并严格解析官网的打断、文本、直接 TTS、自动打断、设备/GIS、云音乐、ASR 模式、system prompt、动态变量、三方透传、角色、增强 Query、MCP Tools 变更、直接音乐和会议纪要静态指令；未声明的 server event、license 激活、原始图片帧与调用方自造的 `[F]:` 结果不能伪装成普通字符串。需要图片时把单个非空文件放在 `image_file`，可在 body 设 `image_mode="image_generate"`；server 只在收到精确的 `[E]:[UPLOAD_IMAGE]` 后按官网 16 KiB 分片协议上传一次，并保证图片帧不会与并发音频帧交错。配置了图片但会话未请求、未配置图片却收到请求、或重复请求都会 fail closed。Function Call 通过 body `function_results` 按 `function_name` 预声明最多 32 个无凭证 `ok|error` 结果模板，并用 `max_function_calls` 限制本次调用数；server 严格解析 provider 的嵌套 JSON，只复用其 `session_id` 生成响应，未知函数、重复 session、旧 `[F]:[C]:` 格式和带凭证参数均 fail closed。模板可含 `message` 或官网 `post_function` 的 `text|prompt|play_music` 组合，但类型必须唯一，且 `text` 与 `prompt` 不可并存。`audio_codec` 支持官网列出的 `raw|raw16k|pcma|pcmu|g722|opus`，server 会把同值写入控制面 `config.audiocodec` 与内部 WSS `ac`。固定码率文件按 `stream_interval_ms=20..200`（默认 20）分包，`stream_chunk_bytes` 必须与编码/时长严格相符；Opus 使用 `opus_packet_time_ms=20|40|60` 和覆盖整个文件的 `opus_packet_lengths`，server 内部生成 `ptime`/`plen`。WSS 结果以 text/Base64-binary NDJSON 原子发布，创建后的任何失败都会尝试签名 stop：

```json
{"name":"baiducloud_api_mutate","arguments":{"force":true,"auth_scheme":"rtc-aiagent-ws","service":"rtc-aiagent","operation":"RealtimeInteraction","api_version":"1","method":"GET","url":"wss://rtc-aiotgw.exp.bcelive.com/v1/realtime","body":{"app_id":"<rtc-app-id>","instance_type":"VoiceChat","config":{},"audio_codec":"raw16k","device_id":"<device-id>","user_id":"<user-id>","messages":["[T]:你好"],"max_messages":32,"timeout_seconds":60,"terminal_event":"tts_end"},"body_file":"/approved/audio/raw16k.pcm","stream_chunk_bytes":640,"stream_interval_ms":20,"response_file":"/approved/results/baidu-rtc.ndjson"}}
```

事件触发图片示例可省略音频和静态消息：

```json
{"name":"baiducloud_api_mutate","arguments":{"force":true,"auth_scheme":"rtc-aiagent-ws","service":"rtc-aiagent","operation":"RealtimeInteraction","api_version":"1","method":"GET","url":"wss://rtc-aiotgw.exp.bcelive.com/v1/realtime","body":{"app_id":"<rtc-app-id>","device_id":"<device-id>","user_id":"<user-id>","messages":[],"image_mode":"image_generate","max_messages":32,"timeout_seconds":60,"terminal_event":"tts_end"},"image_file":"/approved/images/camera.jpg","response_file":"/approved/results/baidu-rtc-image.ndjson"}}
```

Function Call 关联响应示例（`session_id` 不属于调用参数，由 server 从 provider 事件中绑定）：

```json
{"name":"baiducloud_api_mutate","arguments":{"force":true,"auth_scheme":"rtc-aiagent-ws","service":"rtc-aiagent","operation":"RealtimeInteraction","api_version":"1","method":"GET","url":"wss://rtc-aiotgw.exp.bcelive.com/v1/realtime","body":{"app_id":"<rtc-app-id>","device_id":"<device-id>","user_id":"<user-id>","messages":["[T]:调高音量"],"function_results":{"adjust_volume":{"result":"ok","message":"音量已调整"}},"max_function_calls":4,"max_messages":32,"timeout_seconds":60,"terminal_event":"tts_end"},"response_file":"/approved/results/baidu-rtc-function.ndjson"}}
```

Tencent ASR WebSocket 有限音频流示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"asr-ws","service":"asr","operation":"RecognizeStream","method":"GET","url":"wss://asr.cloud.tencent.com/asr/v2/<appid>","parameters":{"engine_model_type":"16k_zh","voice_format":1},"body_file":"/approved/audio/input.pcm","response_file":"/approved/results/asr.ndjson"}}
```

默认按官方建议每 200ms 发送一帧；PCM 根据 8k/16k 自动选择 3200/6400 字节。压缩格式或完整 m4a 分片可用 `stream_chunk_bytes` 指定单帧大小，`stream_interval_ms` 调整节奏。两者只控制内部连接，不进入签名查询参数。若 operator 注入 CAM/STS 三元组，server 按官方 Web SDK 行为将小写 `token` 加入排序后的签名查询；调用方仍不能通过 MCP 参数提供 token。

Tencent 虚拟号真人判定示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"virtual-number-ws","service":"asr","operation":"RecognizeHumanStream","method":"GET","url":"wss://asr.cloud.tencent.com/asr/virtual_number/v1/<appid>","parameters":{"voice_format":1,"wait_time":30},"body_file":"/approved/audio/call.pcm","response_file":"/approved/results/human.ndjson"}}
```

8k PCM 默认每 40ms 发送 640 字节；服务端在真人接听结果或 `final=1` 返回后停止，并以有界 NDJSON 返回/原子落盘。压缩格式可显式调整 `stream_chunk_bytes`，带签名 URL 始终留在进程内部。

Tencent 新版口语评测示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"soe-ws","service":"soe","operation":"EvaluateSpeechStream","method":"GET","url":"wss://soe.cloud.tencent.com/soe/api/<appid>","parameters":{"server_engine_type":"16k_en","eval_mode":1,"score_coeff":1.5,"ref_text":"hello","voice_format":0},"body_file":"/approved/audio/speech.pcm","response_file":"/approved/results/soe.ndjson"}}
```

实时 PCM 默认每 40ms 发送 1280 字节；`rec_mode=1` 时禁止自定义流控并将受控录音文件一次性发送。`server_engine_type`、`eval_mode`、`score_coeff` 和可选模式字段均按官网范围验证。评测中间结果与 `final=1` 终态以有界 NDJSON 返回，参考文本和签名不进入审计记录。

Tencent 实时语音翻译示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"speech-translate-ws","service":"asr","operation":"TranslateStream","method":"GET","url":"wss://asr.cloud.tencent.com/asr/speech_translate/<appid>","parameters":{"source":"zh","target":"en","trans_model":"hunyuan-translation-lite","voice_format":1,"enable_tts":1,"codec":"mp3","sample_rate":16000},"body_file":"/approved/audio/input.pcm","response_file":"/approved/results/translation.mp3"}}
```

未开启 `enable_tts` 时，JSON 译文按有界 NDJSON 返回，`response_file` 可选；开启后该文件专用于按序原子发布合成音频，JSON 握手/译文/终态仍以结构化 `messages` 数组返回。服务端先返回 `final=1` 表示翻译结束，开启 TTS 时服务器继续接收音频，直到 `final=2` 才完成调用。

Tencent 实时音色变换示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"voice-convert-ws","service":"vc","operation":"ConvertVoice","method":"GET","url":"wss://tts.cloud.tencent.com/vc_stream/<appid>","parameters":{"VoiceType":301005,"SampleRate":16000,"Codec":"pcm","Volume":0},"body_file":"/approved/audio/input.pcm","response_file":"/approved/results/converted.pcm"}}
```

服务端按官网协议默认每 100ms 读取 3200 字节 16kHz/16-bit/mono PCM，生成 `HEAD + JSON + PCM` 大端二进制帧，并将转换后的 PCM 仅写入新的受控文件。只有收到同一 `VoiceId` 的 `Final=1` 且 `Code=0` 才原子发布；失败或超限不会留下目标文件。

Tencent MPS WebSocket 私有 PCM 音频流示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"mps-ws","service":"mps","operation":"RecognizeStream","method":"GET","url":"wss://mps.cloud.tencent.com/wss/v1/<appid>","parameters":{"asrDst":"zh","fragmentNotify":0,"timeoutSec":10},"body_file":"/approved/audio/input.pcm","response_file":"/approved/results/mps.ndjson","stream_user_id":"speaker-1","stream_format":1}}
```

`stream_format=1` 是 16kHz s16 单声道，`2` 是 8kHz；默认按官网示例每 40ms 发送 1280/640 字节。服务器用网络字节序封装每个二进制包，末包设置 `IsEnd=1`，收到 `ProcessEof` 后原子发布 NDJSON。未传 `timeoutSec` 时遵循官方 120 秒无音频超时，建议有限文件调用显式设置 1–300 秒内的等待值。

Tencent MPS 流式 TTS 示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"mps-tts-ws","service":"mps","operation":"SynthesizeSpeech","method":"GET","url":"wss://mps.cloud.tencent.com/tts/v1/<appid>","parameters":{"voiceId":"<voice-id>","format":"mp3","sampleRate":22050,"language":"zh","timeoutSec":30},"body":["第一段文本。","第二段文本。"],"response_file":"/approved/results/speech.mp3"}}
```

`body` 是一段文本或最多 256 段的字符串数组，每段按官网限制最多 5000 个 Unicode 字符。服务器等待握手协商后的格式/采样率，逐段发送 `Final=false`，最后发送空文本 `Final=true`；所有下行二进制音频按序写入新的 mode-0600 文件，只有收到成功的 `ProcessEof` 才原子发布，MCP 仅返回路径、字节数、实际格式、采样率和任务 ID。

Tencent 标准实时 TTS 示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"tts-ws","service":"tts","operation":"SynthesizeSpeech","method":"GET","url":"wss://tts.cloud.tencent.com/stream_ws","parameters":{"AppId":1300460000,"Codec":"mp3","VoiceType":101001,"SampleRate":16000,"EnableSubtitle":true},"body":"欢迎使用腾讯云实时语音合成","response_file":"/approved/results/tts.mp3"}}
```

`AppId` 是公开账号标识，不是凭证；SecretId/SecretKey 只从 server 环境读取。服务器生成 `SessionId`、时间戳、有效期和 HMAC-SHA1 签名，文本仅进入内部签名查询。中文或其他非 ASCII 文本保守限制为 600 个 Unicode 字符，纯 ASCII 文本限制为 1800 个字符。文本状态帧以结构化 `messages` 返回，二进制音频仅在同一 session/request 收到 `final=1` 后原子发布；失败、断流或超限不会留下目标文件。

Tencent 流式文本 TTS 示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"tts-stream-ws","service":"tts","operation":"SynthesizeSpeechStream","method":"GET","url":"wss://tts.cloud.tencent.com/stream_wsv2","parameters":{"AppId":1300460000,"Codec":"mp3","VoiceType":101001,"SampleRate":16000},"body":["第一段逐步生成的文本，","第二段文本！"],"response_file":"/approved/results/stream-tts.mp3"}}
```

`body` 是一段文本或最多 1024 个受控文本分片，会话总长度按官网限制不超过 10000 个 Unicode 字符，且不支持 SSML。服务器完成 HMAC-SHA1 握手并收到 `ready=1` 后，才为每个分片生成唯一 message ID 并发送 `ACTION_SYNTHESIS`，最后发送 `ACTION_COMPLETE`。所有 JSON 事件以 `messages` 返回，音频只有在匹配 session/request 的 `final=1` 后才原子发布；官网定义为可忽略通知的 10009 不会误判失败，其他非零错误会中止。

Tencent 大模型播客示例：

```json
{"name":"tencent_api_read","arguments":{"auth_scheme":"podcast-ws","service":"tts","operation":"TextToPodcastStreamAudioWS","method":"GET","url":"wss://tts.cloud.tencent.com/stream_ws_podcast","parameters":{"AppId":1300466766,"SampleRate":24000,"Codec":"pcm","SpeakerNumber":2,"Speaker1Voice":"zixin","Speaker2Voice":"acan","EnableWebSearch":false},"body":[{"ObjectType":"TYPE_TEXT","Text":"生成一段关于云原生架构的播客。"},{"ObjectType":"TYPE_TEXT","Text":"重点比较控制面与数据面。"}],"response_file":"/approved/results/podcast.pcm"}}
```

`body` 是一个 InputObject 或最多 10 个同类型对象，支持 `TYPE_TEXT`、`TYPE_URL`、`TYPE_FILE`；文本总长不超过 10000 个 Unicode 字符，URL/文件仅接受非 IP、非 localhost 的 HTTPS DNS 域名，文件格式限 `pdf|txt|docx|md`。服务器使用官网固定算法签名，收到 `ready=1` 后逐项发送 JSON 编码的 `ACTION_SYNTHESIS`，最后发送 `ACTION_COMPLETE`。脚本、token 用量、心跳和状态事件进入结构化 `messages`；24kHz mono PCM 只有在匹配 session/request 的 `final=1` 后才原子发布。

Google Cloud Storage 分段下载示例（其他五云同样使用各自官方 GetObject/Get Blob URL 与 `Range` header）：

```json
{"name":"gcp_api_read","arguments":{"method":"GET","url":"https://storage.googleapis.com/storage/v1/b/<bucket>/o/<url-encoded-object>?alt=media","headers":{"Range":"bytes=0-104857599"},"response_file":"/approved/downloads/object.part-000"}}
```

## 验证

无需凭证的 hermetic gate：

```bash
gofmt -l . && go vet ./...
go test -race ./...
go build -trimpath -o /tmp/cloud-skills-mcp ./cmd/cloud-skills-mcp
./scripts/ci/protocol-smoke.sh /tmp/cloud-skills-mcp
./scripts/ci/mapping-audit.sh "$PWD"
./scripts/ci/install-smoke.sh
```

以上全部不依赖任何云账号：协议冒烟用 72 个 `auth_scheme` 的预凭证扫描，
mapping audit 证明每个 scheme 有定义、分发、测试、冒烟与文档。

## 没有云账号？先本地验证，再选真实路径

如果你还没有 AWS/Azure/GCP/阿里/腾讯/百度账号，按优先级三条路：

1. **本地全绿（零账号，必做）**：上面的 hermetic gate 全部通过即证明协议族、
   签名、安全门与文档映射正确，这一步你可以完整跑完。
2. **AWS 本地模拟（可选，有前提）**：本 server 的安全边界只接受官方 HTTPS 443 +
   受信任证书，而 LocalStack 默认是 http/自签证书，直接连会被拒绝。要用它演练，
   需把 LocalStack 放到 443 且证书被本机信任（例如把 LocalStack 的 CA 加入系统
   信任后映射到 443，或用可信证书的反向代理），再
   `export CLOUD_SKILLS_AWS_ALLOWED_ENDPOINT_HOSTS=localhost` 放行。这属于社区
   演练路径，不在审计过的 live gate 之内，也不能代表真实 IAM 权限语义。
3. **真实 live 验收（最终标准）**：任选一家开免费/试用账号即可跑只读 gate，成本
   最低路径是“开账号 + 只读调用 + 用完即弃”，不需要预置生产资源：

   - AWS Free Tier：注册需信用卡验证；EC2 `DescribeInstances` 空结果也算通过
   - Azure 免费账号：$200 额度 + 12 个月常用免费服务，注册需手机/卡验证
   - GCP $300 试用：需卡验证；用默认项目跑只读查询
   - 阿里/腾讯/百度：免费账号 + 实名认证，多数只读 OpenAPI 可直接跑

   统一入口（一次跑全部已启用 gate）：

   ```bash
   CLOUD_SKILLS_LIVE_TEST=1 scripts/ci/live-acceptance.sh
   ```

   每家 gate 的必填环境变量见 `docs/goal-completion-matrix.md`。也可以把
   `live-acceptance.sh` 交给有对应账号的同事/朋友跑，把结果回填矩阵即可，无需你
   自己注册全部六家。

真实云只读验收必须由 operator 显式打开，并只从进程环境读取凭证：

```bash
CLOUD_SKILLS_LIVE_TEST=1 go test ./internal/mcp/cloud -run TestLiveSixCloudReadOnly -v
```

GCP live 验收需要 `CLOUD_SKILLS_LIVE_GCP_PROJECT`。各云可用 `CLOUD_SKILLS_LIVE_PROVIDERS` 选择子集。live gate 永不调用 mutate 工具，也不打印响应正文；它逐云输出 adapter availability、credential source/status、审计 outcome、响应字节数和可用的 provider RequestId。

Artifact Registry 使用独立只读 live gate，需要 ADC、精确 Registry origin 和现存可读取 image path（`pkg.dev` 为 `<project>/<repository>/<image>`，`gcr.io` 为 `<project>/<image>`）：

```bash
CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY=1 \
CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY_ENDPOINT=https://us-west1-docker.pkg.dev \
CLOUD_SKILLS_LIVE_GCP_ARTIFACT_REGISTRY_REPOSITORY='<project>/<repository>/<image>' \
go test ./internal/mcp/cloud -run TestLiveGCPArtifactRegistryReadOnly -v
```

Alibaba Cloud ACR 企业版使用独立只读 live gate，需要 RAM AKSK/STS 或角色身份、精确 Enterprise Registry origin、instance ID 和现存 repository。VPC endpoint 的 runner 还必须能解析并访问对应 Registry、`dockerauth-vpc`/`dockerauth-ee-vpc` 与 OSS 私网域名：

```bash
CLOUD_SKILLS_LIVE_ALIBABA_ACR=1 \
CLOUD_SKILLS_LIVE_ALIBABA_ACR_ENDPOINT=https://demo-registry.cn-hangzhou.cr.aliyuncs.com \
CLOUD_SKILLS_LIVE_ALIBABA_ACR_INSTANCE_ID=cri-xxxxxxxx \
CLOUD_SKILLS_LIVE_ALIBABA_ACR_REPOSITORY=team/app \
go test ./internal/mcp/cloud -run TestLiveAlibabaACRReadOnly -v
```

私有 ECR Registry live gate 需要精确 registry origin 和现存可拉取 repository；ECR Public 使用独立 gate，因为它固定 `ecr-public/us-east-1`、使用 Bearer token 且不支持 `/tags/list`。两者都只从 AWS SDK chain 解析 IAM/AKSK/STS，不打印 Registry token 或响应正文：

```bash
CLOUD_SKILLS_LIVE_AWS_ECR=1 \
CLOUD_SKILLS_LIVE_AWS_ECR_ENDPOINT=https://123456789012.dkr.ecr.us-west-2.amazonaws.com \
CLOUD_SKILLS_LIVE_AWS_ECR_REPOSITORY=team/app \
go test ./internal/mcp/cloud -run TestLiveAWSECRReadOnly -v
```

```bash
CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC=1 \
CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC_ENDPOINT=https://public.ecr.aws \
CLOUD_SKILLS_LIVE_AWS_ECR_PUBLIC_REPOSITORY='<registry-alias>/<repository>' \
go test ./internal/mcp/cloud -run TestLiveAWSECRPublicReadOnly -v
```

Baidu RTC 是会创建计费实例的独立 opt-in mutation gate。只有 operator 已完成具体会话批准并注入 BCE AKSK/IAM 与 product license 后才运行；测试强制走 MCP mutation/force/audit、内部 create/WSS/stop 和 secret containment：

```bash
CLOUD_SKILLS_ALLOW_MUTATIONS=1 \
CLOUD_SKILLS_LIVE_BAIDU_RTC=1 \
CLOUD_SKILLS_LIVE_BAIDU_RTC_APP_ID='<app-id>' \
CLOUD_SKILLS_LIVE_BAIDU_RTC_DEVICE_ID='<device-or-mac>' \
CLOUD_SKILLS_LIVE_BAIDU_RTC_USER_ID='<user-id>' \
CLOUD_SKILLS_LIVE_BAIDU_RTC_TEXT='你好' \
go test ./internal/mcp/cloud -run TestLiveBaiduRTCAgentMutation -v
```

`BCE_RTC_LICENSE_KEY` 与 BCE AKSK/IAM 环境变量不写在命令参数示例中；它们只应预先注入测试进程环境。测试不打印对话正文，只记录 outcome、字节数和 request ID。

## 目录

```text
cmd/cloud-skills-mcp/       # 六云统一 stdio server
internal/mcp/cloud/        # 通用 contract、policy 与六云 adapters
aws/ azure/ google-cloud/ alicloud/ tencent-cloud/ baiducloud/
                            # 六份 Skills + 官方文档映射
plugin/                     # 开源 Codex 插件打包（.codex-plugin + skills）
docs/six-cloud-full-resource-contract.md
docs/agentic-quickstart.md  # Agent 快速使用提示词与规则
scripts/ci/                # 协议、安装与安全验证
PROMOTION.md                # 宣传定位、渠道文案与演示脚本
```

## 官方文档

每份 Skill 的 `references/official-docs.md` 保存对应云厂商的认证、HTTP 签名、资源发现和 API reference 链接。通用访问层的完成定义、安全合同与真实验收门见 [docs/six-cloud-full-resource-contract.md](docs/six-cloud-full-resource-contract.md)，认证/传输族的真实覆盖与缺口见 [docs/api-protocol-coverage.md](docs/api-protocol-coverage.md)，逐项完成证据与仍未通过的 live gate 见 [docs/goal-completion-matrix.md](docs/goal-completion-matrix.md)。

## License

MIT
