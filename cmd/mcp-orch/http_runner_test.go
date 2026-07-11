package main

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
)

func TestNewHTTPRunnerPeerModeRequiresSessionToken(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "")

	runner := newHTTPRunner(tools.Registry{})
	httpRunner, ok := runner.(*httpRunner)
	if !ok {
		t.Fatalf("newHTTPRunner() = %T, want *httpRunner", runner)
	}
	if httpRunner.bearerToken != "" {
		t.Fatalf("bearerToken = %q, want empty before fail-fast Run", httpRunner.bearerToken)
	}

	err := httpRunner.Run(context.Background())
	if !errors.Is(err, errOrchHTTPSessionTokenRequired) {
		t.Fatalf("Run() error = %v, want errOrchHTTPSessionTokenRequired", err)
	}
}

func TestNewHTTPRunnerPeerModeCarriesSessionToken(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", " secret ")

	runner := newHTTPRunner(tools.Registry{})
	httpRunner, ok := runner.(*httpRunner)
	if !ok {
		t.Fatalf("newHTTPRunner() = %T, want *httpRunner", runner)
	}
	if httpRunner.bearerToken != "secret" {
		t.Fatalf("bearerToken = %q, want trimmed session token", httpRunner.bearerToken)
	}
}

func TestNewHTTPRunnerPeerModeCarriesLegacySessionToken(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "")
	t.Setenv("GO_AGENT_MCP_SESSION_TOKEN", " legacy-secret ")

	runner := newHTTPRunner(tools.Registry{})
	httpRunner, ok := runner.(*httpRunner)
	if !ok {
		t.Fatalf("newHTTPRunner() = %T, want *httpRunner", runner)
	}
	if httpRunner.bearerToken != "legacy-secret" {
		t.Fatalf("bearerToken = %q, want trimmed legacy session token", httpRunner.bearerToken)
	}
}
