---
name: azure-cloud
description: Operate or inspect any Azure resource or Microsoft Graph API through cloud-skills-mcp and the official Azure CLI identity chain. Use for Azure, ARM, Entra ID, Microsoft Graph, subscriptions, resource groups, or any documented Azure REST API.
---

# Azure Cloud

Use the unified MCP server as a guarded Azure REST gateway. With Service Principal, Workload Identity, or Managed Identity configuration it uses the official Go `DefaultAzureCredential` chain directly for ARM, Microsoft Graph, and common data-plane audiences. Other validated Azure endpoints fall back to `az rest`.

## Workflow

1. Call `cloud_provider_status` with `provider="azure"`.
2. Verify the resource-provider API version in the official reference. `azure_api_discover` explains the adapter but cannot choose an API version for you.
3. Use `azure_api_read` for `GET`, `HEAD`, or `OPTIONS` only.
4. For `POST`, `PUT`, `PATCH`, or `DELETE`, obtain explicit human approval for the tenant/subscription, target URL, method, body, and effect; then use `azure_api_mutate(force=true)`.
5. Secret, credential, token, password, and access-key endpoints require the separate sensitive gate.
6. Do not place bearer tokens, SAS signatures, client secrets, cookies, or API keys in URL/query/header arguments.

## MCP arguments

- `method` and `url`: exact documented Azure REST request. Hosts are restricted to official Azure/Microsoft domains.
- `subscription`: optional subscription passed to Azure CLI.
- `audience`: optional Microsoft Entra resource/application audience for an uncommon official data-plane endpoint. Supply the documented audience, not a token; the server derives the `.default` scope internally and passes it only to `az rest --resource` on CLI fallback.
- `headers`: non-credential headers such as `If-Match`.
- `body`: JSON-compatible request body.
- `body_file`: binary/media request body under an operator-approved `CLOUD_SKILLS_ALLOWED_FILE_ROOTS` directory. Do not combine it with `body`; use provider multipart/chunk APIs above 64 MiB.
- `arguments`: optional safe `az rest` arguments; authentication and endpoint override flags are rejected.

Example read: `azure_api_read(method="GET", url="https://management.azure.com/subscriptions/<id>/resources?api-version=2021-04-01", subscription="<id>")`.

Common ARM, Graph, Storage, Key Vault, SQL, Service Bus, Monitor, App Configuration, Search, Databricks, Grafana, Web PubSub, SignalR, Digital Twins, Synapse, Log Analytics, and ACR endpoints have built-in audience routing. Use the endpoint's official documentation for an explicit `audience` value.

Public Azure, Azure operated by 21Vianet, Azure US Government, and legacy Germany endpoint suffixes are validated. Configure the matching Azure cloud/authority in the official Azure CLI or Azure Identity environment before calling a sovereign endpoint.

## Credentials

Authenticate the server process through managed identity, workload identity, an Azure service principal (`AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`), or `az login`. Set `CLOUD_SKILLS_AZURE_USE_DEFAULT_CREDENTIAL=1` to explicitly select DefaultAzureCredential in an Azure-hosted environment without identity hint variables. Credentials stay in Azure Identity/Azure CLI state and are never MCP parameters.

Read [references/official-docs.md](references/official-docs.md) before selecting authentication or an API version.
