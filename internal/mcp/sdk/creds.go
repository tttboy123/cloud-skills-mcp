// Package sdk provides shared infrastructure for the 6 cloud MCP servers.
//
// creds.go — credential abstraction with 3-tier fallback:
//  1. macOS Keychain (per-cloud service name, see CloudConfig)
//  2. env vars (per-cloud variable set)
//  3. cloud-CLI's own config file (e.g. ~/.aws/credentials, ~/.tencentcloud/credentials)
//
// The 6 supported clouds are aws / azure / gcp / alicloud / tencent-cloud / baiducloud.
package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Cloud identifies a cloud provider. The string is the canonical short name
// used in CloudConfig maps; it must match the SKILL.md directory name.
type Cloud string

const (
	CloudAWS        Cloud = "aws"
	CloudAzure      Cloud = "azure"
	CloudGCP        Cloud = "gcp"
	CloudAlicloud   Cloud = "alicloud"
	CloudTencent    Cloud = "tencent-cloud"
	CloudBaiducloud Cloud = "baiducloud"
)

// Creds is the uniform credential struct returned to all MCP servers.
// Field semantics:
//
//	AccessKeyID     — primary access identifier (for tccli this is the SecretId,
//	                  for AWS this is the access key id; see per-cloud notes)
//	AccessKeySecret — secret half of the key pair
//	SecurityToken   — STS / OAuth token (optional, AWS / Alibaba use this for temporary creds)
//	Region          — default region if not supplied per-call
//	Source          — diagnostic tag: "keychain" | "env" | "cli-config" | "none"
//	Account         — for multi-account scenarios, the Keychain account name (e.g. "tccli-secretid")
type Creds struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
	Region          string
	Source          string
	Account         string
}

// CloudConfig is the per-cloud wiring used by LoadCreds.
// Adding a new cloud = add a new entry here + a new env function + a new CLI-config parser.
type CloudConfig struct {
	// KeychainService is the service name passed to `security find-generic-password -s <name>`.
	// For non-macOS platforms (CI / Linux), LoadCreds skips Keychain and falls through to env.
	KeychainService string
	// KeychainAccountAKID / KeychainAccountSecret are the account names under the service.
	// For most clouds the convention is "<cli>-secretid" / "<cli>-secretkey".
	KeychainAccountAKID   string
	KeychainAccountSecret string
	KeychainAccountToken  string // optional STS account
	KeychainAccountRegion string // optional region account
	// EnvAKID / EnvSecret / EnvToken / EnvRegion are the env var names the cloud SDK reads.
	EnvAKID   []string // try in order, first non-empty wins
	EnvSecret []string
	EnvToken  []string
	EnvRegion []string
	// DefaultRegion is used when neither Keychain nor env supplies one.
	DefaultRegion string
	// CLIBinary is the cloud's CLI; presence is checked before parsing CLI config.
	CLIBinary string
	// CLICredPath is the canonical credentials file for this cloud (relative to $HOME).
	// Empty means "no file to parse" (e.g. GCP uses gcloud auth, not a file).
	CLICredPath string
	// ParseCLIConfig reads the CLICredPath and returns (akid, secret, region, error).
	// nil means "not supported — skip tier 3".
	ParseCLIConfig func(home string) (akid, secret, region string, err error)
	// EnvPrefixHint is shown in error messages to help the user fix their env.
	EnvPrefixHint string
}

// registry holds the 6 cloud configs. Populated by init() below.
var registry = map[Cloud]CloudConfig{}

// SupportedClouds returns the list of clouds registered in init().
func SupportedClouds() []Cloud {
	out := make([]Cloud, 0, len(registry))
	for c := range registry {
		out = append(out, c)
	}
	return out
}

// IsValidCloud returns true if c is in the registry.
func IsValidCloud(c Cloud) bool {
	_, ok := registry[c]
	return ok
}

