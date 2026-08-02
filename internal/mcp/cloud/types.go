package cloud

import (
	"context"
	"time"
)

type Provider string

const (
	ProviderAWS       Provider = "aws"
	ProviderAzure     Provider = "azure"
	ProviderGCP       Provider = "gcp"
	ProviderAlicloud  Provider = "alicloud"
	ProviderTencent   Provider = "tencent"
	ProviderBaidu     Provider = "baiducloud"
	defaultOutputSize          = 1024 * 1024
)

func AllProviders() []Provider {
	return []Provider{
		ProviderAWS,
		ProviderAzure,
		ProviderGCP,
		ProviderAlicloud,
		ProviderTencent,
		ProviderBaidu,
	}
}

type InvocationMode string

const (
	ModeRead   InvocationMode = "read"
	ModeMutate InvocationMode = "mutate"
)

type ProviderStatus struct {
	Provider         Provider `json:"provider"`
	Available        bool     `json:"available"`
	Adapter          string   `json:"adapter,omitempty"`
	Version          string   `json:"version,omitempty"`
	CredentialSource string   `json:"credential_source,omitempty"`
	Message          string   `json:"message,omitempty"`
}

type DiscoveryRequest struct {
	Provider  Provider
	Service   string
	Operation string
}

type Invocation struct {
	Provider     Provider
	Mode         InvocationMode
	Service      string
	Operation    string
	Region       string
	Project      string
	Subscription string
	Method       string
	URL          string
	Parameters   map[string]any
	Arguments    []string
	Headers      map[string]string
	Body         any
}

type InvocationResult struct {
	Output    []byte
	RequestID string
}

type Adapter interface {
	Status(context.Context) (ProviderStatus, error)
	Discover(context.Context, DiscoveryRequest) ([]byte, error)
	Invoke(context.Context, Invocation) (InvocationResult, error)
}

type AuditEvent struct {
	Time         time.Time      `json:"time"`
	Provider     Provider       `json:"provider"`
	Mode         InvocationMode `json:"mode"`
	Service      string         `json:"service,omitempty"`
	Operation    string         `json:"operation,omitempty"`
	Method       string         `json:"method,omitempty"`
	URL          string         `json:"url,omitempty"`
	Region       string         `json:"region,omitempty"`
	Project      string         `json:"project,omitempty"`
	Subscription string         `json:"subscription,omitempty"`
	Sensitive    bool           `json:"sensitive"`
	Outcome      string         `json:"outcome"`
	RequestID    string         `json:"request_id,omitempty"`
}

type AuditSink func(context.Context, AuditEvent) error

type Runtime struct {
	Adapters         map[Provider]Adapter
	AllowMutations   bool
	AllowSensitive   bool
	AllowedFileRoots []string
	MaxOutputBytes   int
	Audit            AuditSink
	Now              func() time.Time
}

func (runtime Runtime) normalized() Runtime {
	if runtime.Adapters == nil {
		runtime.Adapters = DefaultAdapters()
	}
	if runtime.MaxOutputBytes <= 0 {
		runtime.MaxOutputBytes = defaultOutputSize
	}
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return runtime
}
