# Azure official references

- Azure REST API reference: https://learn.microsoft.com/en-us/rest/api/azure/
- Service-principal authentication for Go: https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/authentication-on-premises-apps
- Azure Identity authentication for Go: https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/authentication-overview
- Azure Identity credential chains for Go: https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/credential-chains
- Azure Resource Graph: https://learn.microsoft.com/en-us/azure/governance/resource-graph/overview
- Azure MCP Server overview: https://learn.microsoft.com/en-us/azure/developer/azure-mcp-server/overview
- App Configuration data-plane REST and Entra audience: https://learn.microsoft.com/en-us/azure/azure-app-configuration/rest-api and https://learn.microsoft.com/en-us/azure/azure-app-configuration/concept-enable-rbac
- Azure data-plane endpoint/audience model: https://learn.microsoft.com/en-us/azure/developer/terraform/concept-azapi-data-plane-framework
- Azure Web PubSub data-plane authentication: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-rest-api-data-plane
- Azure Health Data Services authentication and FHIR/DICOM audiences: https://learn.microsoft.com/en-us/azure/healthcare-apis/authentication-authorization
- Azure Health Data Services FHIR/DICOM access-token and endpoint examples: https://learn.microsoft.com/en-us/azure/healthcare-apis/fhir/using-curl
- Azure API for FHIR legacy service endpoint: https://learn.microsoft.com/en-us/azure/healthcare-apis/azure-api-for-fhir/azure-api-fhir-resource-manager-template
- Azure Maps Microsoft Entra authentication and public/geographic endpoints: https://learn.microsoft.com/en-us/azure/azure-maps/azure-maps-authentication
- Azure Maps daemon scope (`https://atlas.microsoft.com/.default`): https://learn.microsoft.com/en-us/azure/azure-maps/how-to-secure-daemon-app
- Azure environment endpoint metadata for public, China, US Government, and other clouds: https://learn.microsoft.com/en-us/powershell/module/Az.Accounts/get-azenvironment
- Blob Get Blob, Entra authorization, and Range download: https://learn.microsoft.com/en-us/rest/api/storageservices/get-blob
- Azure OpenAI Realtime WebSocket, GA/preview URLs, Entra scope, and event flow: https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/realtime-audio-websockets

The adapter uses only EnvironmentCredential, WorkloadIdentityCredential, and ManagedIdentityCredential. Azure CLI credentials are intentionally excluded. Azure OpenAI Realtime obtains the documented `https://ai.azure.com/.default` token internally, sends it only in the WSS Authorization header, validates JSON event framing and terminal events, and never accepts the API-key alternative.