// LoadCreds resolves credentials for the given cloud. It is the only function
// the per-cloud MCP servers should call; the tier-1/2/3 fallback logic lives here.
//
// Errors mention the cloud name + the next remediation step (e.g. "run setup-keychain.sh")
// so the LLM can self-correct on the first failure without leaking secret material.
func LoadCreds(cloud Cloud) (*Creds, error) {
	cfg, ok := registry[cloud]
	if !ok {
		return nil, fmt.Errorf("unknown cloud %q (supported: %s)", cloud, joinClouds(SupportedClouds()))
	}

	// Tier 1: macOS Keychain (only on darwin + only if `security` exists).
	if runtime.GOOS == "darwin" && hasCommand("security") {
		if c, err := loadFromKeychain(cfg); err == nil && c.AccessKeyID != "" {
			return c, nil
		}
	}

	// Tier 2: env vars.
	if c, ok := loadFromEnv(cfg); ok {
		return c, nil
	}

	// Tier 3: cloud-CLI's own config file.
	if cfg.ParseCLIConfig != nil {
		home, _ := os.UserHomeDir()
		if home != "" && cfg.CLICredPath != "" {
			if c, err := loadFromCLIConfig(cfg, home); err == nil && c.AccessKeyID != "" {
				return c, nil
			}
		}
	}

	// All tiers empty — produce a single, helpful error.
	return nil, fmt.Errorf(
		"no credentials for cloud %q. Tried: keychain service=%q, env vars [%s], %s config file. "+
			"Fix: run %s setup or export %s",
		cloud,
		cfg.KeychainService,
		strings.Join(allEnvNames(cfg), ", "),
		cliHint(cfg),
		cliHint(cfg),
		cfg.EnvPrefixHint,
	)
}

// loadFromKeychain reads AKID/secret/(token)/(region) from macOS Keychain.
// A keychain source is accepted only when both halves of the credential pair
// exist; partial entries fall through to the next credential tier.
func loadFromKeychain(cfg CloudConfig) (*Creds, error) {
	return loadFromKeychainWithLookup(cfg, keychainGet)
}

func loadFromKeychainWithLookup(cfg CloudConfig, lookup func(service, account string) (string, error)) (*Creds, error) {
	c := &Creds{Source: "keychain", Account: cfg.KeychainAccountAKID}
	if v, err := lookup(cfg.KeychainService, cfg.KeychainAccountAKID); err == nil {
		c.AccessKeyID = v
		c.Account = cfg.KeychainAccountAKID
	}
	if v, err := lookup(cfg.KeychainService, cfg.KeychainAccountSecret); err == nil {
		c.AccessKeySecret = v
	}
	if cfg.KeychainAccountToken != "" {
		if v, err := lookup(cfg.KeychainService, cfg.KeychainAccountToken); err == nil {
			c.SecurityToken = v
		}
	}
	if cfg.KeychainAccountRegion != "" {
		if v, err := lookup(cfg.KeychainService, cfg.KeychainAccountRegion); err == nil {
			c.Region = v
		}
	}
	if c.Region == "" {
		c.Region = cfg.DefaultRegion
	}
	if c.AccessKeyID == "" {
		return c, fmt.Errorf("keychain service=%q has no entry for account=%q", cfg.KeychainService, cfg.KeychainAccountAKID)
	}
	if c.AccessKeySecret == "" {
		return c, fmt.Errorf("keychain service=%q has no entry for account=%q", cfg.KeychainService, cfg.KeychainAccountSecret)
	}
	return c, nil
}

// loadFromEnv tries each env var in the order given; first non-empty wins.
func loadFromEnv(cfg CloudConfig) (*Creds, bool) {
	akid := firstEnv(cfg.EnvAKID)
	secret := firstEnv(cfg.EnvSecret)
	if akid == "" || secret == "" {
		return nil, false
	}
	c := &Creds{
		AccessKeyID:     akid,
		AccessKeySecret: secret,
		SecurityToken:   firstEnv(cfg.EnvToken),
		Region:          firstEnv(cfg.EnvRegion),
		Source:          "env",
	}
	if c.Region == "" {
		c.Region = cfg.DefaultRegion
	}
	return c, true
}

