package codexapp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

func newRoutingDriver(t *testing.T, pool *ServerPool) *driver {
	t.Helper()
	return &driver{
		logger: slog.Default(),
		pool:   pool,
	}
}

func newSingleURLPoolForTest(t *testing.T, url string) *ServerPool {
	t.Helper()
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		return newFakeServer(url), nil
	}, PoolConfig{})
	t.Cleanup(func() { _ = pool.Close(context.Background()) })
	return pool
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

type recordingSkillMirrorReconciler struct {
	events  *[]string
	err     error
	report  contract.SkillMirrorReport
	cwd     string
	targets []contract.SkillProviderMirrorTarget
	calls   int
}

func (r *recordingSkillMirrorReconciler) ReconcileProviderMirrors(ctx context.Context, cwd string, targets []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	if r.events != nil {
		*r.events = append(*r.events, "reconcile")
	}
	r.calls++
	r.cwd = cwd
	r.targets = append([]contract.SkillProviderMirrorTarget(nil), targets...)
	return r.report, r.err
}

func TestStartSessionReconcilesMirrorsBeforePoolAcquireAndDefaultsIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	events := []string{}
	var gotHome string
	pool := NewServerPool(slog.Default(), func(_ context.Context, home string) (SpawnedServer, error) {
		events = append(events, "acquire")
		gotHome = home
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{
		events: &events,
	}
	d := &driver{logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-default",
		CWD:     workDir,
	})
	if err == nil || !strings.Contains(err.Error(), "stop after acquire") {
		t.Fatalf("StartSession() error = %v, want acquire error after reconcile", err)
	}
	if strings.Join(events, ",") != "reconcile,acquire" {
		t.Fatalf("events = %v, want reconcile before acquire", events)
	}
	wantHome, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks provider home: %v", err)
	}
	if gotHome != wantHome {
		t.Fatalf("pool codex home = %q, want %q", gotHome, wantHome)
	}
	assertCodexMirrorTargets(t, mirror.targets, workDir, superHome)
}

func TestStartSessionReconcilesProjectMirrorsFromGitRootBeforePoolAcquire(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	subdir := filepath.Join(repoRoot, "cmd", "agent-terminal")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}
	events := []string{}
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		events = append(events, "acquire")
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{events: &events}
	d := &driver{logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-subdir",
		CWD:     subdir,
	})
	if err == nil || !strings.Contains(err.Error(), "stop after acquire") {
		t.Fatalf("StartSession() error = %v, want acquire error after reconcile", err)
	}
	if strings.Join(events, ",") != "reconcile,acquire" {
		t.Fatalf("events = %v, want reconcile before acquire", events)
	}
	assertCodexMirrorTargets(t, mirror.targets, repoRoot, superHome)
}

func TestStartSessionFailsClosedWhenPreparedIdentityHasNoPool(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{logger: slog.Default(), mirror: mirror}

	session, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-no-pool",
		CWD:     workDir,
	})
	if err == nil || !strings.Contains(err.Error(), "pool-backed app-server") {
		t.Fatalf("StartSession() error = %v, want pool-backed app-server requirement", err)
	}
	if session != nil {
		t.Fatalf("StartSession() session = %#v, want nil", session)
	}
	if mirror.calls != 1 {
		t.Fatalf("mirror reconcile calls = %d, want 1 before routing guard", mirror.calls)
	}
	assertCodexMirrorTargets(t, mirror.targets, workDir, superHome)
}

func TestStartSessionMirrorConflictBlocksPoolAcquire(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	acquires := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		acquires.Add(1)
		return newFakeServer("ws://unused"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{
		logger: slog.Default(),
		pool:   pool,
		mirror: &recordingSkillMirrorReconciler{report: contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
			TargetID:     "codex:project:conflict",
			ConflictKind: "mirror_drift",
		}}}},
	}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{AgentID: "agent-conflict", CWD: workDir})
	if err == nil || !strings.Contains(err.Error(), "skill mirror conflicts") {
		t.Fatalf("StartSession() error = %v, want skill mirror conflicts", err)
	}
	if acquires.Load() != 0 {
		t.Fatalf("pool acquire calls = %d, want 0", acquires.Load())
	}
}

