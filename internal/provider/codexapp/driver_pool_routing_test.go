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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func newRoutingDriver(t *testing.T, pool *ServerPool) *driver {
	t.Helper()
	return &driver{
		approvals: testApprovalManager(),
		logger:    slog.Default(),
		pool:      pool,
	}
}

func newSingleURLPoolForTest(t *testing.T, url string) *ServerPool {
	t.Helper()
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
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

// TestCanonicalStartRuntimeConfigPreservesRestrictedReadOnlySandboxPolicy 确认 native tool 只读收敛
// 不能抹掉用户已经配置好的 restricted readable roots。
func TestCanonicalStartRuntimeConfigPreservesRestrictedReadOnlySandboxPolicy(t *testing.T) {
	out := canonicalStartRuntimeConfig(map[string]any{
		"sandbox": map[string]any{
			"type": "readOnly",
			"access": map[string]any{
				"type":                    "restricted",
				"readableRoots":           []string{"/repo/app", "/Users/ai/shared"},
				"includePlatformDefaults": true,
			},
		},
		codexDisabledNativeToolsConfigKey: []string{contract.CodexNativeToolApplyPatch},
	})

	if out["sandbox"] != "read-only" {
		t.Fatalf("sandbox = %#v, want read-only", out["sandbox"])
	}
	assertRestrictedReadOnlyPolicyValue(t, out["sandboxPolicy"], []string{"/repo/app", "/Users/ai/shared"}, true)
}

func assertRestrictedReadOnlyPolicyValue(t *testing.T, raw any, roots []string, includePlatformDefaults bool) {
	t.Helper()
	policy, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("sandboxPolicy = %#v, want object", raw)
	}
	if policy["type"] != "readOnly" {
		t.Fatalf("sandboxPolicy.type = %#v, want readOnly; policy=%#v", policy["type"], policy)
	}
	access, ok := policy["access"].(map[string]any)
	if !ok {
		t.Fatalf("sandboxPolicy.access = %#v, want object; policy=%#v", policy["access"], policy)
	}
	if access["type"] != "restricted" {
		t.Fatalf("sandboxPolicy.access.type = %#v, want restricted; access=%#v", access["type"], access)
	}
	gotRoots := anyStringSlice(access["readableRoots"])
	if len(gotRoots) != len(roots) {
		t.Fatalf("sandboxPolicy.access.readableRoots = %#v, want %#v", access["readableRoots"], roots)
	}
	for index := range roots {
		if gotRoots[index] != roots[index] {
			t.Fatalf("sandboxPolicy.access.readableRoots = %#v, want %#v", gotRoots, roots)
		}
	}
	if access["includePlatformDefaults"] != includePlatformDefaults {
		t.Fatalf(
			"sandboxPolicy.access.includePlatformDefaults = %#v, want %#v",
			access["includePlatformDefaults"],
			includePlatformDefaults,
		)
	}
}

func anyStringSlice(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, text)
		}
		return out
	default:
		return nil
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

func TestPoolRoutingExplicitlyDisabledAllowsMissingIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "0")
	spawnCalls := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://should-not-be-called"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := newRoutingDriver(t, pool)

	opts, err := d.resolveSessionOptions(context.Background(), dto.StartSessionRequest{AgentID: "a"})
	if err != nil {
		t.Fatalf("resolveSessionOptions() error = %v, want legacy path without identity when pool disabled", err)
	}
	if opts != nil {
		t.Fatalf("want nil opts with feature flag disabled, got %d", len(opts))
	}
	if spawnCalls.Load() != 0 {
		t.Fatalf("spawner should not fire when pool disabled, called %d times", spawnCalls.Load())
	}
}

