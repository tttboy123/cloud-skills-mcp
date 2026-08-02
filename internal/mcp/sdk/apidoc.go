// apidoc.go — OpenAPI / API documentation fetcher framework.
//
// This is a SKELETON for Phase 2. The plan is:
//
//  1. Each cloud publishes a machine-readable API catalog:
//     AWS       — https://docs.aws.amazon.com/cli/latest/reference/<svc>/index.html
//     Azure     — https://learn.microsoft.com/en-us/rest/api/<svc>/
//     GCP       — https://cloud.google.com/<svc>/docs/reference/rest
//     Alicloud  — https://api.aliyun.com/api/<svc>/<ver>
//     Tencent   — https://cloud.tencent.com/document/api/<product-code>/<version>
//     Baidu BCE — https://cloud.baidu.com/doc/<product>/index.html
//
//  2. We pre-fetch the JSON / YAML for the products we care about (CVM, EC2, ...)
//     at SKILL.md install time, cache to ~/.cache/loom/cloud-apidocs/<cloud>/<svc>.json.
//
//  3. At runtime, each tool's handler can call APIDoc(cloud, service, action) to
//     render a parameter cheat sheet into the LLM's context window.
//
// Phase 1 just needs the signature + a clear "not yet implemented" return.
package sdk

import (
	"fmt"
	"os"
	"path/filepath"
)

// APIDoc is a tiny interface for "give me the OpenAPI params for this action".
// Implementations are per-cloud and live in each cloud's MCP main package.
type APIDoc interface {
	// Lookup returns the JSON-schema input for `cloud.service.action`,
	// or an error if the doc has not been cached.
	Lookup(cloud, service, action string) (jsonSchema string, err error)
	// Refresh downloads the API doc bundle for one cloud to the local cache.
	// Phase 1: no-op stub. Phase 2: actually hit the cloud's catalog URL.
	Refresh(cloud string) error
}

// FileCacheAPIDoc is the default APIDoc implementation: it reads cached JSON
// files from a directory (typically ~/.cache/loom/cloud-apidocs/<cloud>/<svc>.json).
// Refresh is unimplemented in Phase 1 — we return a clear error so the caller
// knows to either run `loom apidoc refresh` or fall back to the SKILL.md
// references/ directory.
type FileCacheAPIDoc struct {
	// CacheDir is the root under which <cloud>/<service>.json files live.
	CacheDir string
}

// NewFileCacheAPIDoc returns a FileCacheAPIDoc rooted at the default cache dir.
// On Linux/macOS this is $XDG_CACHE_HOME/loom/cloud-apidocs or $HOME/.cache/loom/cloud-apidocs.
func NewFileCacheAPIDoc() *FileCacheAPIDoc {
	dir := os.Getenv("XDG_CACHE_HOME")
	if dir == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			dir = filepath.Join(home, ".cache")
		} else {
			dir = "/tmp"
		}
	}
	return &FileCacheAPIDoc{CacheDir: filepath.Join(dir, "loom", "cloud-apidocs")}
}

// Lookup reads <CacheDir>/<cloud>/<service>.json and returns the action block.
// If the file does not exist, it returns ErrNotCached (so the tool handler can
// fall back to the SKILL.md references).
func (a *FileCacheAPIDoc) Lookup(cloud, service, action string) (string, error) {
	path := filepath.Join(a.CacheDir, cloud, service+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("apidoc not cached for %s/%s (run `loom apidoc refresh %s` first): %w",
				cloud, service, cloud, err)
		}
		return "", err
	}
	return string(data), nil
}

// Refresh is a Phase-2 placeholder. The real implementation will hit each
// cloud's OpenAPI catalog and write JSON to CacheDir. For now, it always
// returns a clear "not implemented" error.
func (a *FileCacheAPIDoc) Refresh(cloud string) error {
	return fmt.Errorf("FileCacheAPIDoc.Refresh is Phase 2; in Phase 1, the SKILL.md references/ directory is the source of truth for %s API docs", cloud)
}

// VerifyCacheIntegrity is a stub for Phase 2 that will hash-check the cache
// and re-fetch stale files. Phase 1 always returns nil.
func (a *FileCacheAPIDoc) VerifyCacheIntegrity(_ string) error {
	return nil
}
