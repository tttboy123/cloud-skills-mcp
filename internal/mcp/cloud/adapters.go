package cloud

// DefaultAdapters is populated incrementally by the provider implementations.
// Keeping construction here makes the MCP contract testable with injected
// adapters while production code uses one canonical registry.
func DefaultAdapters() map[Provider]Adapter {
	return map[Provider]Adapter{
		ProviderAWS:      NewCLIAdapter(CLIAdapterConfig{Provider: ProviderAWS}),
		ProviderAzure:    NewAzureRESTAdapter(AzureRESTConfig{}),
		ProviderGCP:      NewGCPRESTAdapter(GCPRESTConfig{}),
		ProviderAlicloud: NewCLIAdapter(CLIAdapterConfig{Provider: ProviderAlicloud}),
		ProviderTencent:  NewCLIAdapter(CLIAdapterConfig{Provider: ProviderTencent}),
		ProviderBaidu:    NewBaiduRESTAdapter(BaiduRESTConfig{}),
	}
}