// TestResolveSessionOptionsFailsClosedOnIdentityError checks the fail-closed
// contract: by default, a request missing codexHome must return an identity
// error instead of falling back to the legacy server.
func TestResolveSessionOptionsFailsClosedOnIdentityError(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	spawnCalls := atomic.Int32{}
	spawner := func(context.Context, string, string) (SpawnedServer, error) {
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
func TestStartSessionReconcilesMirrorsBeforePoolAcquireAndDefaultsIdentity(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	workDir := t.TempDir()
	events := []string{}
	var gotHome string
	pool := NewServerPool(slog.Default(), func(_ context.Context, home, _ string) (SpawnedServer, error) {
		events = append(events, "acquire")
		gotHome = home
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{
		events: &events,
	}
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-default",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
	})
	if err == nil || !strings.Contains(err.Error(), "stop after acquire") {
		t.Fatalf("StartSession() error = %v, want acquire error after reconcile", err)
	}
	if strings.Join(events, ",") != "reconcile,acquire" {
		t.Fatalf("events = %v, want reconcile before acquire", events)
	}
	wantHome, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks app-managed codex home: %v", err)
	}
	if gotHome != wantHome {
		t.Fatalf("pool codex home = %q, want %q", gotHome, wantHome)
	}
	assertExplicitCodexMirrorTargets(t, mirror.targets, workDir, wantHome)
}

func TestStartSessionReconcilesProjectMirrorsFromGitRootBeforePoolAcquire(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile .git/HEAD: %v", err)
	}
	subdir := filepath.Join(repoRoot, "cmd", "agent-terminal")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}
	events := []string{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		events = append(events, "acquire")
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{events: &events}
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-subdir",
		CWD:           subdir,
		StartAssembly: validStartAssemblyForTest(),
	})
	if err == nil || !strings.Contains(err.Error(), "stop after acquire") {
		t.Fatalf("StartSession() error = %v, want acquire error after reconcile", err)
	}
	if strings.Join(events, ",") != "reconcile,acquire" {
		t.Fatalf("events = %v, want reconcile before acquire", events)
	}
	wantHome, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks app-managed codex home: %v", err)
	}
	assertExplicitCodexMirrorTargets(t, mirror.targets, repoRoot, wantHome)
}

func TestStartSessionFailsClosedWhenPreparedIdentityHasNoPool(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	workDir := t.TempDir()
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), mirror: mirror}

	session, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-no-pool",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
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
	wantHome, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks app-managed codex home: %v", err)
	}
	assertExplicitCodexMirrorTargets(t, mirror.targets, workDir, wantHome)
}

func TestStartSessionMirrorContentConflictBlocksPoolAcquire(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	workDir := t.TempDir()
	acquires := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		acquires.Add(1)
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	d := &driver{
		approvals: testApprovalManager(),
		logger:    slog.Default(),
		pool:      pool,
		mirror: &recordingSkillMirrorReconciler{report: contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
			TargetID:     "codex:project:conflict",
			Scope:        "project",
			ConflictKind: "drift",
		}}}},
	}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-conflict",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
	})
	if err == nil || !strings.Contains(err.Error(), "skill mirror conflicts") || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("StartSession() error = %v, want blocking project mirror content conflict", err)
	}
	if acquires.Load() != 0 {
		t.Fatalf("pool acquire calls = %d, want 0", acquires.Load())
	}
}

func TestStartSessionMirrorSafetyConflictBlocksPoolAcquire(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	workDir := t.TempDir()
	acquires := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		acquires.Add(1)
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	d := &driver{
		approvals: testApprovalManager(),
		logger:    slog.Default(),
		pool:      pool,
		mirror: &recordingSkillMirrorReconciler{report: contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
			TargetID:     "codex:project:conflict",
			ConflictKind: "mirror_root_symlink",
		}}}},
	}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-safety-conflict",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
	})
	if err == nil || !strings.Contains(err.Error(), "skill mirror conflicts") || !strings.Contains(err.Error(), "mirror_root_symlink") {
		t.Fatalf("StartSession() error = %v, want blocking mirror safety conflict", err)
	}
	if acquires.Load() != 0 {
		t.Fatalf("pool acquire calls = %d, want 0", acquires.Load())
	}
}

