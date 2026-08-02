# Azure official references

- Azure REST API reference: https://learn.microsoft.com/en-us/rest/api/azure/
- `az rest`: https://learn.microsoft.com/en-us/cli/azure/reference-index?view=azure-cli-latest#az-rest
- Azure CLI authentication: https://learn.microsoft.com/en-us/cli/azure/authenticate-azure-cli?view=azure-cli-latest
- Service-principal authentication: https://learn.microsoft.com/en-us/cli/azure/authenticate-azure-cli-service-principal?view=azure-cli-latest
- Azure Identity authentication for Go: https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/authentication-overview
- Azure Identity credential chains for Go: https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/credential-chains
- Azure Resource Graph: https://learn.microsoft.com/en-us/azure/governance/resource-graph/overview
- Azure MCP Server overview: https://learn.microsoft.com/en-us/azure/developer/azure-mcp-server/overview

`az rest` automatically supplies a token for the logged-in identity. The caller remains responsible for using the documented provider path, API version, and least-privilege role.
