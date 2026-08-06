package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestNewHTTPRunnerPeerModeRequiresSessionToken(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "")

	runner := newHTTPRunner(tools.Registry{}, pkglogger.NewRuntime(pkglogger.RuntimeConfig{}))
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

	runner := newHTTPRunner(tools.Registry{}, pkglogger.NewRuntime(pkglogger.RuntimeConfig{}))
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

	runner := newHTTPRunner(tools.Registry{}, pkglogger.NewRuntime(pkglogger.RuntimeConfig{}))
	httpRunner, ok := runner.(*httpRunner)
	if !ok {
		t.Fatalf("newHTTPRunner() = %T, want *httpRunner", runner)
	}
	if httpRunner.bearerToken != "legacy-secret" {
		t.Fatalf("bearerToken = %q, want trimmed legacy session token", httpRunner.bearerToken)
	}
}

func TestHTTPRunnerDiscoveryWriteFailureStopsServerAndJoinsErrors(t *testing.T) {
	discoveryErr := errors.New("write discovery")
	stopErr := errors.New("stop server")
	stopCalled := false
	runner := &httpRunner{
		bearerToken: "token",
		startServer: func(context.Context) (string, func(context.Context) error, error) {
			return "127.0.0.1:1234", func(ctx context.Context) error {
				stopCalled = true
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("stop context has no bounded deadline")
				}
				return stopErr
			}, nil
		},
		writePeerDiscovery: func(string, string) error { return discoveryErr },
		cleanupPeerDiscovery: func(string) error {
			t.Fatal("cleanup must not run after discovery write failure")
			return nil
		},
	}

	err := runner.Run(context.Background())
	if !errors.Is(err, discoveryErr) || !errors.Is(err, stopErr) {
		t.Fatalf("Run() error = %v, want joined discovery and stop errors", err)
	}
	if !stopCalled {
		t.Fatal("Run() did not stop server after discovery write failure")
	}
}

func TestHTTPRunnerNormalExitJoinsCleanupAndStopErrors(t *testing.T) {
	cleanupErr := errors.New("cleanup discovery")
	stopErr := errors.New("stop server")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &httpRunner{
		bearerToken: "token",
		startServer: func(context.Context) (string, func(context.Context) error, error) {
			return "127.0.0.1:1234", func(stopCtx context.Context) error {
				if _, ok := stopCtx.Deadline(); !ok {
					t.Fatal("stop context has no bounded deadline")
				}
				return stopErr
			}, nil
		},
		writePeerDiscovery:   func(string, string) error { return nil },
		cleanupPeerDiscovery: func(string) error { return cleanupErr },
	}

	started := time.Now()
	err := runner.Run(ctx)
	if time.Since(started) > time.Second {
		t.Fatal("Run() did not observe canceled runtime context")
	}
	if !errors.Is(err, cleanupErr) || !errors.Is(err, stopErr) {
		t.Fatalf("Run() error = %v, want joined cleanup and stop errors", err)
	}
}