func TestStartSessionRequiresSkillMirrorReconciler(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	t.Setenv(providershared.SuperDolphinHomeEnv, filepath.Join(t.TempDir(), "sd-home"))
	workDir := t.TempDir()
	acquires := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		acquires.Add(1)
		return newFakeServer("ws://unused"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{logger: slog.Default(), pool: pool}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{AgentID: "agent-no-mirror", CWD: workDir})
	if err == nil || !strings.Contains(err.Error(), "skill mirror reconciler") {
		t.Fatalf("StartSession() error = %v, want skill mirror reconciler requirement", err)
	}
	if acquires.Load() != 0 {
		t.Fatalf("pool acquire calls = %d, want 0", acquires.Load())
	}
}

func TestStartSessionMirrorReconcileFailureBlocksPoolAcquire(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	acquires := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		acquires.Add(1)
		return newFakeServer("ws://unused"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{
		logger: slog.Default(),
		pool:   pool,
		mirror: &recordingSkillMirrorReconciler{err: errors.New("mirror unavailable")},
	}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{AgentID: "agent-blocked", CWD: workDir})
	if err == nil || !strings.Contains(err.Error(), "mirror unavailable") {
		t.Fatalf("StartSession() error = %v, want mirror unavailable", err)
	}
	if acquires.Load() != 0 {
		t.Fatalf("pool acquire calls = %d, want 0", acquires.Load())
	}
}

