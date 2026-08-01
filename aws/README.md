# aws — Amazon Web Services Manager (待写)

⏳ **Phase 2 实现**

## 计划覆盖

| 服务 | CLI 命令 | 状态 |
|---|---|---|
| **EC2** (云服务器) | `aws ec2 describe-instances / start-instances / stop-instances` | ⏳ |
| **S3** (对象存储) | `aws s3 ls / cp / rm / presign` | ⏳ |
| **RDS** (关系数据库) | `aws rds describe-db-instances` | ⏳ |
| **VPC** (专有网络) | `aws ec2 describe-vpcs` | ⏳ |
| **Secrets Manager** | `aws secretsmanager get-secret-value` | ⏳ |
| **EKS** (Kubernetes) | `aws eks list-clusters` | ⏳ |
| **Lambda** (Serverless) | `aws lambda invoke` | ⏳ |
| **CloudFront** (CDN) | `aws cloudfront list-distributions` | ⏳ |

## 凭证

```bash
# AWS CLI
aws configure
# 输入: Access Key ID / Secret Access Key / Default region (us-east-1) / Output (json)

# 或 macOS Keychain (本仓库统一)
security add-generic-password -s aws -a access-key-id -w <AKID>
security add-generic-password -s aws -a secret-access-key -w <SK>
security add-generic-password -s aws -a region -w us-east-1
```

## SDK 选型

- `aws-sdk-go-v2` (官方, Go)
- 来源: https://github.com/aws/aws-sdk-go-v2

## Phase 2 计划

1. 写 `aws/scripts/{ec2,s3,rds,vpc,secrets,eks,lambda,cloudfront}.sh` (8 个 bash 脚本)
2. 写 `aws/SKILL.md` (meta 入口)
3. 写 `cmd/aws-mcp/main.go` + 编译到 `~/bin/aws-mcp`
4. 端到端测 8 个工具
