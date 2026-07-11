package codexapp

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestPoolRoutingEnabledByDefault(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	spawnCalls := atomic.Int32{}
	spawner := func(context.Context, string, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://127.0.0.1:9999"), nil
	}
	pool := NewServerPool(slog.Default(), spawner, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "a", Config: identityConfig(t, "glm")})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("want 1 pool option with default routing, got %d", len(opts))
	}
	var so sessionOptions
	opts[0](&so)
	if so.poolURL != "ws://127.0.0.1:9999" {
		t.Fatalf("poolURL = %q, want ws://127.0.0.1:9999", so.poolURL)
	}
	so.poolRelease()
	if spawnCalls.Load() != 1 {
		t.Fatalf("spawn calls = %d, want 1", spawnCalls.Load())
	}
}

func TestResolveSessionOptionsDefaultRoutingFailsClosedOnMissingIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	spawnCalls := atomic.Int32{}
	spawner := func(context.Context, string, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://unused"), nil
	}
	pool := NewServerPool(slog.Default(), spawner, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "a"})
	if err == nil || !strings.Contains(err.Error(), "codex identity required") {
		t.Fatalf("err = %v, want codex identity required", err)
	}
	if opts != nil {
		t.Fatalf("want nil opts on identity error, got %d", len(opts))
	}
	if spawnCalls.Load() != 0 {
		t.Fatalf("spawner should not fire on missing identity, called %d times", spawnCalls.Load())
	}
}

func TestPoolRoutingSuccessPath(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	spawner := func(context.Context, string, string) (SpawnedServer, error) {
		return newFakeServer("ws://127.0.0.1:7777"), nil
	}
	pool := NewServerPool(slog.Default(), spawner, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "a", Config: identityConfig(t, "qwen")})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("want 1 option, got %d", len(opts))
	}
	var so sessionOptions
	opts[0](&so)
	if so.poolURL != "ws://127.0.0.1:7777" {
		t.Fatalf("poolURL = %q, want ws://127.0.0.1:7777", so.poolURL)
	}
	if so.poolRelease == nil {
		t.Fatal("poolRelease should be set")
	}
	so.poolRelease()
	if pool.Size() != 0 {
		t.Fatalf("pool size = %d after release, want 0 entries retained", pool.Size())
	}
}
