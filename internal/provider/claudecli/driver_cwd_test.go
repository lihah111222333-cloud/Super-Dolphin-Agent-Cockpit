package claudecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestDriverStartSessionRejectsMissingCWDBeforeWorkspaceSideEffects(t *testing.T) {
	parent := t.TempDir()
	missingCWD := filepath.Join(parent, "missing-worktree")
	launchCalled := false
	mirror := &recordingMirrorReconciler{}
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launchCalled = true
		return nil, nil, errors.New("launch should not be reached")
	})
	d := &driver{
		mirror:     mirror,
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
	if mirror.cwd != "" || len(mirror.targets) != 0 {
		t.Fatalf("mirror reconciler called before cwd validation: cwd=%q targets=%d", mirror.cwd, len(mirror.targets))
	}
	if launchCalled {
		t.Fatal("launchCLI called before cwd validation")
	}
	if _, statErr := os.Stat(missingCWD); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing cwd stat error = %v, want os.ErrNotExist", statErr)
	}
}

func TestDriverResumeSessionRejectsMissingProviderThreadIDBeforeLaunch(t *testing.T) {
	launchCalled := false
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launchCalled = true
		return nil, nil, errors.New("launch should not be reached")
	})
	d := &driver{
		mirror:     &recordingMirrorReconciler{},
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
