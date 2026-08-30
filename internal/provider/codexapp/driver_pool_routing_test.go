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
	return &driver{logRuntime: testLoggerRuntime(t),
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
		contract.RuntimeConfigCodexDisabledNativeTools().Canonical: []string{contract.CodexNativeToolApplyPatch},
	})

	if out["sandbox"] != "read-only" {
		t.Fatalf("sandbox = %#v, want read-only", out["sandbox"])
	}
	assertRestrictedReadOnlyPolicyValue(t, out["sandboxPolicy"], []string{"/repo/app", "/Users/ai/shared"}, true)
}

// TestCanonicalStartRuntimeConfigPreservesDangerSandboxWhenWriteToolsAreNativelyDisabled
// 锁定完整 unified-exec 工具组已由 App Server 原生硬禁用时，不得再把用户显式高权限沙箱降为只读。
func TestCanonicalStartRuntimeConfigPreservesDangerSandboxWhenWriteToolsAreNativelyDisabled(t *testing.T) {
	out := canonicalStartRuntimeConfig(map[string]any{
		"sandbox": map[string]any{"type": "dangerFullAccess"},
		contract.RuntimeConfigCodexDisabledNativeTools().Canonical: []string{
			contract.CodexNativeToolShell,
			contract.CodexNativeToolApplyPatch,
			contract.CodexNativeToolWriteNewFile,
		},
	})
	if policy, ok := out["sandboxPolicy"].(map[string]any); !ok || policy["type"] != "dangerFullAccess" {
		t.Fatalf("sandboxPolicy = %#v, want dangerFullAccess", out["sandboxPolicy"])
	}
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

func TestStartSessionFailsClosedWhenPreparedIdentityHasNoPool(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	workDir := t.TempDir()
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default()}

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
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default(), pool: pool}

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
	if acquires.Load() != 0 {
		t.Fatalf("pool acquire calls = %d, want 0", acquires.Load())
	}
}

func TestStartSessionUsesExplicitCodexHomeForProviderNativeDiscovery(t *testing.T) {
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
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default(), pool: pool}

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
		t.Fatalf("StartSession() error = %v, want acquire error after provider-native preparation", err)
	}
	wantHome, err := filepath.EvalSymlinks(explicitHome)
	if err != nil {
		t.Fatalf("EvalSymlinks explicit codex home: %v", err)
	}
	if gotHome != wantHome {
		t.Fatalf("pool codex home = %q, want %q", gotHome, wantHome)
	}
}

func TestStartSessionRejectsRelativeExplicitCodexHome(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	workDir := t.TempDir()
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called with relative codex home")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default(), pool: pool}

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

func TestStartSessionRejectsMalformedCodexIdentityBeforeHome(t *testing.T) {
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
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default(), pool: pool}

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
	if _, statErr := os.Stat(filepath.Join(superHome, "providers", "codex")); !os.IsNotExist(statErr) {
		t.Fatalf("provider home stat = %v, want not created", statErr)
	}
}

func TestStartSessionNormalizesExplicitCodexHomeBeforeProviderNativePoolLaunch(t *testing.T) {
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
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default(), pool: pool}

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
		t.Fatalf("StartSession() error = %v, want acquire error after provider-native preparation", err)
	}
	if gotHome != wantHome {
		t.Fatalf("pool codex home = %q, want %q", gotHome, wantHome)
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

func TestStartSessionRejectsEmptyCWD(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called with empty cwd")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default(), pool: pool}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{AgentID: "agent-empty-cwd"})
	if err == nil || !strings.Contains(err.Error(), "provider-native skills cwd is required") {
		t.Fatalf("StartSession() error = %v, want provider-native cwd rejection", err)
	}
}

func TestStartSessionRejectsUnsafeCWDBeforeCreatingCodexHome(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	t.Setenv(providershared.SuperDolphinHomeEnv, filepath.Join(t.TempDir(), "sd-home"))
	t.Setenv(providershared.RuntimeModeEnv, providershared.RuntimeModePackaged)
	root := t.TempDir()
	missingCWD := filepath.Join(root, "missing-worktree")
	explicitHome := filepath.Join(root, "explicit-codex-home")

	pool := NewServerPool(slog.Default(), func(context.Context, string, string) (SpawnedServer, error) {
		t.Fatal("pool acquire called before unsafe cwd validation")
		return nil, nil
	}, PoolConfig{})
	defer pool.Close(context.Background())
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default(), pool: pool}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-cwd-before-codex-home",
		CWD:           missingCWD,
		StartAssembly: validStartAssemblyForTest(),
		Config: map[string]any{
			contract.CodexHomeKey:          explicitHome,
			contract.CodexInstanceKeyKey:   "explicit",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "provider-native skills cwd") {
		t.Fatalf("StartSession() error = %v, want unsafe cwd rejection", err)
	}
	if _, statErr := os.Stat(explicitHome); !os.IsNotExist(statErr) {
		t.Fatalf("explicit Codex home stat = %v, want no home creation", statErr)
	}
}

