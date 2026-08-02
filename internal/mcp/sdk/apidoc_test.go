package sdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileCacheAPIDoc(t *testing.T) {
	cache := &FileCacheAPIDoc{CacheDir: t.TempDir()}
	if _, err := cache.Lookup("tencent-cloud", "cvm", "DescribeInstances"); err == nil {
		t.Fatal("missing cache entry must fail")
	}
	path := filepath.Join(cache.CacheDir, "tencent-cloud", "cvm.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"DescribeInstances":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := cache.Lookup("tencent-cloud", "cvm", "DescribeInstances")
	if err != nil || value != `{"DescribeInstances":{}}` {
		t.Fatalf("lookup=%q err=%v", value, err)
	}
	if err := cache.Refresh("tencent-cloud"); err == nil || !strings.Contains(err.Error(), "Phase 2") {
		t.Fatalf("expected Phase 2 error, got %v", err)
	}
	if err := cache.VerifyCacheIntegrity("tencent-cloud"); err != nil {
		t.Fatal(err)
	}
}

func TestNewFileCacheAPIDocUsesXDGCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cache := NewFileCacheAPIDoc()
	if !strings.HasSuffix(cache.CacheDir, filepath.Join("loom", "cloud-apidocs")) {
		t.Fatalf("unexpected cache dir: %s", cache.CacheDir)
	}
}
