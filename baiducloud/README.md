# baiducloud — Baidu Intelligent Cloud (BCE) Manager (待写)

⏳ **Phase 2 实现**

## 计划覆盖

| 服务 | CLI 命令 | 状态 |
|---|---|---|
| **BCC** (云服务器) | `bcecmd bcc list / start / stop / reboot` | ⏳ |
| **BOS** (对象存储) | `bcecmd bos list / cp / rm` | ⏳ |
| **RDS** (关系数据库) | `bcecmd rds list` | ⏳ |
| **VPC** (专有网络) | `bcecmd vpc list` | ⏳ |
| **KMS** (密钥管理) | `bcecmd kms list-key` | ⏳ |
| **CCE** (容器引擎, K8s) | `bcecmd cce list-cluster` | ⏳ |
| **CFC** (函数计算) | `bcecmd cfc invoke` | ⏳ |
| **CDN** | `bcecmd cdn list-domain` | ⏳ |

## 凭证

```bash
# bcecmd CLI
bcecmd configure

# 或 macOS Keychain
security add-generic-password -s baiducloud -a access-key-id -w <AK>
security add-generic-password -s baiducloud -a secret-access-key -w <SK>
security add-generic-password -s baiducloud -a region -w cn-bj
```

## SDK 选型

- `bce-sdk-go` (官方, Go)
- 来源: https://github.com/baidubce/bce-sdk-go

## Phase 2 计划

1. 写 `baiducloud/scripts/{bcc,bos,rds,vpc,kms,cce,cfc,cdn}.sh` (8 个 bash 脚本)
2. 写 `baiducloud/SKILL.md` (meta 入口)
3. 写 `cmd/baiducloud-mcp/main.go` + 编译到 `~/bin/baiducloud-mcp`
4. 端到端测 8 个工具
