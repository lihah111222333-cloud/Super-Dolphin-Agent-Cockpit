package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func newRoutingDriver(t *testing.T, pool *ServerPool) *driver {
	t.Helper()
	return &driver{
		logger: slog.Default(),
		pool:   pool,
	}
}

// identityConfig returns a req.Config map that ResolveCodexIdentity
// will accept. dir must be an existing realpath; t.TempDir satisfies
// that on all supported platforms.
func identityConfig(t *testing.T, key string) map[string]any {
	t.Helper()
	dir := t.TempDir()
	return map[string]any{
		"codexHome":          dir,
		"codexInstanceKey":   key,
		"codexModelProvider": "mp-" + key,
	}
}

// TestPoolRoutingEnabledByDefault verifies the clean lifecycle path:
// without CODEXAPP_USE_POOL set, valid identity uses the ServerPool so
// session shutdown owns app-server/MCP/LSP process cleanup.
func TestPoolRoutingEnabledByDefault(t *testing.T) {
	// t.Setenv is inherited by parallel siblings, so keep the test
	// serial to avoid env bleed.
	t.Setenv(poolRoutingEnvVar, "")
	spawnCalls := atomic.Int32{}
	spawner := func(context.Context, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://127.0.0.1:9999"), nil
	}
	pool := NewServerPool(slog.Default(), spawner, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	cfg := identityConfig(t, "glm")
	req := dto.StartSessionRequest{AgentID: "a", Config: cfg}
	opts, err := d.resolveSessionOptions(context.Background(), req)
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

func TestPoolRoutingExplicitlyDisabledUsesLegacyPath(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "0")
	spawner := func(context.Context, string) (SpawnedServer, error) {
		return newFakeServer("ws://should-not-be-called"), nil
	}
	pool := NewServerPool(slog.Default(), spawner, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{
		AgentID: "a",
		Config:  identityConfig(t, "glm"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if opts != nil {
		t.Fatalf("want nil opts with feature flag disabled, got %d", len(opts))
	}
}

// TestResolveSessionOptionsFailsClosedOnIdentityError checks the fail-closed
// contract: by default, a request missing codexHome must return an identity
// error instead of falling back to the legacy server.
func TestResolveSessionOptionsFailsClosedOnIdentityError(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	spawnCalls := atomic.Int32{}
	spawner := func(context.Context, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://unused"), nil
	}
	pool := NewServerPool(slog.Default(), spawner, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	req := dto.StartSessionRequest{AgentID: "a"} // no Config
	opts, err := d.resolveSessionOptions(context.Background(), req)
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

// TestPoolRoutingSuccessPath exercises the happy path: flag on +
// valid identity + cooperative pool. The returned option slice must
// attach the pool-provided URL to the next session.
func TestPoolRoutingSuccessPath(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	fake := newFakeServer("ws://127.0.0.1:7777")
	spawner := func(context.Context, string) (SpawnedServer, error) {
		return fake, nil
	}
	pool := NewServerPool(slog.Default(), spawner, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	cfg := identityConfig(t, "qwen")
	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "a", Config: cfg})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("want 1 option, got %d", len(opts))
	}
	// Apply the option onto a fresh sessionOptions so we can observe
	// the wiring without booting a real session.
	var so sessionOptions
	opts[0](&so)
	if so.poolURL != "ws://127.0.0.1:7777" {
		t.Fatalf("poolURL = %q, want ws://127.0.0.1:7777", so.poolURL)
	}
	if so.poolRelease == nil {
		t.Fatal("poolRelease should be set")
	}
	// Releasing must land on the pool so refCount drops back to 0.
	so.poolRelease()
	if pool.Size() != 0 {
		t.Fatalf("pool size = %d after release, want 0 entries retained", pool.Size())
	}
}

func TestPoolRoutingPassesStartCWDToSpawnerWorkDir(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	workDir := t.TempDir()
	var got string
	pool := NewServerPool(slog.Default(), func(ctx context.Context, home string) (SpawnedServer, error) {
		got = poolSpawnWorkDir(ctx)
		return newFakeServer("ws://127.0.0.1:7788"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	cfg := identityConfig(t, "cwd")
	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "agent-cwd", CWD: workDir, Config: cfg})
	if err != nil {
		t.Fatalf("resolveSessionOptions() error = %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("options = %d, want 1", len(opts))
	}
	if got != workDir {
		t.Fatalf("spawn WorkDir = %q, want %q", got, workDir)
	}
	var so sessionOptions
	opts[0](&so)
	so.poolRelease()
}

// TestPoolRoutingSurfacesPoolError asserts pool acquire errors are
// not swallowed: retry / observability must see them.
func TestPoolRoutingSurfacesPoolError(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	spawner := func(context.Context, string) (SpawnedServer, error) {
		return nil, errors.New("spawn blew up")
	}
	pool := NewServerPool(slog.Default(), spawner, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	cfg := identityConfig(t, "deepseek")
	_, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "a", Config: cfg})
	if err == nil {
		t.Fatal("want pool acquire error to surface")
	}
}

// TestPoolRoutingInvalidConfigTypeFailsClosed guards the specific failure
// mode where req.Config carries a codexHome with the wrong dynamic
// type. With pool routing enabled, ResolveCodexIdentity errors must surface.
func TestPoolRoutingInvalidConfigTypeFailsClosed(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		return newFakeServer("ws://unused"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	cfg := map[string]any{"codexHome": 123, "codexInstanceKey": "glm", "codexModelProvider": "mp"}
	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "a", Config: cfg})
	if err == nil || !strings.Contains(err.Error(), "codex identity required") {
		t.Fatalf("err = %v, want codex identity required", err)
	}
	if opts != nil {
		t.Fatalf("invalid config must fail before options, got %d opts", len(opts))
	}
}

// TestPoolRoutingFlagFalsyStaysDisabled locks the parse contract:
// typos / non-bool values must all leave the routing off so a
// malformed env var cannot silently flip production onto the new
// path.
func TestPoolRoutingFlagFalsyStaysDisabled(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "garbage"} {
		t.Setenv(poolRoutingEnvVar, v)
		if poolRoutingEnabled() {
			t.Fatalf("flag %q must parse as disabled", v)
		}
	}
}

func TestPoolRoutingFlagMissingEnablesStrictRouting(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	enabled, strict := poolRoutingDecision()
	if !enabled || !strict {
		t.Fatalf("missing pool flag = enabled %v strict %v, want true/true", enabled, strict)
	}
}

// TestSessionShutdownReleasesPoolOnce verifies the session-side
// half: shutdownSessionCleanup must invoke poolRelease exactly once
// even if Close + ForceStop race.
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

// TestSessionShutdownNoReleaseIsSafe ensures sessions without a
// pool attachment (legacy ServerManager path) tolerate
// shutdownSessionCleanup as a no-op.
func TestSessionShutdownNoReleaseIsSafe(t *testing.T) {
	s := &session{}
	// Must not panic.
	s.shutdownSessionCleanup()
}

// smokeHome gives a realpath we can thread into ResolveCodexIdentity.
func smokeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Clean(dir)); err != nil {
		t.Fatalf("tempdir missing: %v", err)
	}
	return dir
}

// TestPoolRoutingAgentIDBlankFailsClosed guards the session lifecycle key:
// pool-backed Codex sessions must have an owning agent id so archive/stop can
// reclaim the exact app-server process group and sidecars.
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

// ensure json types round-trip for the tests above; keeps the import
// alive even if no explicit use remains after edits.
var _ = json.RawMessage{}

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
