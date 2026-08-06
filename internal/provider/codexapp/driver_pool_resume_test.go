package codexapp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestPoolRoutingExplicitlyDisabledFailsClosedWithCodexIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "0")
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		return newFakeServer("ws://should-not-be-called"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{
		AgentID: "a",
		Config:  identityConfig(t, "glm"),
	})
	if err == nil || !strings.Contains(err.Error(), "pool-backed app-server") {
		t.Fatalf("err = %v, want pool-backed app-server requirement", err)
	}
	if opts != nil {
		t.Fatalf("want nil opts when app-managed codex identity cannot use pool, got %d", len(opts))
	}
}

func TestPoolRoutingAgentIDBlankFailsClosed(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	spawnCalls := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://127.0.0.1:1234"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	cfg := map[string]any{
		"codexHome":          smokeHome(t),
		"codexInstanceKey":   "k",
		"codexModelProvider": "mp",
	}
	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "", Config: cfg})
	if err == nil || !strings.Contains(err.Error(), "pool owner agentID is empty") {
		t.Fatalf("err = %v, want empty owner failure", err)
	}
	if opts != nil {
		t.Fatalf("empty agent id must not return options, got %d", len(opts))
	}
	if spawnCalls.Load() != 0 {
		t.Fatalf("spawner should not fire on empty agent id, called %d times", spawnCalls.Load())
	}
}

func TestResumeRoutingExplicitlyDisabledFailsClosedWithCodexIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "0")
	spawnCalls := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://should-not-be-called"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveResumeOptions(context.Background(), dto.ResumeSessionRequest{
		AgentID:            "agent-resume",
		CodexHome:          smokeHome(t),
		CodexInstanceKey:   "glm",
		CodexModelProvider: "openai-compatible-glm",
	})
	if err == nil || !strings.Contains(err.Error(), "pool-backed app-server") {
		t.Fatalf("resolveResumeOptions() err = %v, want pool-backed app-server requirement", err)
	}
	if opts != nil {
		t.Fatalf("want nil opts when resume identity cannot use pool, got %d", len(opts))
	}
	if spawnCalls.Load() != 0 {
		t.Fatalf("spawner should not fire when resume identity cannot use pool, called %d times", spawnCalls.Load())
	}
}

func TestPrepareResumeSessionRequestDefaultsMissingLegacyIdentity(t *testing.T) {
	userHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(userHome, ".codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll default codex home: %v", err)
	}
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv(providershared.SuperDolphinHomeEnv, filepath.Join(t.TempDir(), "sd-home"))
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	workDir := t.TempDir()
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{logRuntime: testLoggerRuntime(t), logger: slog.Default(), mirror: mirror}

	got, err := d.prepareResumeSessionRequest(context.Background(), dto.ResumeSessionRequest{
		Provider: "codex",
		AgentID:  "agent-resume",
		ThreadID: "thread-1",
		CWD:      workDir,
		Config: map[string]any{
			"provider": "codex",
			"cwd":      workDir,
		},
	})
	if err != nil {
		t.Fatalf("prepareResumeSessionRequest() error = %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(filepath.Join(userHome, ".codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks default codex home: %v", err)
	}
	assertDefaultResumeIdentity(t, got, wantHome)
	assertDefaultResumeConfigIdentity(t, got.Config, wantHome)
	if mirror.calls != 1 {
		t.Fatalf("mirror reconcile calls = %d, want 1 after default identity resolution", mirror.calls)
	}
}

func assertDefaultResumeIdentity(t *testing.T, got dto.ResumeSessionRequest, wantHome string) {
	t.Helper()
	if got.CodexHome != wantHome ||
		got.CodexInstanceKey != defaultCodexInstanceKey ||
		got.CodexModelProvider != localCodexModelProvider {
		t.Fatalf("resume identity = (%q,%q,%q), want default local CLI identity (%q,%q,%q)",
			got.CodexHome,
			got.CodexInstanceKey,
			got.CodexModelProvider,
			wantHome,
			defaultCodexInstanceKey,
			localCodexModelProvider)
	}
}

func assertDefaultResumeConfigIdentity(t *testing.T, config map[string]any, wantHome string) {
	t.Helper()
	if config["codexHome"] != wantHome ||
		config["codexInstanceKey"] != defaultCodexInstanceKey ||
		config["codexModelProvider"] != localCodexModelProvider {
		t.Fatalf("resume config identity = %#v, want canonical identity in config", config)
	}
}

func TestResumeSessionUsesPoolWhenBindingHasIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	spawnCalls := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://127.0.0.1:4321"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveResumeOptions(context.Background(), dto.ResumeSessionRequest{
		AgentID:            "agent-resume",
		CodexHome:          smokeHome(t),
		CodexInstanceKey:   "glm",
		CodexModelProvider: "openai-compatible-glm",
	})
	if err != nil {
		t.Fatalf("resolveResumeOptions() error = %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("resume options = %d, want 1", len(opts))
	}
	var so sessionOptions
	opts[0](&so)
	if so.poolURL != "ws://127.0.0.1:4321" {
		t.Fatalf("poolURL = %q", so.poolURL)
	}
	so.poolRelease()
	if spawnCalls.Load() != 1 {
		t.Fatalf("spawn calls = %d, want 1", spawnCalls.Load())
	}
}

func TestResumeSessionFailsClosedWhenPoolEnabledAndIdentityMissing(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	spawnCalls := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://unused"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveResumeOptions(context.Background(), dto.ResumeSessionRequest{AgentID: "agent-resume"})
	if err == nil || !strings.Contains(err.Error(), "codex identity required for resume") {
		t.Fatalf("resolveResumeOptions() err = %v, want identity-required failure", err)
	}
	if opts != nil {
		t.Fatalf("missing resume identity must not return options, got %d", len(opts))
	}
	if spawnCalls.Load() != 0 {
		t.Fatalf("spawner should not fire on missing resume identity, called %d times", spawnCalls.Load())
	}
}

func smokeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Clean(dir)); err != nil {
		t.Fatalf("tempdir missing: %v", err)
	}
	return dir
}
