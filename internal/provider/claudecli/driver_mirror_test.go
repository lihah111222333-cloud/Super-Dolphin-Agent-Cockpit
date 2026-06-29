package claudecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

type recordingMirrorReconciler struct {
	events  *[]string
	err     error
	report  contract.SkillMirrorReport
	cwd     string
	targets []contract.SkillProviderMirrorTarget
}

func (r *recordingMirrorReconciler) ReconcileProviderMirrors(ctx context.Context, cwd string, targets []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	if r.events != nil {
		*r.events = append(*r.events, "reconcile")
	}
	r.cwd = cwd
	r.targets = append([]contract.SkillProviderMirrorTarget(nil), targets...)
	return r.report, r.err
}

func TestStartSessionReconcilesMirrorsBeforeLaunchWithUserClaudeHome(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	workDir := t.TempDir()
	events := []string{}
	mirror := &recordingMirrorReconciler{
		events: &events,
	}
	next := newBufferedTransport(t, "claude-session-1")
	d := newTestDriverWithLaunch(t, mirror, func(_, cwd, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		events = append(events, "launch")
		if cwd != workDir {
			t.Fatalf("launch cwd = %q, want %q", cwd, workDir)
		}
		if cfg.ClaudeHome != "" {
			t.Fatalf("ClaudeHome = %q, want empty default so Claude uses user CLI identity", cfg.ClaudeHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude",
		CWD:     workDir,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())
	if strings.Join(events, ",") != "reconcile,launch" {
		t.Fatalf("events = %v, want reconcile before launch", events)
	}
	assertClaudeMirrorTargets(t, mirror.targets, workDir, userHome)
}

func TestStartSessionReconcilesProjectMirrorsFromGitRootBeforeLaunch(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
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
	mirror := &recordingMirrorReconciler{events: &events}
	next := newBufferedTransport(t, "claude-session-subdir")
	d := newTestDriverWithLaunch(t, mirror, func(_, cwd, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		events = append(events, "launch")
		if cwd != subdir {
			t.Fatalf("launch cwd = %q, want request cwd %q", cwd, subdir)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-subdir",
		CWD:     subdir,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())
	if strings.Join(events, ",") != "reconcile,launch" {
		t.Fatalf("events = %v, want reconcile before launch", events)
	}
	assertClaudeMirrorTargets(t, mirror.targets, repoRoot, userHome)
}

func TestStartSessionMirrorContentConflictBlocksClaudeLaunch(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	launched := false
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{report: contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
		TargetID:     "claude:project:conflict",
		Scope:        "project",
		ConflictKind: "drift",
	}}}}, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		t.Fatal("launchCLI was called with active project mirror drift")
		return nil, nil, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-conflict",
		CWD:     t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("StartSession() error = %v, want active mirror drift startup error", err)
	}
	if got != nil {
		_ = got.Close(context.Background())
	}
	if launched {
		t.Fatalf("launchCLI was called")
	}
}

func TestStartSessionMirrorSafetyConflictBlocksClaudeLaunch(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	launched := false
	next := newBufferedTransport(t, "claude-session-mirror-safety-conflict")
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{report: contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
		TargetID:     "claude:project:conflict",
		ConflictKind: "mirror_root_symlink",
	}}}}, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-safety-conflict",
		CWD:     t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "skill mirror conflicts") || !strings.Contains(err.Error(), "mirror_root_symlink") {
		t.Fatalf("StartSession() error = %v, want blocking mirror safety conflict", err)
	}
	if got != nil {
		t.Fatalf("StartSession() session = %#v, want nil", got)
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
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{}, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-malformed-security-config",
		CWD:     t.TempDir(),
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

func TestStartSessionRequiresSkillMirrorReconciler(t *testing.T) {
	t.Setenv(providershared.SuperDolphinHomeEnv, filepath.Join(t.TempDir(), "sd-home"))
	d := newTestDriverWithLaunch(t, nil, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		t.Fatal("launchCLI called without skill mirror reconciler")
		return nil, nil, nil
	})

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-no-mirror",
		CWD:     t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "skill mirror reconciler") {
		t.Fatalf("StartSession() error = %v, want skill mirror reconciler requirement", err)
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
	mirror := &recordingMirrorReconciler{}
	next := newBufferedTransport(t, "claude-session-explicit")
	d := newTestDriverWithLaunch(t, mirror, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want explicit %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-explicit",
		CWD:     workDir,
		Config:  map[string]any{"claude_home": explicitHome},
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
	assertExplicitClaudeMirrorTargets(t, mirror.targets, workDir, wantHome)
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
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{}, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want explicit %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-camel-home",
		CWD:     workDir,
		Config:  map[string]any{"claudeHome": explicitHome},
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
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{}, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != "" {
			t.Fatalf("ClaudeHome = %q, want empty default so Claude uses user CLI identity", cfg.ClaudeHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-runtime-home",
		CWD:     workDir,
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

func TestStartSessionNormalizesExplicitClaudeHomeBeforeLaunchAndMirror(t *testing.T) {
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
	mirror := &recordingMirrorReconciler{}
	next := newBufferedTransport(t, "claude-session-normalized")
	d := newTestDriverWithLaunch(t, mirror, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want normalized %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-normalized",
		CWD:     workDir,
		Config:  map[string]any{"claude_home": "~/explicit-claude"},
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
	assertExplicitClaudeMirrorTargets(t, mirror.targets, workDir, wantHome)
}

func TestResumeSessionKeepsExplicitClaudeHomeBeforeLaunchAndMirror(t *testing.T) {
	explicitHome := filepath.Join(t.TempDir(), "explicit-claude")
	if err := os.MkdirAll(explicitHome, 0o700); err != nil {
		t.Fatalf("MkdirAll explicit home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(explicitHome)
	if err != nil {
		t.Fatalf("EvalSymlinks explicit home: %v", err)
	}
	workDir := t.TempDir()
	mirror := &recordingMirrorReconciler{}
	next := newBufferedTransport(t, "claude-session-resumed")
	d := newTestDriverWithLaunch(t, mirror, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
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
	assertExplicitClaudeMirrorTargets(t, mirror.targets, workDir, wantHome)
}

func TestResumeSessionRuntimeConfigSnapshotIncludesCWDAndRequestConfig(t *testing.T) {
	workDir := t.TempDir()
	next := newBufferedTransport(t, "claude-session-runtime-cwd")
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{}, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
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

func TestStartSessionMirrorFailureBlocksClaudeLaunch(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	launched := false
	next := newBufferedTransport(t, "claude-session-mirror-failure")
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{err: errors.New("mirror unavailable")}, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, func() { next.finish() }, nil
	})

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-blocked",
		CWD:     t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "mirror unavailable") {
		t.Fatalf("StartSession() error = %v, want mirror failure", err)
	}
	if got != nil {
		t.Fatalf("StartSession() session = %#v, want nil", got)
	}
	if launched {
		t.Fatalf("launchCLI was called")
	}
}

func assertClaudeMirrorTargets(t *testing.T, targets []contract.SkillProviderMirrorTarget, project, userHome string) {
	t.Helper()
	wantPersonalHome := filepath.Join(userHome, ".claude")
	wantPersonalSkills := filepath.Join(wantPersonalHome, "skills")
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	wantProjectSkills := filepath.Join(wantProject, ".claude", "skills")
	if len(targets) != 2 {
		t.Fatalf("mirror targets = %#v, want personal + project", targets)
	}
	if targets[0].Provider != "claude" || targets[0].HomeRoot != wantPersonalHome || targets[0].SkillsRoot != wantPersonalSkills {
		t.Fatalf("personal target = %#v, want home %q skills %q", targets[0], wantPersonalHome, wantPersonalSkills)
	}
	if targets[1].Provider != "claude" || targets[1].SkillsRoot != wantProjectSkills {
		t.Fatalf("project target = %#v, want skills %q", targets[1], wantProjectSkills)
	}
}

func assertExplicitClaudeMirrorTargets(t *testing.T, targets []contract.SkillProviderMirrorTarget, project, explicitHome string) {
	t.Helper()
	if len(targets) != 2 {
		t.Fatalf("mirror targets = %#v, want personal + project", targets)
	}
	if targets[0].Provider != "claude" || targets[0].HomeRoot != explicitHome || targets[0].SkillsRoot != filepath.Join(explicitHome, "skills") || !targets[0].AllowExplicitHome {
		t.Fatalf("explicit personal target = %#v, want home %q", targets[0], explicitHome)
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	if targets[1].Provider != "claude" || targets[1].SkillsRoot != filepath.Join(wantProject, ".claude", "skills") {
		t.Fatalf("project target = %#v, want project skills under %q", targets[1], wantProject)
	}
}
