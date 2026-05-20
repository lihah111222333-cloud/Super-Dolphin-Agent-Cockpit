package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools/modelregistry"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
)

type stubBootstrapClient struct {
	startErr     error
	subscribeErr error
	started      chan struct{}
	subscribed   chan struct{}

	startCalls     int
	closeCalls     int
	subscribeCalls int
	relayCalls     int

	subscriptionID string
	topics         []string
	mode           string
}

func (s *stubBootstrapClient) InstallLogRelay() {
	s.relayCalls++
}

func (s *stubBootstrapClient) Start(context.Context) error {
	s.startCalls++
	if s.started != nil {
		select {
		case <-s.started:
		default:
			close(s.started)
		}
	}
	return s.startErr
}

func (s *stubBootstrapClient) Close() error {
	s.closeCalls++
	return nil
}

func (s *stubBootstrapClient) SubscribeHooks(_ context.Context, subscriptionID string, topics []string, _ mcp.Selector, _ json.RawMessage, mode string) (*mcp.HookSubscribeResponse, error) {
	s.subscribeCalls++
	s.subscriptionID = subscriptionID
	s.topics = append([]string(nil), topics...)
	s.mode = mode
	if s.subscribed != nil {
		select {
		case <-s.subscribed:
		default:
			close(s.subscribed)
		}
	}
	return &mcp.HookSubscribeResponse{}, s.subscribeErr
}

func TestBootstrapRunnerSkipsStartWhenRPCAddrMissing(t *testing.T) {
	t.Parallel()

	client := &stubBootstrapClient{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- bootstrapRunner{client: client}.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after cancel")
	}

	if client.startCalls != 0 {
		t.Fatalf("Start() calls = %d, want 0", client.startCalls)
	}
	if client.subscribeCalls != 0 {
		t.Fatalf("SubscribeHooks() calls = %d, want 0", client.subscribeCalls)
	}
	if client.closeCalls != 0 {
		t.Fatalf("Close() calls = %d, want 0", client.closeCalls)
	}
}

func TestBootstrapRunnerStartsAndSubscribesWhenRPCAddrPresent(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")

	client := &stubBootstrapClient{
		started:    make(chan struct{}),
		subscribed: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- bootstrapRunner{
			cfg:    bootstrap.Config{RPCAddr: "127.0.0.1:9123", BinaryName: "mcp-orch"},
			client: client,
		}.Run(ctx)
	}()

	waitForBootstrapSignal(t, client.started, "Start() was not called")
	waitForBootstrapSignal(t, client.subscribed, "SubscribeHooks() was not called")

	cancel()
	waitForBootstrapRunDone(t, done)
	assertBootstrapClientStartState(t, client)
	assertBootstrapSubscription(t, client)
}

func TestBootstrapRunnerFailsWhenHookSubscriptionFails(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")

	subscribeErr := errors.New("subscribe failed")
	client := &stubBootstrapClient{subscribeErr: subscribeErr}

	err := bootstrapRunner{
		cfg:    bootstrap.Config{RPCAddr: "127.0.0.1:9123", BinaryName: "mcp-orch"},
		client: client,
	}.Run(context.Background())
	if !errors.Is(err, subscribeErr) {
		t.Fatalf("Run() error = %v, want subscribe failed", err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1 after hook subscribe failure", client.closeCalls)
	}
}

func waitForBootstrapSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func waitForBootstrapRunDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after cancel")
	}
}

func assertBootstrapClientStartState(t *testing.T, client *stubBootstrapClient) {
	t.Helper()
	if client.startCalls != 1 {
		t.Fatalf("Start() calls = %d, want 1", client.startCalls)
	}
	if client.subscribeCalls != 1 {
		t.Fatalf("SubscribeHooks() calls = %d, want 1", client.subscribeCalls)
	}
	if client.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", client.closeCalls)
	}
}

func assertBootstrapSubscription(t *testing.T, client *stubBootstrapClient) {
	t.Helper()
	if client.subscriptionID != orchestrationHookSubscriptionID {
		t.Fatalf("subscriptionID = %q, want %q", client.subscriptionID, orchestrationHookSubscriptionID)
	}
	if client.mode != "sync" {
		t.Fatalf("mode = %q, want sync", client.mode)
	}
	if len(client.topics) != len(orchestrationHookTopics) {
		t.Fatalf("topics = %v, want %v", client.topics, orchestrationHookTopics)
	}
	for i, want := range orchestrationHookTopics {
		if client.topics[i] != want {
			t.Fatalf("topics[%d] = %q, want %q", i, client.topics[i], want)
		}
	}
}

func TestNewModelRegistryFailsWhenDefaultRegistryFails(t *testing.T) {
	t.Setenv(modelregistry.EnvRegistryPath, filepath.Join(t.TempDir(), "missing.yaml"))
	var logs bytes.Buffer

	registry, err := newModelRegistry(slog.New(slog.NewTextHandler(&logs, nil)))
	if err == nil {
		t.Fatalf("newModelRegistry() error = nil, registry = %#v", registry)
	}
	if !strings.Contains(err.Error(), "model registry load failed") {
		t.Fatalf("newModelRegistry() error = %v, want model registry load failed", err)
	}
	if logs.String() != "" {
		t.Fatalf("logs = %q, want no fallback warning", logs.String())
	}
}
