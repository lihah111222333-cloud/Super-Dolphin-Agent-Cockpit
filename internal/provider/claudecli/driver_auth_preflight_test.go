package claudecli

import (
	"context"
	"errors"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func overrideClaudeAuthStatus(t *testing.T, fn func(context.Context, string, string, cliLaunchConfig) (claudeAuthStatus, string, error)) {
	t.Helper()
	// Call after overrideLaunchCLI(t, ...). That helper holds the provider
	// global override lock for the test lifetime and restores readClaudeAuthStatus
	// after this cleanup runs.
	prev := readClaudeAuthStatus
	readClaudeAuthStatus = fn
	t.Cleanup(func() {
		readClaudeAuthStatus = prev
	})
}

func TestDriverStartSessionFailsFastWhenClaudeAuthMissing(t *testing.T) {
	var launched bool
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return nil, nil, nil
	})
	overrideClaudeAuthStatus(t, func(context.Context, string, string, cliLaunchConfig) (claudeAuthStatus, string, error) {
		return claudeAuthStatus{LoggedIn: false, AuthMethod: "none", APIProvider: "firstParty"}, `{"loggedIn":false}`, nil
	})

	d := newDriver(nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)
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
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return next.tr, nil, nil
	})
	overrideClaudeAuthStatus(t, func(_ context.Context, _ string, _ string, cfg cliLaunchConfig) (claudeAuthStatus, string, error) {
		preflightHome = cfg.ClaudeHome
		return claudeAuthStatus{LoggedIn: true, AuthMethod: "oauth_token", APIProvider: "firstParty"}, `{"loggedIn":true}`, nil
	})

	claudeHome := t.TempDir()
	d := newDriver(nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)
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

func TestDriverStartSessionContinuesWhenClaudeAuthStatusFails(t *testing.T) {
	var launched bool
	next := newBufferedTransport(t, "provider-thread-auth-status-error")
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, nil, nil
	})
	overrideClaudeAuthStatus(t, func(context.Context, string, string, cliLaunchConfig) (claudeAuthStatus, string, error) {
		return claudeAuthStatus{}, "unsupported auth status command", errors.New("exit status 1")
	})

	d := newDriver(nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)
	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-auth-status-error",
		CWD:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v, want launch to continue after auth status failure", err)
	}
	if !launched {
		t.Fatal("launchCLI was not called after inconclusive auth status failure")
	}
}

func TestDriverStartSessionContinuesWhenClaudeAuthStatusIsInconclusive(t *testing.T) {
	var launched bool
	next := newBufferedTransport(t, "provider-thread-auth-status-inconclusive")
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched = true
		return next.tr, nil, nil
	})
	overrideClaudeAuthStatus(t, func(context.Context, string, string, cliLaunchConfig) (claudeAuthStatus, string, error) {
		return claudeAuthStatus{}, `{}`, nil
	})

	d := newDriver(nil, nil, nil, nil, nil, &recordingMirrorReconciler{}, nil).(*driver)
	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-auth-status-inconclusive",
		CWD:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v, want launch to continue after inconclusive auth status", err)
	}
	if !launched {
		t.Fatal("launchCLI was not called after inconclusive auth status")
	}
}
