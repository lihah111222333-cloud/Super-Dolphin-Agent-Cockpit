package claudecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func validClaudeStartAssemblyForTest() dto.StartAssembly {
	return dto.StartAssembly{BaseInstructions: "test base instructions"}
}

func TestStartSessionProviderHomeChmodFailureBlocksStart(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}
	explicitHome := filepath.Join(blocker, "claude")
	launched := false
	d := newTestDriverWithLaunch(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		t.Fatal("launchCLI was called after provider home permission failure")
		return nil, nil, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-claude-home-permission",
		CWD:           t.TempDir(),
		StartAssembly: validClaudeStartAssemblyForTest(),
		Config:        map[string]any{"claude_home": explicitHome},
	})
	if got != nil {
		_ = got.Close(context.Background())
	}
	if err == nil {
		t.Fatalf("StartSession() error = nil, want provider home permission failure")
	}
	assertProviderStartupGateCode(t, err, "provider_home_permission_failed")
	if !strings.Contains(err.Error(), providershared.ProviderClaude) || !strings.Contains(err.Error(), explicitHome) {
		t.Fatalf("StartSession() error = %v, want provider and path context", err)
	}
	if launched {
		t.Fatalf("launchCLI was called")
	}
}

func TestDisallowedToolsRejectsMalformedConfig(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	launched := false
	next := newBufferedTransport(t, "claude-session-malformed-security-config")
	d := newTestDriverWithLaunch(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-claude-malformed-security-config",
		CWD:           t.TempDir(),
		StartAssembly: validClaudeStartAssemblyForTest(),
		Config: map[string]any{
			"disallowed_tools": map[string]any{"tool": "Read"},
		},
	})
	if got != nil {
		next.finish()
		_ = got.Close(context.Background())
	}
	if err == nil || !strings.Contains(err.Error(), "disallowed_tools") {
		t.Fatalf("StartSession() error = %v, want malformed disallowed_tools rejection", err)
	}
	if launched {
		t.Fatalf("launchCLI was called with malformed disallowed_tools")
	}
}

func TestStartSessionRejectsMalformedAutoApproveConfig(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	launched := false
	next := newBufferedTransport(t, "claude-session-malformed-auto-approve")
	d := newTestDriverWithLaunch(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-claude-malformed-auto-approve",
		CWD:           t.TempDir(),
		StartAssembly: validClaudeStartAssemblyForTest(),
		Config: map[string]any{
			"autoApprove": []any{"Read", 42},
		},
	})
	if got != nil {
		next.finish()
		_ = got.Close(context.Background())
	}
	if err == nil || !strings.Contains(err.Error(), "autoApprove[1]") {
		t.Fatalf("StartSession() error = %v, want malformed autoApprove rejection", err)
	}
	if launched {
		t.Fatalf("launchCLI was called with malformed autoApprove")
	}
}

