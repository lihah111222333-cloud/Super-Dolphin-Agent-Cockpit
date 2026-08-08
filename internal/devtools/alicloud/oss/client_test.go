package oss

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type runCall struct {
	name string
	args []string
}

type fakeRunner struct {
	stdout         []byte
	stderr         []byte
	err            error
	calls          []runCall
	errors         []error
	mutateArgs     bool
	checkpointDirs []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	recordedArgs, checkpointDirectory := normalizeCheckpointArgument(args)
	if checkpointDirectory != "" {
		r.checkpointDirs = append(r.checkpointDirs, checkpointDirectory)
	}
	r.calls = append(r.calls, runCall{name: name, args: recordedArgs})
	if r.mutateArgs && len(args) > 0 {
		args[0] = "mutated-by-runner"
	}
	if len(r.errors) > 0 {
		err := r.errors[0]
		r.errors = r.errors[1:]
		if err != nil {
			return r.stdout, r.stderr, err
		}
	}
	return r.stdout, r.stderr, r.err
}

func TestClient_TransportCreateDownloadDelete(t *testing.T) {
	runner := &fakeRunner{}
	client := newTestClient(t, runner)
	ctx := context.Background()

	if err := client.Create(ctx, "/tmp/input.tar", "source-bundles/input.tar"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := client.Download(ctx, "source-bundles/input.tar", "/tmp/output.tar"); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if err := client.Delete(ctx, "source-bundles/input.tar"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := client.DeletePrefix(ctx, "source-bundles/generation-1/"); err != nil {
		t.Fatalf("DeletePrefix() error = %v", err)
	}

	want := []runCall{
		{name: "aliyun", args: []string{"oss", "cp", "/tmp/input.tar", "oss://ci-bucket/source-bundles/input.tar", "--meta", "x-oss-forbid-overwrite:true", "--checkpoint-dir", "<checkpoint-dir>", "--profile", "ci", "--endpoint", "https://oss-cn-hangzhou.aliyuncs.com"}},
		{name: "aliyun", args: []string{"oss", "cp", "oss://ci-bucket/source-bundles/input.tar", "/tmp/output.tar", "--checkpoint-dir", "<checkpoint-dir>", "--profile", "ci", "--endpoint", "https://oss-cn-hangzhou.aliyuncs.com"}},
		{name: "aliyun", args: []string{"oss", "rm", "oss://ci-bucket/source-bundles/input.tar", "--profile", "ci", "--endpoint", "https://oss-cn-hangzhou.aliyuncs.com"}},
		{name: "aliyun", args: []string{"oss", "rm", "oss://ci-bucket/source-bundles/generation-1/", "--recursive", "--force", "--profile", "ci", "--endpoint", "https://oss-cn-hangzhou.aliyuncs.com"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	assertCheckpointDirectoriesClean(t, runner.checkpointDirs)
}

func normalizeCheckpointArgument(args []string) ([]string, string) {
	recordedArgs := append([]string(nil), args...)
	directory := checkpointDirectoryArgument(recordedArgs)
	for index, argument := range recordedArgs {
		if argument == "--checkpoint-dir" && index+1 < len(recordedArgs) {
			recordedArgs[index+1] = "<checkpoint-dir>"
		}
	}
	return recordedArgs, directory
}

func assertCheckpointDirectoriesClean(t *testing.T, directories []string) {
	t.Helper()
	for _, directory := range directories {
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("checkpoint directory %q was not cleaned: %v", directory, err)
		}
	}
}

func checkpointDirectoryArgument(args []string) string {
	for index, argument := range args {
		if argument == "--checkpoint-dir" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func TestClient_RejectsEscapingOrOutOfPrefixKeys(t *testing.T) {
	client := newTestClient(t, &fakeRunner{})
	for _, key := range []string{"../source-bundles/input.tar", "source-bundles/../results/output.tar", "results/output.tar", "/source-bundles/input.tar"} {
		t.Run(key, func(t *testing.T) {
			if err := client.Create(context.Background(), "/tmp/input.tar", key); err == nil {
				t.Fatalf("Create(%q) error = nil", key)
			}
		})
	}
	for _, prefix := range []string{
		"source-bundles/",
		"source-bundles/generation-1",
		"source-bundles/../results/",
		"results/generation-1/",
	} {
		t.Run("prefix "+prefix, func(t *testing.T) {
			if err := client.DeletePrefix(context.Background(), prefix); err == nil {
				t.Fatalf("DeletePrefix(%q) error = nil", prefix)
			}
		})
	}
}

func TestClient_CreateFailsFastOnObjectCollision(t *testing.T) {
	for _, stderr := range []string{"FileAlreadyExists", "HTTP 409 Conflict", "HTTP 412 PreconditionFailed"} {
		t.Run(stderr, func(t *testing.T) {
			runner := &fakeRunner{
				stderr: []byte(stderr),
				errors: []error{errors.New("i/o timeout")},
			}
			client := newTestClient(t, runner)
			client.wait = func(context.Context, time.Duration) error { return nil }

			err := client.Create(context.Background(), "/tmp/input.tar", "source-bundles/input.tar")
			if err == nil {
				t.Fatal("Create() collision error = nil")
			}
			if len(runner.calls) != 1 {
				t.Fatalf("Create() attempts = %d, want 1", len(runner.calls))
			}
			if !strings.Contains(err.Error(), stderr) {
				t.Fatalf("Create() error = %v, want collision detail", err)
			}
		})
	}
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	for _, config := range []Config{
		{Binary: "aliyun", Bucket: "INVALID", Endpoint: "https://oss-cn-hangzhou.aliyuncs.com", Profile: "ci", Prefix: "source-bundles/"},
		{Binary: "aliyun", Bucket: "ci-bucket", Endpoint: "http://oss-cn-hangzhou.aliyuncs.com", Profile: "ci", Prefix: "source-bundles/"},
		{Binary: "aliyun", Bucket: "ci-bucket", Endpoint: "https://oss-cn-hangzhou.aliyuncs.com", Profile: "../../ci", Prefix: "source-bundles/"},
		{Binary: "aliyun", Bucket: "ci-bucket", Endpoint: "https://oss-cn-hangzhou.aliyuncs.com", Profile: "ci", Prefix: "../source-bundles/"},
	} {
		if _, err := New(config, &fakeRunner{}); err == nil {
			t.Fatalf("New(%+v) error = nil", config)
		}
	}
}

func TestClient_CommandFailureIncludesStderr(t *testing.T) {
	runner := &fakeRunner{stderr: []byte("AccessDenied: forbidden\n"), err: errors.New("exit status 1")}
	client := newTestClient(t, runner)
	err := client.Delete(context.Background(), "source-bundles/input.tar")
	if err == nil {
		t.Fatal("Delete() error = nil")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("Delete() error = %T, want *CommandError", err)
	}
	if commandErr.Stderr != "AccessDenied: forbidden" {
		t.Fatalf("CommandError.Stderr = %q", commandErr.Stderr)
	}
}

func TestClient_RetriesTransientFailureWithBoundedBackoffAndStableArgs(t *testing.T) {
	sensitive := "example-access-key-id-not-a-credential"
	runner := &fakeRunner{
		stderr:     []byte("init client failed Post https://sts.example.invalid?AccessKeyId=" + sensitive + ": context deadline exceeded (Client.Timeout exceeded while awaiting headers)"),
		err:        errors.New("exit status 1"),
		mutateArgs: true,
	}
	client := newTestClient(t, runner)
	var waits []time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	err := client.Delete(context.Background(), "source-bundles/input.tar")
	if err == nil || strings.Contains(err.Error(), sensitive) || !strings.Contains(err.Error(), "AccessKeyId=<redacted>") {
		t.Fatalf("redacted Delete() error = %v", err)
	}
	wantWaits := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
	}
	if len(runner.calls) != maxCLIAttempts || !reflect.DeepEqual(waits, wantWaits) {
		t.Fatalf("calls = %d, waits = %v", len(runner.calls), waits)
	}
	for _, call := range runner.calls[1:] {
		if !reflect.DeepEqual(call.args, runner.calls[0].args) {
			t.Fatalf("retry arguments drifted: first = %v, retry = %v", runner.calls[0].args, call.args)
		}
	}
}

func TestClient_PermanentFailuresDoNotRetry(t *testing.T) {
	for _, stderr := range []string{"AccessDenied", "InvalidParameter", "OperationNotSupported"} {
		t.Run(stderr, func(t *testing.T) {
			runner := &fakeRunner{stderr: []byte(stderr), err: errors.New("exit status 1")}
			client := newTestClient(t, runner)
			var waits []time.Duration
			client.wait = func(_ context.Context, delay time.Duration) error {
				waits = append(waits, delay)
				return nil
			}

			if err := client.Delete(context.Background(), "source-bundles/input.tar"); err == nil {
				t.Fatal("Delete() error = nil")
			}
			if len(runner.calls) != 1 || len(waits) != 0 {
				t.Fatalf("calls = %d, waits = %v", len(runner.calls), waits)
			}
		})
	}
}

func TestClient_RetryWaitHonorsCanceledContext(t *testing.T) {
	runner := &fakeRunner{stderr: []byte("tls handshake timeout"), err: errors.New("exit status 1")}
	client := newTestClient(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Delete(ctx, "source-bundles/input.tar")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete() error = %v, want context canceled", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
}

func TestClient_RetriesSTSUserThrottling(t *testing.T) {
	runner := &fakeRunner{
		stderr: []byte(`init client failed refresh session token failed: {"Message":"Request was denied due to user flow control.","Code":"Throttling.User"}`),
		errors: []error{errors.New("exit status 1")},
	}
	client := newTestClient(t, runner)
	var waits []time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	if err := client.Delete(context.Background(), "source-bundles/input.tar"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(runner.calls) != 2 || !reflect.DeepEqual(waits, []time.Duration{initialRetryDelay}) {
		t.Fatalf("calls = %d, waits = %v", len(runner.calls), waits)
	}
}

func newTestClient(t *testing.T, runner CommandRunner) *client {
	t.Helper()
	client, err := New(Config{
		Binary:   "aliyun",
		Bucket:   "ci-bucket",
		Endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
		Profile:  "ci",
		Prefix:   "source-bundles/",
	}, runner)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}
