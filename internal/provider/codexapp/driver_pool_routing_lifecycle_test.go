package codexapp

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestResolveSessionOptionsFailsFastOnMalformedPoolRoutingFlag(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "garbage")
	spawnCalls := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://unused"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "a", Config: identityConfig(t, "bad-env")})
	if err == nil || !strings.Contains(err.Error(), poolRoutingEnvVar) {
		t.Fatalf("resolveSessionOptions() error = %v, want malformed env error", err)
	}
	if opts != nil {
		t.Fatalf("malformed env must not return options, got %d", len(opts))
	}
	if spawnCalls.Load() != 0 {
		t.Fatalf("spawner should not fire on malformed env, called %d times", spawnCalls.Load())
	}
}

func TestSessionShutdownReleasesPoolOnce(t *testing.T) {
	released := atomic.Int32{}
	s := &session{poolRelease: func() { released.Add(1) }}
	s.shutdownSessionCleanup()
	s.shutdownSessionCleanup()
	s.shutdownSessionCleanup()
	if got := released.Load(); got != 1 {
		t.Fatalf("poolRelease called %d times, want 1", got)
	}
}

func TestSessionShutdownNoReleaseIsSafe(t *testing.T) {
	s := &session{}
	s.shutdownSessionCleanup()
}

func TestResumeSessionPoolSpawnUsesRuntimeWorkspaceRoots(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	primary := t.TempDir()
	extra := t.TempDir()
	var gotRoots []string
	pool := NewServerPool(slog.Default(), func(ctx context.Context, _, _ string) (SpawnedServer, error) {
		gotRoots = poolSpawnWorkspaceRoots(ctx)
		return newFakeServer("ws://127.0.0.1:4321"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveResumeOptions(context.Background(), dto.ResumeSessionRequest{
		AgentID:            "agent-resume",
		CWD:                primary,
		CodexHome:          smokeHome(t),
		CodexInstanceKey:   "glm",
		CodexModelProvider: "openai-compatible-glm",
		Config:             map[string]any{"additionalWorkingDirectories": []string{extra}},
	})
	if err != nil {
		t.Fatalf("resolveResumeOptions() error = %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("resume options = %d, want 1", len(opts))
	}
	var so sessionOptions
	opts[0](&so)
	so.poolRelease()
	if wantRoots := []string{primary, extra}; !reflect.DeepEqual(gotRoots, wantRoots) {
		t.Fatalf("pool spawn workspace roots = %#v, want %#v", gotRoots, wantRoots)
	}
}
