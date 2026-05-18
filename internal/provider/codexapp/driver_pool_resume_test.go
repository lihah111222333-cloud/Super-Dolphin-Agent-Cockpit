package codexapp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

func TestPoolRoutingExplicitlyDisabledFailsClosedWithCodexIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "0")
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
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
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
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
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
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

func TestResumeSessionFailsClosedBeforePreparingDefaultIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	spawnCalls := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://should-not-be-called"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{logger: slog.Default(), pool: pool, mirror: mirror}

	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider: "codex",
		AgentID:  "agent-resume",
		ThreadID: "thread-1",
		CWD:      workDir,
	})
	if err == nil || !strings.Contains(err.Error(), "codex identity required for resume") {
		t.Fatalf("ResumeSession() err = %v, want identity-required failure", err)
	}
	if resumed != nil {
		t.Fatalf("ResumeSession() session = %#v, want nil on missing identity", resumed)
	}
	if mirror.calls != 0 {
		t.Fatalf("mirror reconcile calls = %d, want 0", mirror.calls)
	}
	if spawnCalls.Load() != 0 {
		t.Fatalf("spawner should not fire on missing resume identity, called %d times", spawnCalls.Load())
	}
}

func TestResumeSessionUsesPoolWhenBindingHasIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	spawnCalls := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
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
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
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
