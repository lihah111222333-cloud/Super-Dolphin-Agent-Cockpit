package standalone

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProbeConfiguredProvidersCodexHandshake(t *testing.T) {
	requireUnixProbe(t)
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := writeProbeFixture(t, "#!/bin/sh\nif [ \"$1\" != \"app-server\" ] || [ \"$2\" != \"--listen\" ]; then exit 21; fi\nwhile IFS= read -r line; do\n  case \"$line\" in *initialize*) printf '%s\\n' '{\"id\":1,\"result\":{\"userAgent\":\"fixture\"}}';; esac\ndone\n")
	cfg := probeConfig(workspace, Provider{Name: "codex", Executable: executable, Home: dir, Models: []string{"fixture"}, Sandbox: "read-only"})
	got, err := ProbeConfiguredProviders(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ProbeConfiguredProviders() error = %v", err)
	}
	if len(got) != 1 || got[0].Protocol != "codex-app-server-jsonl" {
		t.Fatalf("probe result = %#v", got)
	}
}

func TestProbeConfiguredProvidersRejectsNilContext(t *testing.T) {
	var nilContext context.Context
	if _, err := ProbeConfiguredProviders(nilContext, Config{}); !errors.Is(err, ErrExecutionContextRequired) {
		t.Fatalf("nil context error = %v, want ErrExecutionContextRequired", err)
	}
	typedNil := context.Context((*nilExecutionContext)(nil))
	if _, err := ProbeProviders(typedNil, Config{}); !errors.Is(err, ErrExecutionContextRequired) {
		t.Fatalf("typed-nil context error = %v, want ErrExecutionContextRequired", err)
	}
}

func TestProbeConfiguredProvidersClaudeStreamJSON(t *testing.T) {
	requireUnixProbe(t)
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := writeClaudeControlFixture(t)
	cfg := probeConfig(workspace, Provider{Name: "claude", Executable: executable, Home: dir, Models: []string{"fixture"}})
	got, err := ProbeProviders(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ProbeProviders() error = %v", err)
	}
	if len(got) != 1 || got[0].Protocol != "claude-stream-json" {
		t.Fatalf("probe result = %#v", got)
	}
}

func TestProbeConfiguredProvidersClaudeRejectsResultFrame(t *testing.T) {
	requireUnixProbe(t)
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := writeProbeFixture(t, "#!/bin/sh\nIFS= read -r line || exit 22\nprintf '%s\\n' '{\"type\":\"result\",\"session_id\":\"orch-probe\",\"subtype\":\"error\"}'\n")
	cfg := probeConfig(workspace, Provider{Name: "claude", Executable: executable, Home: dir, Models: []string{"fixture"}})
	if _, err := ProbeConfiguredProviders(context.Background(), cfg); !errors.Is(err, ErrProviderProbe) {
		t.Fatalf("result frame accepted: %v", err)
	}
}

func TestClaudeProbeCleanupPreservesPipeFailure(t *testing.T) {
	want := errors.New("fixture pipe close failed")
	process := &claudeProbeProcess{
		input:  failingProbeWriteCloser{err: want},
		output: failingProbeReadCloser{err: want},
	}
	if err := process.cleanup(); !errors.Is(err, want) {
		t.Fatalf("cleanup error = %v, want pipe failure", err)
	}
}

type failingProbeWriteCloser struct{ err error }

func (f failingProbeWriteCloser) Write([]byte) (int, error) { return 0, f.err }
func (f failingProbeWriteCloser) Close() error              { return f.err }

type failingProbeReadCloser struct{ err error }

func (f failingProbeReadCloser) Read([]byte) (int, error) { return 0, f.err }
func (f failingProbeReadCloser) Close() error             { return f.err }

func TestProbeConfiguredProvidersRejectsWrongProtocolAndNonExecutable(t *testing.T) {
	requireUnixProbe(t)
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	wrong := writeProbeFixture(t, "#!/bin/sh\nprintf 'not-json\\n'\n")
	cfg := probeConfig(workspace, Provider{Name: "codex", Executable: wrong, Home: dir, Models: []string{"fixture"}, Sandbox: "read-only"})
	if _, err := ProbeConfiguredProviders(context.Background(), cfg); !errors.Is(err, ErrProviderProbe) {
		t.Fatalf("wrong protocol error = %v, want ErrProviderProbe", err)
	}
	if err := os.Chmod(wrong, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeConfiguredProviders(context.Background(), cfg); err == nil {
		t.Fatal("non-executable provider accepted")
	}
}

func probeConfig(workspace string, provider Provider) Config {
	return Config{SchemaVersion: 1, WorkspaceRoot: workspace, StateDir: workspace,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(workspace, "credential"),
		PrincipalRef: "operator/probe", ScopeRef: "workspace/probe", Providers: []Provider{provider}}
}

func writeProbeFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-fixture.sh")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireUnixProbe(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell protocol fixture requires Unix")
	}
}

// TestClaudeProbeExpectedShutdownExitContract 使用真实 ExitError 验证主动关停与异常退出的边界。
func TestClaudeProbeExpectedShutdownExitContract(t *testing.T) {
	requireUnixProbe(t)
	cases := []struct {
		name, command    string
		term, kill, want bool
	}{
		{"active-term-mapped", "exit 143", true, false, true},
		{"unrequested-term-mapped", "exit 143", false, false, false},
		{"kill-does-not-authorize-term", "exit 143", false, true, false},
		{"ordinary-one", "exit 1", true, true, false},
		{"ordinary-seven", "exit 7", true, true, false},
		{"active-term-signal", "kill -15 $$", true, false, true},
		{"unrequested-term-signal", "kill -15 $$", false, false, false},
		{"active-kill-signal", "kill -9 $$", true, true, true},
		{"term-does-not-authorize-kill", "kill -9 $$", true, false, false},
		{"segfault-not-cleanup", "ulimit -c 0 && kill -11 $$", true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := exec.CommandContext(ctx, "/bin/sh", "-c", tc.command).Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("fixture must produce actual ExitError: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("exit fixture timed out")
			}
			if got := expectedClaudeProbeExit(err, tc.term, tc.kill); got != tc.want {
				t.Fatalf("expected exit=%v, want %v", got, tc.want)
			}
		})
	}
	t.Run("pipe-failure-retained", TestClaudeProbeCleanupPreservesPipeFailure)
}