func TestStartSessionReconcilesMirrorsToExplicitCodexHome(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	explicitHome := filepath.Join(t.TempDir(), "explicit-codex")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	var gotHome string
	pool := NewServerPool(slog.Default(), func(_ context.Context, home string) (SpawnedServer, error) {
		gotHome = home
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-explicit",
		CWD:     workDir,
		Config: map[string]any{
			contract.CodexHomeKey:          explicitHome,
			contract.CodexInstanceKeyKey:   "explicit",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stop after acquire") {
		t.Fatalf("StartSession() error = %v, want acquire error after reconcile", err)
	}
	wantHome, err := filepath.EvalSymlinks(explicitHome)
	if err != nil {
		t.Fatalf("EvalSymlinks explicit codex home: %v", err)
	}
	if gotHome != wantHome {
		t.Fatalf("pool codex home = %q, want %q", gotHome, wantHome)
	}
	assertExplicitCodexMirrorTargets(t, mirror.targets, workDir, explicitHome)
}

func TestStartSessionRejectsRelativeExplicitCodexHome(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	workDir := t.TempDir()
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called with relative codex home")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{logger: slog.Default(), pool: pool, mirror: &recordingSkillMirrorReconciler{}}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-relative-home",
		CWD:     workDir,
		Config: map[string]any{
			contract.CodexHomeKey:          "relative-codex-home",
			contract.CodexInstanceKeyKey:   "default",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("StartSession() error = %v, want absolute home rejection", err)
	}
}

func TestStartSessionRejectsMalformedCodexIdentityBeforeHomeOrMirror(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called for malformed codex identity")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-malformed-home",
		CWD:     workDir,
		Config: map[string]any{
			contract.CodexHomeKey:          123,
			contract.CodexInstanceKeyKey:   "default",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must be string") {
		t.Fatalf("StartSession() error = %v, want invalid type rejection", err)
	}
	if mirror.calls != 0 {
		t.Fatalf("mirror reconcile calls = %d, want 0", mirror.calls)
	}
	if _, statErr := os.Stat(filepath.Join(superHome, "providers", "codex")); !os.IsNotExist(statErr) {
		t.Fatalf("provider home stat = %v, want not created", statErr)
	}
}

func TestStartSessionNormalizesExplicitCodexHomeBeforeMirrorAndPool(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	realHome := filepath.Join(t.TempDir(), "real-codex")
	if err := os.MkdirAll(realHome, 0o700); err != nil {
		t.Fatalf("MkdirAll real codex home: %v", err)
	}
	aliasHome := filepath.Join(t.TempDir(), "alias-codex")
	if err := os.Symlink(realHome, aliasHome); err != nil {
		t.Fatalf("Symlink codex home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(realHome)
	if err != nil {
		t.Fatalf("EvalSymlinks real codex home: %v", err)
	}
	var gotHome string
	pool := NewServerPool(slog.Default(), func(_ context.Context, home string) (SpawnedServer, error) {
		gotHome = home
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{logger: slog.Default(), pool: pool, mirror: mirror}

	_, err = d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-explicit-normalized",
		CWD:     workDir,
		Config: map[string]any{
			contract.CodexHomeKey:          aliasHome,
			contract.CodexInstanceKeyKey:   "explicit",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stop after acquire") {
		t.Fatalf("StartSession() error = %v, want acquire error after reconcile", err)
	}
	if gotHome != wantHome {
		t.Fatalf("pool codex home = %q, want %q", gotHome, wantHome)
	}
	assertExplicitCodexMirrorTargets(t, mirror.targets, workDir, wantHome)
}

func assertCodexMirrorTargets(t *testing.T, targets []contract.SkillProviderMirrorTarget, project, superHome string) {
	t.Helper()
	wantPersonalHome, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks personal home: %v", err)
	}
	wantPersonalSkills := filepath.Join(wantPersonalHome, "skills")
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	wantProjectSkills := filepath.Join(wantProject, ".codex", "skills")
	if len(targets) != 2 {
		t.Fatalf("mirror targets = %#v, want personal + project", targets)
	}
	if targets[0].Provider != "codex" || targets[0].HomeRoot != wantPersonalHome || targets[0].SkillsRoot != wantPersonalSkills {
		t.Fatalf("personal target = %#v, want home %q skills %q", targets[0], wantPersonalHome, wantPersonalSkills)
	}
	if targets[1].Provider != "codex" || targets[1].SkillsRoot != wantProjectSkills {
		t.Fatalf("project target = %#v, want skills %q", targets[1], wantProjectSkills)
	}
}

func assertExplicitCodexMirrorTargets(t *testing.T, targets []contract.SkillProviderMirrorTarget, project, explicitHome string) {
	t.Helper()
	if len(targets) != 2 {
		t.Fatalf("mirror targets = %#v, want personal + project", targets)
	}
	wantHome, err := filepath.EvalSymlinks(explicitHome)
	if err != nil {
		t.Fatalf("EvalSymlinks explicit home: %v", err)
	}
	if targets[0].Provider != "codex" || targets[0].HomeRoot != wantHome || targets[0].SkillsRoot != filepath.Join(wantHome, "skills") || !targets[0].AllowExplicitHome {
		t.Fatalf("explicit personal target = %#v, want home %q", targets[0], wantHome)
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	if targets[1].Provider != "codex" || targets[1].SkillsRoot != filepath.Join(wantProject, ".codex", "skills") {
		t.Fatalf("project target = %#v, want project skills under %q", targets[1], wantProject)
	}
}

func TestStartSessionRejectsEmptyCWDBeforeMirrorReconcile(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	pool := NewServerPool(slog.Default(), func(context.Context, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called with empty cwd")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{logger: slog.Default(), pool: pool, mirror: &recordingSkillMirrorReconciler{}}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{AgentID: "agent-empty-cwd"})
	if err == nil || !strings.Contains(err.Error(), "provider project cwd is required") {
		t.Fatalf("StartSession() error = %v, want cwd rejection", err)
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
