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
- Azure Web PubSub Entra authorization and internal client-token flow: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/concept-azure-ad-authorization
- Azure Web PubSub Generate Client Token REST API: https://learn.microsoft.com/en-us/rest/api/webpubsub/dataplane/web-pub-sub/generate-client-token?view=rest-webpubsub-dataplane-2024-01-01
- Azure Web PubSub JSON WebSocket subprotocol: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-json-webpubsub-subprotocol
- Azure Web PubSub reliable JSON recovery, publisher resend, and sequence acknowledgement: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/howto-develop-reliable-clients
- Azure Web PubSub reliable JSON protocol reference: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-json-reliable-webpubsub-subprotocol
- Azure Web PubSub Protobuf protocol and current proto3 field schema: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-protobuf-webpubsub-subprotocol
- Azure Web PubSub reliable Protobuf protocol: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-protobuf-reliable-webpubsub-subprotocol
- Azure Web PubSub service-supported subprotocol tutorial: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/tutorial-subprotocol
- Azure Web PubSub maintained protocol schema repository: https://github.com/Azure/azure-webpubsub/tree/main/protocols
- Azure Web PubSub MQTT WSS endpoint, client ID, keepalive, token, and permission rules: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/howto-connect-mqtt-websocket-client
- Azure Web PubSub MQTT supported and unsupported protocol features: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-mqtt-support-status
- Azure Web PubSub MQTT data-plane mappings and required `clientType=MQTT`: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/reference-rest-api-mqtt
- OASIS MQTT 5.0 control packets, properties, QoS state machines, and reason codes: https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html
- Azure Web PubSub WebSocket Authorization header: https://learn.microsoft.com/en-us/azure/azure-web-pubsub/howto-websocket-connect
- Azure Health Data Services authentication and FHIR/DICOM audiences: https://learn.microsoft.com/en-us/azure/healthcare-apis/authentication-authorization
- Azure Health Data Services FHIR/DICOM access-token and endpoint examples: https://learn.microsoft.com/en-us/azure/healthcare-apis/fhir/using-curl
- Azure API for FHIR legacy service endpoint: https://learn.microsoft.com/en-us/azure/healthcare-apis/azure-api-for-fhir/azure-api-fhir-resource-manager-template
- Azure Maps Microsoft Entra authentication and public/geographic endpoints: https://learn.microsoft.com/en-us/azure/azure-maps/azure-maps-authentication
- Azure Maps daemon scope (`https://atlas.microsoft.com/.default`): https://learn.microsoft.com/en-us/azure/azure-maps/how-to-secure-daemon-app
- Azure environment endpoint metadata for public, China, US Government, and other clouds: https://learn.microsoft.com/en-us/powershell/module/Az.Accounts/get-azenvironment
- Blob Get Blob, Entra authorization, and Range download: https://learn.microsoft.com/en-us/rest/api/storageservices/get-blob
- Azure OpenAI Realtime WebSocket, GA/preview URLs, Entra scope, and event flow: https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/realtime-audio-websockets
- Azure Voice Live WSS endpoints, Entra scopes, model/Agent query parameters, events, audio enhancements, and avatar boundary: https://learn.microsoft.com/en-us/azure/ai-services/speech-service/voice-live-how-to
- Azure Voice Live SDK quickstart with `DefaultAzureCredential`, finite session events, and current `2026-04-10` endpoint vector: https://learn.microsoft.com/en-us/azure/ai-services/speech-service/voice-live-quickstart

The adapter uses only EnvironmentCredential, WorkloadIdentityCredential, and ManagedIdentityCredential. Azure CLI credentials are intentionally excluded. Azure OpenAI Realtime obtains the documented `https://ai.azure.com/.default` token internally, sends it only in the WSS Authorization header, validates JSON event framing and terminal events, and never accepts the API-key alternative. Voice Live uses the same bounded event engine on exact Foundry or legacy Speech WSS endpoints and selects `https://ai.azure.com/.default` or `https://cognitiveservices.azure.com/.default` from that endpoint. Model sessions remain read-only; configured Agent sessions require mutation approval, and credential-bearing avatar/ICE events fail before output. Azure Web PubSub obtains `https://webpubsub.azure.com/.default`, calls the fixed Generate Client Token API internally, and sends the five-minute client token only in the validated WSS Authorization header. It supports standard and reliable JSON/Protobuf subprotocols plus finite MQTT 3.1.1/5.0 read or bidirectional client profiles whose token requests include `clientType=MQTT`. Protobuf wire fields, Any/binary data, streaming messages, uint64 acknowledgement and reliable recovery are handled inside the server; MQTT publish, QoS 2, persistent sessions, Last Will, MQTT 5 message/session properties, subscription identifiers, negotiated flow-control limits, and server disconnect are implemented. Client and recovery tokens are never returned. Client-certificate and CONNECT username/password authentication remain intentionally outside the operator-IAM-only MCP credential boundary.
