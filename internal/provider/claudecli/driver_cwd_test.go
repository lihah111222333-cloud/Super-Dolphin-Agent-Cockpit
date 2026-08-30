package claudecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestDriverStartSessionRejectsMissingCWDBeforeWorkspaceSideEffects(t *testing.T) {
	parent := t.TempDir()
	missingCWD := filepath.Join(parent, "missing-worktree")
	launchCalled := false
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launchCalled = true
		return nil, nil, errors.New("launch should not be reached")
	})
	d := &driver{
		launchCLI:  launchFn,
		authStatus: loggedInClaudeAuthStatus,
	}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-1",
		CWD:      missingCWD,
		Model:    "sonnet",
	})

	if err == nil {
		t.Fatal("StartSession() error = nil, want missing cwd error")
	}
	if !strings.Contains(err.Error(), "cwd") || !strings.Contains(err.Error(), missingCWD) {
		t.Fatalf("StartSession() error = %v, want missing cwd path", err)
	}
	if launchCalled {
		t.Fatal("launchCLI called before cwd validation")
	}
	if _, statErr := os.Stat(missingCWD); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing cwd stat error = %v, want os.ErrNotExist", statErr)
	}
}

func TestDriverStartSessionRejectsUnsafeCWDBeforeCreatingClaudeHome(t *testing.T) {
	root := t.TempDir()
	missingCWD := filepath.Join(root, "missing-worktree")
	requestedHome := filepath.Join(root, "claude-home")
	d := &driver{authStatus: loggedInClaudeAuthStatus}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-cwd-before-home",
		CWD:      missingCWD,
		Config:   map[string]any{"claudeHome": requestedHome},
	})
	if err == nil {
		t.Fatal("StartSession() error = nil, want cwd validation failure")
	}
	if _, statErr := os.Stat(requestedHome); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("requested Claude home stat = %v, want no home creation", statErr)
	}
}

func TestDriverResumeSessionRejectsMissingProviderThreadIDBeforeLaunch(t *testing.T) {
	launchCalled := false
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launchCalled = true
		return nil, nil, errors.New("launch should not be reached")
	})
	d := &driver{
		launchCLI:  launchFn,
		authStatus: loggedInClaudeAuthStatus,
	}

	_, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider: "claude",
		AgentID:  "agent-1",
		ThreadID: "thread-public",
		CWD:      t.TempDir(),
	})

	if err == nil || !strings.Contains(err.Error(), "provider thread id is required") {
		t.Fatalf("ResumeSession() error = %v, want provider thread id required", err)
	}
	if launchCalled {
		t.Fatal("launchCLI called before provider thread id validation")
	}
}

func TestDriverStartSessionCanonicalizesCWDForLaunchAndNativeContext(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "cwd-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	next := newBufferedTransport(t, "claude-canonical-start")
	var launchCWD string
	d := newTestDriverWithLaunch(t, func(_, cwd, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		launchCWD = cwd
		return next.tr, nil, nil
	})

	sessionAny, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:     "claude",
		AgentID:      "agent-canonical-start",
		CWD:          " " + filepath.Join(link, ".") + " ",
		Instructions: "base",
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s, ok := sessionAny.(*session)
	if !ok {
		t.Fatalf("StartSession() type = %T, want *session", sessionAny)
	}
	defer s.Close(context.Background())
	defer next.finish()
	if launchCWD != root {
		t.Fatalf("launch cwd = %q, want canonical %q", launchCWD, root)
	}
	if s.cwd != root || s.nativeContext.CWD != root {
		t.Fatalf("session cwd/context = (%q,%q), want %q", s.cwd, s.nativeContext.CWD, root)
	}
	if err := providershared.ValidateProviderNativeContext(s.nativeContext); err != nil {
		t.Fatalf("native context validation error = %v", err)
	}
	if got := s.RuntimeConfigSnapshot()["cwd"]; got != root {
		t.Fatalf("RuntimeConfigSnapshot()[cwd] = %#v, want %q", got, root)
	}
}

func TestDriverResumeSessionCanonicalizesCWDForLaunchAndNativeContext(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "cwd-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	next := newBufferedTransport(t, "claude-canonical-resume")
	var launchCWD string
	d := newTestDriverWithLaunch(t, func(_, cwd, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		launchCWD = cwd
		return next.tr, nil, nil
	})

	sessionAny, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-canonical-resume",
		ThreadID:         "thread-canonical-resume",
		ProviderThreadID: "provider-thread-canonical-resume",
		CWD:              filepath.Join(link, "."),
		PromptSnapshot:   validResumePromptSnapshotForTest(),
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s, ok := sessionAny.(*session)
	if !ok {
		t.Fatalf("ResumeSession() type = %T, want *session", sessionAny)
	}
	defer s.Close(context.Background())
	defer next.finish()
	if launchCWD != root {
		t.Fatalf("launch cwd = %q, want canonical %q", launchCWD, root)
	}
	if s.cwd != root || s.nativeContext.CWD != root {
		t.Fatalf("session cwd/context = (%q,%q), want %q", s.cwd, s.nativeContext.CWD, root)
	}
}

func TestClaudeNativeContextRecordsExplicitSkillsDisablement(t *testing.T) {
	root := t.TempDir()
	spec := startSpec{
		cwd:        root,
		config:     configFromMap(map[string]any{"disableProviderNativeSkills": true}),
		historyDir: "",
		startAssembly: contract.StartAssembly{
			BaseInstructions: "base",
		},
	}
	d := &driver{}
	got, err := d.prepareNativeContext(spec)
	if err != nil {
		t.Fatalf("prepareNativeContext() error = %v", err)
	}
	if got.nativeContext.Capabilities.Skills.Status != contract.ProviderNativeStatusDisabled ||
		got.nativeContext.Capabilities.Skills.Owner != contract.ProviderNativeOwnerDisabled {
		t.Fatalf("skills capability = %#v, want explicit disabled", got.nativeContext.Capabilities.Skills)
	}
	if err := providershared.ValidateProviderNativeContext(got.nativeContext); err != nil {
		t.Fatalf("native context validation error = %v", err)
	}
}