func TestStartSessionProviderHomeChmodFailureBlocksStart(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}
	t.Setenv(providershared.SuperDolphinHomeEnv, filepath.Join(blocker, "sd-home"))
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	acquires := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		acquires.Add(1)
		t.Fatal("pool acquire called after provider home permission failure")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-codex-home-permission",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if err == nil {
		t.Fatalf("StartSession() error = nil, want provider home permission failure")
	}
	assertProviderStartupGateCode(t, err, "provider_home_permission_failed")
	if !strings.Contains(err.Error(), providershared.ProviderCodex) || !strings.Contains(err.Error(), filepath.Join(blocker, "sd-home")) {
		t.Fatalf("StartSession() error = %v, want provider and path context", err)
	}
	if mirror.calls != 0 {
		t.Fatalf("mirror reconcile calls = %d, want 0", mirror.calls)
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
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		acquires.Add(1)
		return newFakeServer("ws://unused"), nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-no-mirror",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
	})
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
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	workDir := t.TempDir()
	acquires := atomic.Int32{}
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		acquires.Add(1)
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	d := &driver{
		approvals: testApprovalManager(),
		logger:    slog.Default(),
		pool:      pool,
		mirror:    &recordingSkillMirrorReconciler{err: errors.New("mirror unavailable")},
	}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-blocked",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
	})
	if err == nil || !strings.Contains(err.Error(), "mirror unavailable") {
		t.Fatalf("StartSession() error = %v, want mirror reconcile failure", err)
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
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	workDir := t.TempDir()
	var gotHome string
	pool := NewServerPool(slog.Default(), func(_ context.Context, home, _ string) (SpawnedServer, error) {
		gotHome = home
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-explicit",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
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
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called with relative codex home")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: &recordingSkillMirrorReconciler{}}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-relative-home",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
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
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	workDir := t.TempDir()
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called for malformed codex identity")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-malformed-home",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
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
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	workDir := t.TempDir()
	realHome := filepath.Join(t.TempDir(), "real-codex")
	if err := os.MkdirAll(realHome, 0o700); err != nil {
		t.Fatalf("MkdirAll real codex home: %v", err)
	}
	aliasHome := filepath.Join(t.TempDir(), "alias-codex")
	if err := os.Symlink(realHome, aliasHome); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink codex home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(realHome)
	if err != nil {
		t.Fatalf("EvalSymlinks real codex home: %v", err)
	}
	var gotHome string
	pool := NewServerPool(slog.Default(), func(_ context.Context, home, _ string) (SpawnedServer, error) {
		gotHome = home
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: mirror}

	_, err = d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-explicit-normalized",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
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

func assertCodexMirrorTargets(t *testing.T, targets []contract.SkillProviderMirrorTarget, project, userHome string) {
	t.Helper()
	wantPersonalHome := filepath.Join(userHome, ".agents")
	wantPersonalSkills := filepath.Join(wantPersonalHome, "skills")
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	wantProjectSkills := filepath.Join(wantProject, ".agents", "skills")
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
	if targets[1].Provider != "codex" || targets[1].SkillsRoot != filepath.Join(wantProject, ".agents", "skills") {
		t.Fatalf("project target = %#v, want project skills under %q", targets[1], wantProject)
	}
}

func assertProviderStartupGateCode(t *testing.T, err error, want string) {
	t.Helper()
	var coded interface {
		GateCode() string
	}
	if !errors.As(err, &coded) {
		t.Fatalf("error = %T %v, want provider startup gate code %q", err, err, want)
	}
	if got := coded.GateCode(); got != want {
		t.Fatalf("GateCode() = %q, want %q; error=%v", got, want, err)
	}
}

func TestStartSessionRejectsEmptyCWDBeforeMirrorReconcile(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called with empty cwd")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: &recordingSkillMirrorReconciler{}}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{AgentID: "agent-empty-cwd"})
	if err == nil || !strings.Contains(err.Error(), "provider project cwd is required") {
		t.Fatalf("StartSession() error = %v, want cwd rejection", err)
	}
}

func TestPoolRoutingPassesStartCWDToSpawnerWorkDir(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	workDir := t.TempDir()
	var got string
	pool := NewServerPool(slog.Default(), func(ctx context.Context, home, _ string) (SpawnedServer, error) {
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
	spawner := func(context.Context, string, string) (SpawnedServer, error) {
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
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
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

func TestPoolRoutingDisabledStillRejectsInvalidIdentityConfig(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "0")
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
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

// TestPoolRoutingFlagFalsyStaysDisabled locks explicit false values:
// valid false disables routing; malformed values must be rejected by
// TestPoolRoutingFlagMalformedFailsFast instead of being treated as false.
func TestPoolRoutingFlagFalsyStaysDisabled(t *testing.T) {
	for _, v := range []string{"0", "false"} {
		t.Setenv(poolRoutingEnvVar, v)
		enabled, _, err := poolRoutingDecision()
		if err != nil {
			t.Fatalf("poolRoutingDecision(%q) error = %v", v, err)
		}
		if enabled {
			t.Fatalf("flag %q must parse as disabled", v)
		}
	}
}

func TestPoolRoutingFlagMalformedFailsFast(t *testing.T) {
	for _, v := range []string{"no", "garbage"} {
		t.Setenv(poolRoutingEnvVar, v)
		_, _, err := poolRoutingDecision()
		if err == nil || !strings.Contains(err.Error(), poolRoutingEnvVar) {
			t.Fatalf("poolRoutingDecision(%q) error = %v, want malformed env error", v, err)
		}
	}
}

func TestPoolRoutingFlagMissingEnablesStrictRouting(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	enabled, strict, err := poolRoutingDecision()
	if err != nil {
		t.Fatalf("poolRoutingDecision() error = %v", err)
	}
	if !enabled || !strict {
		t.Fatalf("missing pool flag = enabled %v strict %v, want true/true", enabled, strict)
	}
}