func TestPrepareProviderNativeRequestsCanonicalizeCWD(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "cwd-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	d := &driver{}
	start, err := d.prepareStartSessionRequest(context.Background(), dto.StartSessionRequest{
		Provider: "codex",
		CWD:      filepath.Join(link, "."),
		Config:   identityConfig(t, "canonical-start"),
	})
	if err != nil {
		t.Fatalf("prepareStartSessionRequest() error = %v", err)
	}
	if start.CWD != root {
		t.Fatalf("start CWD = %q, want canonical %q", start.CWD, root)
	}
	resume, err := d.prepareResumeSessionRequest(context.Background(), dto.ResumeSessionRequest{
		Provider:         "codex",
		AgentID:          "agent-canonical-resume",
		ThreadID:         "thread-canonical-resume",
		ProviderThreadID: "provider-thread-canonical-resume",
		CWD:              " " + link + " ",
		Config:           identityConfig(t, "canonical-resume"),
	})
	if err != nil {
		t.Fatalf("prepareResumeSessionRequest() error = %v", err)
	}
	if resume.CWD != root {
		t.Fatalf("resume CWD = %q, want canonical %q", resume.CWD, root)
	}
}

func TestBuildCodexProviderNativeContextUsesCanonicalRequest(t *testing.T) {
	root := t.TempDir()
	request := dto.StartSessionRequest{
		CWD:    root,
		Config: identityConfig(t, "native-context"),
	}
	ctx, err := buildCodexProviderNativeContext(request)
	if err != nil {
		t.Fatalf("buildCodexProviderNativeContext() error = %v", err)
	}
	if ctx.Provider != contract.ProviderNativeProviderCodex || ctx.CWD != root {
		t.Fatalf("native context identity = (%q,%q), want (codex,%q)", ctx.Provider, ctx.CWD, root)
	}
	if err := providershared.ValidateProviderNativeContext(ctx); err != nil {
		t.Fatalf("ValidateProviderNativeContext() error = %v", err)
	}
	if err := ctx.RequireProviderFeature(contract.ProviderNativeFeatureSkills); !errors.Is(err, contract.ErrProviderNativeCapabilityUnknown) {
		t.Fatalf("RequireProviderFeature(skills) error = %v, want explicit unknown", err)
	}
}

func TestRecoveryResumeCWDUsesCanonicalNativePath(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "cwd-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	s := &session{}
	s.setRuntimeConfig(map[string]any{"cwd": filepath.Join(link, ".")})
	got, err := s.recoveryResumeCWD()
	if err != nil {
		t.Fatalf("recoveryResumeCWD() error = %v", err)
	}
	if got != root {
		t.Fatalf("recovery cwd = %q, want canonical %q", got, root)
	}
}

func TestRecoveryResumeCWDPrefersNativeContextOverMutableRuntimeConfig(t *testing.T) {
	nativeRoot := t.TempDir()
	mutableRoot := t.TempDir()
	s := &session{}
	s.setNativeContext(mustCodexNativeContextForTest(t, nativeRoot))
	s.setRuntimeConfig(map[string]any{"cwd": mutableRoot})

	got, err := s.recoveryResumeCWD()
	if err != nil {
		t.Fatalf("recoveryResumeCWD() error = %v", err)
	}
	if got != nativeRoot {
		t.Fatalf("recovery cwd = %q, want native context cwd %q", got, nativeRoot)
	}
}

func TestRuntimeConfigCWDHydratePrecedesRecoveryNativeContextValidation(t *testing.T) {
	nativeRoot := t.TempDir()
	mismatchRoot := t.TempDir()
	native := mustCodexNativeContextForTest(t, nativeRoot)

	tests := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{name: "hydrated canonical cwd"},
		{name: "missing cwd remains fail-fast", config: map[string]any{"cwd": ""}, wantErr: "cwd is required"},
		{name: "mismatch remains fail-fast", config: map[string]any{"cwd": mismatchRoot}, wantErr: "cwd mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &session{}
			s.setNativeContext(native)
			s.setRuntimeConfig(canonicalRuntimeConfigWithCWD(tt.config, nativeRoot))

			err := s.validateRecoveryNativeContext()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRecoveryNativeContext() error = %v, want nil after cwd hydrate", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateRecoveryNativeContext() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func mustCodexNativeContextForTest(t *testing.T, cwd string) contract.ProviderNativeContext {
	t.Helper()
	ctx, err := providershared.BuildProviderNativeContext(providershared.ProviderNativeContextInput{
		Provider:     contract.ProviderNativeProviderCodex,
		CWD:          cwd,
		Capabilities: providershared.UnknownProviderNativeCapabilities(),
	})
	if err != nil {
		t.Fatalf("BuildProviderNativeContext() error = %v", err)
	}
	return ctx
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