// loadFromCLIConfig delegates to the per-cloud parser. Some clouds (GCP) have
// no file format; ParseCLIConfig is nil and we skip.
func loadFromCLIConfig(cfg CloudConfig, home string) (*Creds, error) {
	akid, secret, region, err := cfg.ParseCLIConfig(home)
	if err != nil {
		return nil, err
	}
	if akid == "" {
		return nil, fmt.Errorf("cli config at %s has no AKID", filepath.Join(home, cfg.CLICredPath))
	}
	if secret == "" && len(cfg.EnvSecret) > 0 {
		return nil, fmt.Errorf("cli config at %s has no credential secret", filepath.Join(home, cfg.CLICredPath))
	}
	c := &Creds{
		AccessKeyID:     akid,
		AccessKeySecret: secret,
		Region:          region,
		Source:          "cli-config",
	}
	if c.Region == "" {
		c.Region = cfg.DefaultRegion
	}
	return c, nil
}

// keychainGet runs `security find-generic-password -s S -a A -w` and returns the password.
// Errors are returned (no fallthrough inside the function) so the caller can decide
// whether to surface them or fall through to tier 2.
func keychainGet(service, account string) (string, error) {
	if service == "" || account == "" {
		return "", fmt.Errorf("keychain: empty service or account")
	}
	out, err := exec.Command("security", "find-generic-password",
		"-s", service, "-a", account, "-w").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// hasCommand returns true if `name` is in PATH.
func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// firstEnv returns the first non-empty value among the names, or "".
func firstEnv(names []string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// allEnvNames flattens all env var lists for error messages.
func allEnvNames(cfg CloudConfig) []string {
	var out []string
	out = append(out, cfg.EnvAKID...)
	out = append(out, cfg.EnvSecret...)
	out = append(out, cfg.EnvToken...)
	out = append(out, cfg.EnvRegion...)
	return out
}

// joinClouds formats a []Cloud as "aws, azure, gcp, alicloud, tencent-cloud, baiducloud".
func joinClouds(cs []Cloud) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = string(c)
	}
	return strings.Join(parts, ", ")
}

// cliHint returns a human hint for "run this CLI to authenticate".
func cliHint(cfg CloudConfig) string {
	if cfg.CLIBinary == "" {
		return "cloud-specific"
	}
	return fmt.Sprintf("`%s` configure", cfg.CLIBinary)
}

// iniValue reads a single key from an INI-style file. Used by the AWS / Tencent parsers.
// Returns "" if the file is missing or the key is not present.
func iniValue(path, section, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	inSection := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			inSection = (sec == section)
			continue
		}
		if !inSection {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		if k != key {
			continue
		}
		v := strings.TrimSpace(line[eq+1:])
		return v, nil
	}
	return "", nil
}

// jsonValue reads a nested key from a JSON file using path segments.
// Example: jsonValue(path, "current-context") or jsonValue(path, "users", "0", "user", "exec", "args").
// Returns "" if any segment is missing.
func jsonValue(path string, segments ...string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var node any
	if err := json.Unmarshal(data, &node); err != nil {
		return "", err
	}
	for _, seg := range segments {
		switch current := node.(type) {
		case map[string]any:
			var ok bool
			node, ok = current[seg]
			if !ok {
				return "", nil
			}
		case []any:
			index, err := strconv.Atoi(seg)
			if err != nil || index < 0 || index >= len(current) {
				return "", nil
			}
			node = current[index]
		default:
			return "", nil
		}
	}
	if s, ok := node.(string); ok {
		return s, nil
	}
	return "", nil
}

// shellOut runs a shell command and returns trimmed stdout. Used by GCP's
// `gcloud config get-value` since GCP has no static credentials file.
func shellOut(name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
