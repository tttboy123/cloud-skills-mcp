package cloud

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingResponseBody struct{}

func (failingResponseBody) Read([]byte) (int, error) { return 0, errors.New("stream interrupted") }
func (failingResponseBody) Close() error             { return nil }

func TestReadRESTResponseStreamsSuccessfulBodyToNewFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "object.bin")
	response := &http.Response{
		StatusCode: http.StatusPartialContent,
		Header:     http.Header{"X-Request-Id": []string{"request-123"}},
		Body:       io.NopCloser(strings.NewReader("binary-object-data")),
	}

	output, err := readRESTResponseWithFile(response, 8, target, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "binary-object-data" {
		t.Fatalf("download=%q err=%v", data, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	var metadata struct {
		ResponseFile string `json:"response_file"`
		Bytes        int64  `json:"bytes"`
		RequestID    string `json:"request_id"`
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		t.Fatalf("metadata=%q err=%v", output, err)
	}
	if metadata.ResponseFile != target || metadata.Bytes != int64(len(data)) || metadata.RequestID != "request-123" {
		t.Fatalf("metadata=%+v", metadata)
	}
	if strings.Contains(string(output), "binary-object-data") {
		t.Fatalf("download body leaked into MCP output: %s", output)
	}
}

func TestReadRESTResponseFileNeverOverwritesOrLeavesPartialFiles(t *testing.T) {
	root := t.TempDir()
	t.Run("existing target", func(t *testing.T) {
		target := filepath.Join(root, "existing.bin")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("replace"))}
		if _, err := readRESTResponseWithFile(response, 8, target, 1024); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("error=%v", err)
		}
		data, _ := os.ReadFile(target)
		if string(data) != "keep" {
			t.Fatalf("existing target changed: %q", data)
		}
	})

	t.Run("download limit", func(t *testing.T) {
		target := filepath.Join(root, "oversized.bin")
		response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("too-large"))}
		if _, err := readRESTResponseWithFile(response, 8, target, 3); err == nil || !strings.Contains(err.Error(), "download exceeds") {
			t.Fatalf("error=%v", err)
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("partial target exists: %v", err)
		}
		matches, err := filepath.Glob(filepath.Join(root, ".cloud-skills-download-*"))
		if err != nil || len(matches) != 0 {
			t.Fatalf("temporary files=%v err=%v", matches, err)
		}
	})

	t.Run("content length preflight", func(t *testing.T) {
		target := filepath.Join(root, "known-oversized.bin")
		response := &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: 10,
			Body: io.NopCloser(strings.NewReader("must-not-be-read")),
		}
		if _, err := readRESTResponseWithFile(response, 8, target, 3); err == nil || !strings.Contains(err.Error(), "download exceeds") {
			t.Fatalf("error=%v", err)
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("preflight target exists: %v", err)
		}
	})

	t.Run("missing parent", func(t *testing.T) {
		target := filepath.Join(root, "missing", "object.bin")
		response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data"))}
		if _, err := readRESTResponseWithFile(response, 8, target, 1024); err == nil || !strings.Contains(err.Error(), "temporary file") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("interrupted stream", func(t *testing.T) {
		target := filepath.Join(root, "interrupted.bin")
		response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: failingResponseBody{}}
		if _, err := readRESTResponseWithFile(response, 8, target, 1024); err == nil || !strings.Contains(err.Error(), "stream interrupted") {
			t.Fatalf("error=%v", err)
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("interrupted target exists: %v", err)
		}
	})

	t.Run("provider error", func(t *testing.T) {
		target := filepath.Join(root, "error.bin")
		response := &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"SecretAccessKey":"hidden"}`))}
		if _, err := readRESTResponseWithFile(response, 1024, target, 1024); err == nil || strings.Contains(err.Error(), "hidden") {
			t.Fatalf("error=%v", err)
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("error target exists: %v", err)
		}
	})
}

func TestResponseFilePolicyRequiresNewTargetUnderApprovedRoot(t *testing.T) {
	root := t.TempDir()
	base := Invocation{Provider: ProviderGCP, Method: "GET", URL: "https://storage.googleapis.com/storage/v1/b/b/o/o?alt=media"}
	base.ResponseFile = filepath.Join(root, "object.bin")
	if err := validateInvocation(base, []string{root}); err != nil {
		t.Fatalf("new response_file rejected: %v", err)
	}
	if err := os.WriteFile(base.ResponseFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateInvocation(base, []string{root}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing response_file error=%v", err)
	}
	base.ResponseFile = filepath.Join(filepath.Dir(root), "outside.bin")
	if err := validateInvocation(base, []string{root}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside response_file error=%v", err)
	}
	base.ResponseFile = filepath.Join(root, "missing", "object.bin")
	if err := validateInvocation(base, []string{root}); err == nil || !strings.Contains(err.Error(), "parent") {
		t.Fatalf("missing parent response_file error=%v", err)
	}
}

func TestResponseFilePolicyRejectsBeforeProviderInvocation(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.bin")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeAdapter{invokeOut: []byte(`{"ok":true}`)}
	adapters := allFakeAdapters()
	adapters[ProviderGCP] = fake
	client := newTestClient(t, Runtime{Adapters: adapters, AllowedFileRoots: []string{root}})
	result := callCloudTool(t, client, "gcp_api_read", map[string]any{
		"method": "GET", "url": "https://storage.googleapis.com/storage/v1/b/b/o/o?alt=media", "response_file": target,
	})
	if !result.IsError || !strings.Contains(cloudToolText(t, result), "already exists") {
		t.Fatalf("result=%#v", result)
	}
	if len(fake.invocations) != 0 {
		t.Fatalf("provider invoked despite response_file rejection: %#v", fake.invocations)
	}
}
