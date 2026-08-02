# Azure official references

- Azure REST API reference: https://learn.microsoft.com/en-us/rest/api/azure/
- `az rest`: https://learn.microsoft.com/en-us/cli/azure/reference-index?view=azure-cli-latest#az-rest
- Azure CLI authentication: https://learn.microsoft.com/en-us/cli/azure/authenticate-azure-cli?view=azure-cli-latest
- Service-principal authentication: https://learn.microsoft.com/en-us/cli/azure/authenticate-azure-cli-service-principal?view=azure-cli-latest
- Azure Identity authentication for Go: https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/authentication-overview
- Azure Identity credential chains for Go: https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/credential-chains
- Azure Resource Graph: https://learn.microsoft.com/en-us/azure/governance/resource-graph/overview
- Azure MCP Server overview: https://learn.microsoft.com/en-us/azure/developer/azure-mcp-server/overview
- App Configuration data-plane REST and Entra audience: https://learn.microsoft.com/en-us/azure/azure-app-configuration/rest-api and https://learn.microsoft.com/en-us/azure/azure-app-configuration/concept-enable-rbac
- Azure data-plane endpoint/audience model: https://learn.microsoft.com/en-us/azure/developer/terraform/concept-azapi-data-plane-framework
- Azure Web PubSub data-plane authentication: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-rest-api-data-plane
- Azure environment endpoint metadata for public, China, US Government, and other clouds: https://learn.microsoft.com/en-us/powershell/module/Az.Accounts/get-azenvironment

`az rest` automatically supplies a token for the logged-in identity. The caller remains responsible for using the documented provider path, API version, and least-privilege role.
