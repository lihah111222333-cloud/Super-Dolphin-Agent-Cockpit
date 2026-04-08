package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

	subscriptionID string
	topics         []string
	mode           string
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
	t.Parallel()

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

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("Start() was not called")
	}

	select {
	case <-client.subscribed:
	case <-time.After(time.Second):
		t.Fatal("SubscribeHooks() was not called")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after cancel")
	}

	if client.startCalls != 1 {
		t.Fatalf("Start() calls = %d, want 1", client.startCalls)
	}
	if client.subscribeCalls != 1 {
		t.Fatalf("SubscribeHooks() calls = %d, want 1", client.subscribeCalls)
	}
	if client.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", client.closeCalls)
	}
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
