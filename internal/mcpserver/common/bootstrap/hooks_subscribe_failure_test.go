package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
)

// TestSubscribeHooks_PersistsDesiredStateOnLiveCallFailure 验证实时 SubscribeHooks 失败时仍保存期望订阅状态。
// 断线重连会依赖这份状态重放订阅；若这里丢失参数，后续只能靠人工重新订阅。
func TestSubscribeHooks_PersistsDesiredStateOnLiveCallFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("subscribe boom")
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHookSubscribe: platformrpc.StrictHandler(func(_ context.Context, _ mcpdto.HookSubscribeRequest) (mcpdto.HookSubscribeResponse, error) {
			return mcpdto.HookSubscribeResponse{}, boom
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	scope := mcpdto.Selector{Subscription: "agent.turn.before"}
	filters := json.RawMessage(`{"rule":"only-before"}`)
	topics := []string{"agent.turn.before"}
	client := &Client{conn: local.Client}

	_, err := client.SubscribeHooks(context.Background(), "sub-fail-1", topics, scope, filters, "sync")
	if err == nil {
		t.Fatalf("SubscribeHooks() returned nil err, want propagation of peer failure")
	}

	assertSubscribeHooksState(t, client, scope, filters)
	assertSubscribeHooksReplayPending(t, client)
}

func assertSubscribeHooksState(t *testing.T, client *Client, scope mcpdto.Selector, filters json.RawMessage) {
	t.Helper()
	subID, storedTopics, storedScope, storedFilters, mode, ok := client.hooks.load()
	if !ok {
		t.Fatalf("hooks.load() after live-call failure = not stored; expected desired state to persist for replay")
	}
	if subID != "sub-fail-1" {
		t.Fatalf("stored subscription id = %q, want %q", subID, "sub-fail-1")
	}
	if len(storedTopics) != 1 || storedTopics[0] != "agent.turn.before" {
		t.Fatalf("stored topics = %#v, want [agent.turn.before]", storedTopics)
	}
	if storedScope.Subscription != scope.Subscription {
		t.Fatalf("stored scope subscription = %q, want %q", storedScope.Subscription, scope.Subscription)
	}
	if !bytes.Equal(storedFilters, filters) {
		t.Fatalf("stored filters = %s, want %s", storedFilters, filters)
	}
	if mode != "sync" {
		t.Fatalf("stored mode = %q, want %q", mode, "sync")
	}
}

func assertSubscribeHooksReplayPending(t *testing.T, client *Client) {
	t.Helper()
	// markReplayPending 是诊断边界：初始调用失败要和重连期间 replay 失败分开呈现。
	client.hooks.mu.Lock()
	state := client.hooks.replayState
	lastErr := client.hooks.lastReplayErr
	client.hooks.mu.Unlock()
	if state != "pending" {
		t.Fatalf("replayState = %q, want %q", state, "pending")
	}
	if lastErr == "" {
		t.Fatalf("lastReplayErr = empty, want propagated peer error")
	}
}
