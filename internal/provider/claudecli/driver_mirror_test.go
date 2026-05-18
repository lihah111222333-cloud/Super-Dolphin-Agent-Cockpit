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

func TestStartSessionReconcilesMirrorsBeforeLaunchWithAppManagedClaudeHome(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	wantHomePath := filepath.Join(superHome, "providers", "claude")
	events := []string{}
	mirror := &recordingMirrorReconciler{
		events: &events,
	}
	next := newBufferedTransport(t, "claude-session-1")
	overrideLaunchCLI(t, func(_, cwd, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		events = append(events, "launch")
		if cwd != workDir {
			t.Fatalf("launch cwd = %q, want %q", cwd, workDir)
		}
		wantHome, err := filepath.EvalSymlinks(wantHomePath)
		if err != nil {
			t.Fatalf("EvalSymlinks app-managed home: %v", err)
		}
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want app-managed %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, mirror, nil).(*driver)

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
	assertClaudeMirrorTargets(t, mirror.targets, workDir, superHome)
}

func TestStartSessionReconcilesProjectMirrorsFromGitRootBeforeLaunch(t *testing.T) {
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
	mirror := &recordingMirrorReconciler{events: &events}
	next := newBufferedTransport(t, "claude-session-subdir")
	overrideLaunchCLI(t, func(_, cwd, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		events = append(events, "launch")
		if cwd != subdir {
			t.Fatalf("launch cwd = %q, want request cwd %q", cwd, subdir)
		}
		return next.tr, func() { next.finish() }, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, mirror, nil).(*driver)

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
	assertClaudeMirrorTargets(t, mirror.targets, repoRoot, superHome)
}

func TestStartSessionMirrorConflictBlocksClaudeLaunch(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		t.Fatal("launchCLI called after mirror conflict")
		return nil, nil, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, &recordingMirrorReconciler{report: contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
		TargetID:     "claude:project:conflict",
		ConflictKind: "mirror_drift",
	}}}}, nil).(*driver)

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-conflict",
		CWD:     t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "skill mirror conflicts") {
		t.Fatalf("StartSession() error = %v, want skill mirror conflicts", err)
	}
}

func TestStartSessionRequiresSkillMirrorReconciler(t *testing.T) {
	t.Setenv(providershared.SuperDolphinHomeEnv, filepath.Join(t.TempDir(), "sd-home"))
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		t.Fatal("launchCLI called without skill mirror reconciler")
		return nil, nil, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, nil, nil).(*driver)

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
	overrideLaunchCLI(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want explicit %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, mirror, nil).(*driver)

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
	overrideLaunchCLI(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want explicit %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)

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

func TestStartSessionIncludesAppManagedClaudeHomeInRuntimeSnapshot(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	workDir := t.TempDir()
	wantHomePath := filepath.Join(superHome, "providers", "claude")
	next := newBufferedTransport(t, "claude-session-runtime-home")
	overrideLaunchCLI(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		wantHome, err := filepath.EvalSymlinks(wantHomePath)
		if err != nil {
			t.Fatalf("EvalSymlinks app-managed home: %v", err)
		}
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want app-managed %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-runtime-home",
		CWD:     workDir,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	next.finish()
	defer got.Close(context.Background())
	wantHome, err := filepath.EvalSymlinks(wantHomePath)
	if err != nil {
		t.Fatalf("EvalSymlinks app-managed home: %v", err)
	}
	runtimeCfg := got.(*session).RuntimeConfigSnapshot()
	for _, key := range []string{"claude_home", "claudeHome", "history_dir"} {
		if value, ok := runtimeCfg[key]; !ok || value != wantHome {
			t.Fatalf("RuntimeConfigSnapshot()[%q] = %#v, want app-managed %q", key, value, wantHome)
		}
	}
}

func TestStartSessionNormalizesExplicitClaudeHomeBeforeLaunchAndMirror(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
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
	overrideLaunchCLI(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want normalized %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, mirror, nil).(*driver)

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
	overrideLaunchCLI(t, func(_, _, _, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		if cfg.ClaudeHome != wantHome {
			t.Fatalf("ClaudeHome = %q, want explicit %q", cfg.ClaudeHome, wantHome)
		}
		return next.tr, func() { next.finish() }, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, mirror, nil).(*driver)

	got, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		AgentID:    "agent-claude-resume",
		ThreadID:   "thread-claude-resume",
		CWD:        workDir,
		ClaudeHome: explicitHome,
		PromptSnapshot: dto.PromptAssemblySnapshot{
			DisplayName:      "resume",
			BaseInstructions: "base",
			Provider:         "claude",
		},
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
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		t.Fatal("launchCLI called after mirror reconcile failure")
		return nil, nil, nil
	})
	d := newDriver(nil, nil, nil, nil, nil, &recordingMirrorReconciler{err: errors.New("mirror unavailable")}, nil).(*driver)

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-claude-blocked",
		CWD:     t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "mirror unavailable") {
		t.Fatalf("StartSession() error = %v, want mirror unavailable", err)
	}
}

func assertClaudeMirrorTargets(t *testing.T, targets []contract.SkillProviderMirrorTarget, project, superHome string) {
	t.Helper()
	wantPersonalHome := mustClaudeProviderHome(t, superHome)
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

func mustClaudeProviderHome(t *testing.T, superHome string) string {
	t.Helper()
	wantPersonalHome, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "claude"))
	if err != nil {
		t.Fatalf("EvalSymlinks personal home: %v", err)
	}
	return wantPersonalHome
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
