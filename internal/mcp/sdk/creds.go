package sdk

// Creds 凭证抽象 - Phase 1 待实现
// 详见 docs/cloud-skills-mcp-design.md §6
type Creds struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
	Region          string
}

func LoadCreds(cloudName string) (*Creds, error) {
	// TODO: Phase 1 实现 - macOS Keychain + env var + 各云 CLI 配置
	return nil, nil
}