func TestStartSessionKeepsExplicitClaudeHome(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	explicitHome := filepath.Join(t.TempDir(), "explicit-claude")
	if err := os.MkdirAll(explicitHome, 0o700); err != nil {
		t.Fatalf("MkdirAll explicit home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(explicitHome)
	if err != nil {
		t.Fatalf("EvalSymlinks explicit home: %v", err)
	}
	workDir := t.TempDir()
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	next := newBufferedTransport(t, "claude-session-explicit")
	d := newTestDriverWithLaunch(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want explicit %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-claude-explicit",
		CWD:           workDir,
		StartAssembly: validClaudeStartAssemblyForTest(),
		Config:        map[string]any{"claude_home": explicitHome},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())
	s := got.(*session)
	if s.history == nil || s.history.sessionDir != wantHome {
		t.Fatalf("history sessionDir = %#v, want %q", s.history, wantHome)
	}
}

func TestStartSessionAcceptsCamelCaseClaudeHomeAndPreservesSettings(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	explicitHome := filepath.Join(t.TempDir(), "explicit-claude")
	if err := os.MkdirAll(explicitHome, 0o700); err != nil {
		t.Fatalf("MkdirAll explicit home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(explicitHome)
	if err != nil {
		t.Fatalf("EvalSymlinks explicit home: %v", err)
	}
	workDir := t.TempDir()
	settingsPath := filepath.Join(workDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll settings dir: %v", err)
	}
	originalSettings := `{"model":"opus[1m]","permissions":{"allow":["Read"],"deny":["Read"]},"hooks":{"PreToolUse":[]}}`
	if err := os.WriteFile(settingsPath, []byte(originalSettings), 0o644); err != nil {
		t.Fatalf("WriteFile settings: %v", err)
	}
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	next := newBufferedTransport(t, "claude-session-camel-home")
	d := newTestDriverWithLaunch(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want explicit %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-claude-camel-home",
		CWD:           workDir,
		StartAssembly: validClaudeStartAssemblyForTest(),
		Config:        map[string]any{"claudeHome": explicitHome},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())
	assertFileContent(t, settingsPath, originalSettings)
}

func TestStartSessionOmitsClaudeHomeFromRuntimeSnapshotByDefault(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	workDir := t.TempDir()
	next := newBufferedTransport(t, "claude-session-runtime-home")
	d := newTestDriverWithLaunch(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != "" {
			t.Fatalf("ClaudeHome = %q, want empty default so Claude uses user CLI identity", cfg.ClaudeHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-runtime-home",
		CWD:           workDir,
		StartAssembly: validClaudeStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())
	runtimeCfg := got.(*session).RuntimeConfigSnapshot()
	for _, key := range []string{"claude_home", "claudeHome", "history_dir"} {
		if value, ok := runtimeCfg[key]; ok {
			t.Fatalf("RuntimeConfigSnapshot()[%q] = %#v, want omitted by default", key, value)
		}
	}
}

func TestStartSessionNormalizesExplicitClaudeHomeBeforeProviderNativeLaunch(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	explicitHome := filepath.Join(userHome, "explicit-claude")
	if err := os.MkdirAll(explicitHome, 0o700); err != nil {
		t.Fatalf("MkdirAll explicit home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(explicitHome)
	if err != nil {
		t.Fatalf("EvalSymlinks explicit home: %v", err)
	}
	workDir := t.TempDir()
	next := newBufferedTransport(t, "claude-session-normalized")
	d := newTestDriverWithLaunch(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want normalized %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-claude-normalized",
		CWD:           workDir,
		StartAssembly: validClaudeStartAssemblyForTest(),
		Config:        map[string]any{"claude_home": "~/explicit-claude"},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())
	s := got.(*session)
	if s.history == nil || s.history.sessionDir != wantHome {
		t.Fatalf("history sessionDir = %#v, want %q", s.history, wantHome)
	}
}

func TestResumeSessionKeepsExplicitClaudeHomeBeforeProviderNativeLaunch(t *testing.T) {
	explicitHome := filepath.Join(t.TempDir(), "explicit-claude")
	if err := os.MkdirAll(explicitHome, 0o700); err != nil {
		t.Fatalf("MkdirAll explicit home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(explicitHome)
	if err != nil {
		t.Fatalf("EvalSymlinks explicit home: %v", err)
	}
	workDir := t.TempDir()
	next := newBufferedTransport(t, "claude-session-resumed")
	d := newTestDriverWithLaunch(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want explicit %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		AgentID:          "agent-claude-resume",
		ThreadID:         "thread-claude-resume",
		ProviderThreadID: "11111111-2222-3333-4444-555555555555",
		CWD:              workDir,
		ClaudeHome:       explicitHome,
		PromptSnapshot:   validResumePromptSnapshotForTest(),
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())
	s := got.(*session)
	if s.history == nil || s.history.sessionDir != wantHome {
		t.Fatalf("history sessionDir = %#v, want %q", s.history, wantHome)
	}
}

func TestResumeSessionRejectsMalformedAutoApproveConfig(t *testing.T) {
	launched := false
	next := newBufferedTransport(t, "provider-thread-malformed-auto-approve")
	d := newTestDriverWithLaunch(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		AgentID:          "agent-claude-resume-malformed-auto-approve",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-malformed-auto-approve",
		CWD:              t.TempDir(),
		PromptSnapshot:   validResumePromptSnapshotForTest(),
		Config: map[string]any{
			"auto_approve": []any{"Write", false},
		},
	})
	if got != nil {
		next.finish()
		_ = got.Close(context.Background())
	}
	if err == nil || !strings.Contains(err.Error(), "auto_approve[1]") {
		t.Fatalf("ResumeSession() error = %v, want malformed auto_approve rejection", err)
	}
	if launched {
		t.Fatalf("launchCLI was called with malformed auto_approve")
	}
}

func TestResumeSessionRuntimeConfigSnapshotIncludesCWDAndRequestConfig(t *testing.T) {
	workDir := t.TempDir()
	next := newBufferedTransport(t, "claude-session-runtime-cwd")
	d := newTestDriverWithLaunch(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		AgentID:          "agent-claude-runtime",
		ThreadID:         "thread-claude-runtime",
		ProviderThreadID: "11111111-2222-3333-4444-555555555555",
		CWD:              workDir,
		PromptSnapshot:   validResumePromptSnapshotForTest(),
		Config: map[string]any{
			"gitRoot":  workDir,
			"provider": "claude",
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())

	runtimeCfg := got.(*session).RuntimeConfigSnapshot()
	if runtimeCfg["cwd"] != workDir {
		t.Fatalf("RuntimeConfigSnapshot()[cwd] = %#v, want %q; snapshot=%#v", runtimeCfg["cwd"], workDir, runtimeCfg)
	}
	if runtimeCfg["provider"] != "claude" {
		t.Fatalf("RuntimeConfigSnapshot()[provider] = %#v, want claude; snapshot=%#v", runtimeCfg["provider"], runtimeCfg)
	}
	if runtimeCfg["gitRoot"] != workDir {
		t.Fatalf("RuntimeConfigSnapshot()[gitRoot] = %#v, want %q; snapshot=%#v", runtimeCfg["gitRoot"], workDir, runtimeCfg)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	if got := string(data); got != want {
		t.Fatalf("file %s = %q, want %q", path, got, want)
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
