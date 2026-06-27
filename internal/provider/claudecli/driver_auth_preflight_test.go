package claudecli

import (
	"context"
	"errors"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestDriverStartSessionFailsFastWhenClaudeAuthMissing(t *testing.T) {
	var launched bool
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return nil, nil, nil
	})

	d := newDriver(nil, nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)
	d.launchCLI = launchFn
	d.authStatus = func(context.Context, string, string, cliLaunchConfig) (claudeAuthStatus, string, error) {
		return claudeAuthStatus{LoggedIn: false, AuthMethod: "none", APIProvider: "firstParty"}, `{"loggedIn":false}`, nil
	}
	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-auth-missing",
		CWD:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("StartSession() error = nil, want auth preflight failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "authentication required") {
		t.Fatalf("StartSession() error = %v, want authentication required", err)
	}
	if launched {
		t.Fatal("launchCLI was called after failed auth preflight")
	}
}

func TestDriverStartSessionPassesClaudeHomeToAuthPreflight(t *testing.T) {
	var preflightHome string
	next := newBufferedTransport(t, "provider-thread-auth-ok")
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return next.tr, nil, nil
	})

	claudeHome := t.TempDir()
	d := newDriver(nil, nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)
	d.launchCLI = launchFn
	d.authStatus = func(_ context.Context, _ string, _ string, cfg cliLaunchConfig) (claudeAuthStatus, string, error) {
		preflightHome = cfg.ClaudeHome
		return claudeAuthStatus{LoggedIn: true, AuthMethod: "oauth_token", APIProvider: "firstParty"}, `{"loggedIn":true}`, nil
	}
	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-auth-ok",
		CWD:      t.TempDir(),
		Config: map[string]any{
			"claude_home": claudeHome,
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if preflightHome == "" || !strings.Contains(preflightHome, claudeHome) {
		t.Fatalf("preflight ClaudeHome = %q, want normalized home containing %q", preflightHome, claudeHome)
	}
}

func TestDriverStartSessionFailsFastWhenClaudeAuthStatusFails(t *testing.T) {
	var launched bool
	next := newBufferedTransport(t, "provider-thread-auth-status-error")
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, nil, nil
	})

	d := newDriver(nil, nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)
	d.launchCLI = launchFn
	d.authStatus = func(context.Context, string, string, cliLaunchConfig) (claudeAuthStatus, string, error) {
		return claudeAuthStatus{}, "unsupported auth status command", errors.New("exit status 1")
	}
	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-auth-status-error",
		CWD:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("StartSession() error = nil, want auth status failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "auth status") {
		t.Fatalf("StartSession() error = %v, want auth status context", err)
	}
	if launched {
		t.Fatal("launchCLI was called after failed auth status preflight")
	}
}

func TestDriverStartSessionFailsFastWhenClaudeAuthStatusIsInconclusive(t *testing.T) {
	var launched bool
	next := newBufferedTransport(t, "provider-thread-auth-status-inconclusive")
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, nil, nil
	})

	d := newDriver(nil, nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)
	d.launchCLI = launchFn
	d.authStatus = func(context.Context, string, string, cliLaunchConfig) (claudeAuthStatus, string, error) {
		return claudeAuthStatus{}, `{}`, nil
	}
	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-auth-status-inconclusive",
		CWD:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("StartSession() error = nil, want inconclusive auth status failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "inconclusive") {
		t.Fatalf("StartSession() error = %v, want inconclusive context", err)
	}
	if launched {
		t.Fatal("launchCLI was called after inconclusive auth status")
	}
}
