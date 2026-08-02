package cloud

// DefaultAdapters is populated incrementally by the provider implementations.
// Keeping construction here makes the MCP contract testable with injected
// adapters while production code uses one canonical registry.
func DefaultAdapters() map[Provider]Adapter {
	return DefaultAdaptersWithEndpointHosts(nil)
}

func DefaultAdaptersWithEndpointHosts(allowed map[Provider][]string) map[Provider]Adapter {
	return map[Provider]Adapter{
		ProviderAWS:      NewAWSRESTAdapter(AWSRESTConfig{AllowedHosts: allowed[ProviderAWS]}),
		ProviderAzure:    NewAzureRESTAdapter(AzureRESTConfig{AllowedHosts: allowed[ProviderAzure]}),
		ProviderGCP:      NewGCPRESTAdapter(GCPRESTConfig{AllowedHosts: allowed[ProviderGCP]}),
		ProviderAlicloud: NewAlibabaRESTAdapter(AlibabaRESTConfig{AllowedHosts: allowed[ProviderAlicloud]}),
		ProviderTencent:  NewTencentRESTAdapter(TencentRESTConfig{AllowedHosts: allowed[ProviderTencent]}),
		ProviderBaidu:    NewBaiduRESTAdapter(BaiduRESTConfig{AllowedHosts: allowed[ProviderBaidu]}),
	}
}
