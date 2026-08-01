# azure — Microsoft Azure Manager (待写)

⏳ **Phase 2 实现**

## 计划覆盖

| 服务 | CLI 命令 | 状态 |
|---|---|---|
| **VM** (虚拟机) | `az vm list / start / stop / restart` | ⏳ |
| **Blob Storage** (对象存储) | `az storage blob list / upload / download` | ⏳ |
| **SQL Database** | `az sql db list` | ⏳ |
| **Virtual Network** | `az network vnet list` | ⏳ |
| **Key Vault** | `az keyvault secret show` | ⏳ |
| **AKS** (Kubernetes) | `az aks list` | ⏳ |
| **Functions** (Serverless) | `az functionapp list / invoke` | ⏳ |
| **CDN** | `az cdn profile list` | ⏳ |

## 凭证

```bash
# Azure CLI
az login

# 或 Service Principal
az ad sp create-for-rbac --name my-app --role contributor --scopes /subscriptions/<SUB_ID>

# 或 macOS Keychain
security add-generic-password -s azure -a tenant-id -w <TENANT_ID>
security add-generic-password -s azure -a client-id -w <CLIENT_ID>
security add-generic-password -s azure -a client-secret -w <SECRET>
security add-generic-password -s azure -a subscription-id -w <SUB_ID>
```

## SDK 选型

- `azure-sdk-for-go` (官方, Go)
- 来源: https://github.com/Azure/azure-sdk-for-go

## Phase 2 计划

1. 写 `azure/scripts/{vm,blob,sql,vnet,keyvault,aks,functions,cdn}.sh` (8 个 bash 脚本)
2. 写 `azure/SKILL.md` (meta 入口)
3. 写 `cmd/azure-mcp/main.go` + 编译到 `~/bin/azure-mcp`
4. 端到端测 8 个工具
