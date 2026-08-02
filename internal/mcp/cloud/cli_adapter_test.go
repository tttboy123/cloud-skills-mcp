package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type processCall struct {
	binary string
	args   []string
	env    []string
}

type fakeProcessRunner struct {
	calls  []processCall
	stdout []byte
	stderr []byte
	err    error
	check  func(processCall) error
}

func (runner *fakeProcessRunner) Run(_ context.Context, binary string, args, env []string) ([]byte, []byte, error) {
	call := processCall{binary: binary, args: append([]string(nil), args...), env: append([]string(nil), env...)}
	runner.calls = append(runner.calls, call)
	if runner.check != nil {
		if err := runner.check(call); err != nil {
			return nil, nil, err
		}
	}
	return runner.stdout, runner.stderr, runner.err
}

func TestUniversalCLIAdaptersBuildOfficialCommands(t *testing.T) {
	tests := []struct {
		provider Provider
		binary   string
		request  Invocation
		want     []string
	}{
		{
			provider: ProviderAWS,
			binary:   "aws-test",
			request:  Invocation{Provider: ProviderAWS, Service: "ec2", Operation: "describe-instances", Region: "us-east-1", Parameters: map[string]any{"MaxResults": 5}},
			want:     []string{"ec2", "describe-instances", "--region", "us-east-1", "--cli-input-json", "file://", "--output", "json", "--no-cli-pager"},
		},
		{
			provider: ProviderTencent,
			binary:   "tccli-test",
			request:  Invocation{Provider: ProviderTencent, Service: "vpc", Operation: "DescribeVpcs", Region: "ap-shanghai", Parameters: map[string]any{"Limit": 10}},
			want:     []string{"vpc", "DescribeVpcs", "--region", "ap-shanghai", "--cli-input-json", "file://", "--output", "json"},
		},
		{
			provider: ProviderAlicloud,
			binary:   "aliyun-test",
			request:  Invocation{Provider: ProviderAlicloud, Service: "ecs", Operation: "DescribeInstances", Region: "cn-hangzhou", Parameters: map[string]any{"PageSize": 25, "Tags": []string{"prod"}}},
			want:     []string{"ecs", "DescribeInstances", "--region", "cn-hangzhou", "--PageSize=25", `--Tags=["prod"]`},
		},
	}
	for _, test := range tests {
		t.Run(string(test.provider), func(t *testing.T) {
			runner := &fakeProcessRunner{stdout: []byte(`{"ok":true}`)}
			runner.check = func(call processCall) error {
				for _, argument := range call.args {
					if strings.HasPrefix(argument, "file://") {
						data, err := os.ReadFile(strings.TrimPrefix(argument, "file://"))
						if err != nil {
							return err
						}
						if !json.Valid(data) {
							return errors.New("payload is not JSON")
						}
					}
				}
				return nil
			}
			adapter := NewCLIAdapter(CLIAdapterConfig{
				Provider: test.provider, Binary: test.binary, Runner: runner, TempDir: t.TempDir(),
			})
			result, err := adapter.Invoke(t.Context(), test.request)
			if err != nil || string(result.Output) != `{"ok":true}` {
				t.Fatalf("invoke result=%#v err=%v", result, err)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("calls=%d", len(runner.calls))
			}
			got := normalizePayloadPath(runner.calls[0].args)
			if strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
				t.Fatalf("args:\nwant %#v\n got %#v", test.want, got)
			}
			entries, err := os.ReadDir(adapter.config.TempDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("temporary payload leaked: %v", entries)
			}
		})
	}
}

func normalizePayloadPath(arguments []string) []string {
	result := append([]string(nil), arguments...)
	for index, argument := range result {
		if strings.HasPrefix(argument, "file://") {
			result[index] = "file://"
		}
	}
	return result
}

func TestCLIAdapterStatusAndDiscoveryAreCredentialSafe(t *testing.T) {
	runner := &fakeProcessRunner{stdout: []byte("aws-cli/2.test\n")}
	adapter := NewCLIAdapter(CLIAdapterConfig{Provider: ProviderAWS, Binary: "aws-test", Runner: runner})
	status, err := adapter.Status(t.Context())
	if err != nil || !status.Available || status.Version != "aws-cli/2.test" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if _, err := adapter.Discover(t.Context(), DiscoveryRequest{Provider: ProviderAWS, Service: "ec2", Operation: "describe-instances"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls=%#v", runner.calls)
	}
	if got := strings.Join(runner.calls[0].args, " "); got != "--version" {
		t.Fatalf("status args=%q", got)
	}
	if got := strings.Join(runner.calls[1].args, " "); got != "ec2 describe-instances help" {
		t.Fatalf("discover args=%q", got)
	}
	for _, call := range runner.calls {
		joined := strings.ToLower(strings.Join(call.args, " "))
		if strings.Contains(joined, "configure") || strings.Contains(joined, "credential") || strings.Contains(joined, "access-token") {
			t.Fatalf("credential command invoked: %s", joined)
		}
	}
}

func TestAlibabaCredentialStatusRecognizesCurrentOfficialEnvironment(t *testing.T) {
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "secret")
	if got := credentialSource(ProviderAlicloud); got != "environment-aksk" {
		t.Fatalf("credential source=%q", got)
	}
}

func TestAlibabaParametersCannotBecomeNewCLIFlags(t *testing.T) {
	args, err := alicloudParameterArgs(map[string]any{"Name": "--endpoint=https://evil.example"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--Name=--endpoint=https://evil.example"}
	if !slices.Equal(args, want) {
		t.Fatalf("args=%#v", args)
	}
}

func TestAlibabaCLIStatusUsesVersionSubcommand(t *testing.T) {
	runner := &fakeProcessRunner{stdout: []byte("3.4.11\n")}
	adapter := NewCLIAdapter(CLIAdapterConfig{Provider: ProviderAlicloud, Binary: "aliyun-test", Runner: runner})
	status, err := adapter.Status(t.Context())
	if err != nil || status.Version != "3.4.11" || len(runner.calls) != 1 || strings.Join(runner.calls[0].args, " ") != "version" {
		t.Fatalf("status=%#v calls=%#v err=%v", status, runner.calls, err)
	}
}

func TestCLIAdapterReturnsSeparatedProcessFailure(t *testing.T) {
	runner := &fakeProcessRunner{stdout: []byte("stdout"), stderr: []byte("SecretKey=hidden RequestId=req-1"), err: errors.New("exit status 7")}
	adapter := NewCLIAdapter(CLIAdapterConfig{Provider: ProviderTencent, Binary: "tccli-test", Runner: runner})
	_, err := adapter.Invoke(t.Context(), Invocation{Provider: ProviderTencent, Service: "cvm", Operation: "DescribeInstances"})
	if err == nil || !strings.Contains(err.Error(), "RequestId=req-1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFileRootValidationResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(inside, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateArgument("fileb://"+inside, []string{root}); err != nil {
		t.Fatalf("inside path rejected: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := validateArgument("file://"+link, []string{root}); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestExecProcessRunnerBoundsSubprocessStreams(t *testing.T) {
	runner := execProcessRunner{}
	stdout, _, err := runner.Run(t.Context(), "sh", []string{"-c", "yes x | head -c 2200000"}, os.Environ())
	if err == nil || len(stdout) != maxCLIStreamBytes || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("stdout=%d err=%v", len(stdout), err)
	}
}
