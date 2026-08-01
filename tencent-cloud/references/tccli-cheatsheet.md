# tccli 命令速查

## CVM (云服务器)

| 操作 | 命令 |
|---|---|
| 列实例 | `tccli cvm DescribeInstances --region ap-guangzhou` |
| 查详情 | `tccli cvm DescribeInstances --region ap-guangzhou --InstanceIds.0 ins-xxx` |
| 开机 | `tccli cvm StartInstances --region ap-guangzhou --InstanceIds.0 ins-xxx` |
| 关机 | `tccli cvm StopInstances --region ap-guangzhou --InstanceIds.0 ins-xxx --StopType SOFT` |
| 重启 | `tccli cvm RebootInstances --region ap-guangzhou --InstanceIds.0 ins-xxx` |
| 重置密码 | `tccli cvm ResetInstancesPassword --region ap-guangzhou --InstanceIds.0 ins-xxx --Password newpass` |
| 销毁 | `tccli cvm TerminateInstances --region ap-guangzhou --InstanceIds.0 ins-xxx` ⚠️ |
| 列镜像 | `tccli cvm DescribeImages --region ap-guangzhou` |
| 创建实例 | `tccli cvm RunInstances --region ap-guangzhou --ImageId img-xxx --InstanceType S5.SMALL2 ...` |

**InstanceState 状态码**:
- `0` 创建中
- `1` 运行中
- `2` 开机中
- `3` 关机中
- `4` 已关机
- `5` 已销毁

## CDB (云数据库 MySQL)

| 操作 | 命令 |
|---|---|
| 列实例 | `tccli cdb DescribeDBInstances --region ap-guangzhou` |
| 启动 | `tccli cdb StartDBInstances --region ap-guangzhou --InstanceIds.0 cdb-xxx` |
| 关闭 | `tccli cdb StopDBInstances --region ap-guangzhou --InstanceIds.0 cdb-xxx` |
| 重启 | `tccli cdb RestartDBInstances --region ap-guangzhou --InstanceIds.0 cdb-xxx` |
| 慢日志 | `tccli cdb DescribeSlowLogData --region ap-guangzhou --InstanceId cdb-xxx --StartTime 2026-08-01T00:00:00Z --EndTime 2026-08-01T23:59:59Z` |
| 备份列表 | `tccli cdb DescribeBackups --region ap-guangzhou --InstanceId cdb-xxx` |

**Status 状态码**:
- `0` 创建中
- `1` 运行中
- `4` 隔离中
- `5` 已删除

## COS (对象存储)

| 操作 | 命令 |
|---|---|
| 列桶 | `tccli cos GetService` |
| 列对象 | `tccli cos ListObjects --Bucket my-bucket-1300000000 --Region ap-guangzhou --Prefix logs/` |
| 下载 | `tccli cos GetObject --Bucket my-bucket-1300000000 --Region ap-guangzhou --Key file.txt --bin /tmp/out` |
| 上传 | `tccli cos PutObject --Bucket my-bucket-1300000000 --Region ap-guangzhou --Key file.txt --Body "..."` |
| 删除对象 | `tccli cos DeleteObject --Bucket my-bucket-1300000000 --Region ap-guangzhou --Key file.txt` ⚠️ |
| 创建桶 | `tccli cos CreateBucket --Bucket my-bucket-1300000000 --Region ap-guangzhou` |
| 删桶 | `tccli cos DeleteBucket --Bucket my-bucket-1300000000 --Region ap-guangzhou` ⚠️ |

**注意 Bucket 名格式**: `<bucket-name>-<APPID>`, APPID 在腾讯云账号信息里看

## CloudBase (Serverless)

| 操作 | 命令 |
|---|---|
| 列环境 | `tccli tcb DescribeEnvList` |
| 列云函数 | `tccli scf ListFunctions --region ap-guangzhou --Namespace cloudbase-envid` |
| 触发函数 | `tccli scf Invoke --region ap-guangzhou --FunctionName xxx --Payload '{"key":"value"}'` |
| 查环境数据库 | `tccli tcb DescribeDatabase --EnvId envid` |
| 查环境存储 | `tccli tcb DescribeStorage --EnvId envid` |

**CloudBase 推荐**: 实际场景优先用 `tcb` CLI (`npm i -g @cloudbase/cli`), 比 tccli tcb 子命令好用

## CLB (负载均衡)

| 操作 | 命令 |
|---|---|
| 列 CLB | `tccli clb DescribeLoadBalancers --region ap-guangzhou` |
| 查 CLB | `tccli clb DescribeLoadBalancers --region ap-guangzhou --LoadBalancerIds.0 lb-xxx` |

## 通用

- `--region` 默认 `ap-guangzhou`, 改成 `ap-shanghai` / `ap-beijing` / `ap-hongkong`
- `--profile` 用其他 profile, 默认 `default`
- `--cli-unfold-argument` 自动展开数组
- `--output json|table|yaml` 输出格式
- `--filter` jq 风格过滤

## 凭证优先级

1. 命令行参数: `--secret-id xxx --secret-key yyy`
2. 环境变量: `TENCENTCLOUD_SECRETID` / `TENCENTCLOUD_SECRETKEY`
3. 本文件 ~/.tencentcloud/credentials (CLI 默认, 我们不用, 走 Keychain)
4. **本 skill 走 macOS Keychain** (setup-keychain.sh)
