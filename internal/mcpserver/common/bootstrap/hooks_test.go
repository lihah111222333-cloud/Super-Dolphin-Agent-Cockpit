package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestResolveHook_CallsHookResolveRPC(t *testing.T) {
	t.Parallel()

	var captured mcpdto.HookResolveRequest
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHookResolve: platformrpc.StrictHandler(func(_ context.Context, req mcpdto.HookResolveRequest) (mcpdto.HookResolveResponse, error) {
			captured = req
			return mcpdto.HookResolveResponse{
				Accepted:          true,
				ResolvedAt:        "2026-03-24T00:00:00Z",
				CanonicalDecision: mcpdto.HookDecisionApprove,
				PendingState:      "resolved",
			}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{conn: local.Client}
	req := mcpdto.HookResolveRequest{
		HookCallID:     "hook-1",
		Decision:       mcpdto.HookDecisionApprove,
		Reason:         "approved",
		IdempotencyKey: "idem-1",
		ResolvedBy:     "bootstrap-reviewer",
	}
	resp, err := client.ResolveHook(context.Background(), req)
	if err != nil {
		t.Fatalf("ResolveHook() error = %v", err)
	}
	if captured != req {
		t.Fatalf("ResolveHook() request = %#v, want %#v", captured, req)
	}
	if resp == nil || !resp.Accepted {
		t.Fatalf("ResolveHook() response = %#v, want accepted response", resp)
	}
}

func TestSubscribeHooks_PassesFiltersAndReplayUsesStoredFilters(t *testing.T) {
	t.Parallel()

	var captured []mcpdto.HookSubscribeRequest
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHookSubscribe: platformrpc.StrictHandler(func(_ context.Context, req mcpdto.HookSubscribeRequest) (mcpdto.HookSubscribeResponse, error) {
			captured = append(captured, req)
			return mcpdto.HookSubscribeResponse{
				Accepted:            true,
				SubscriptionVersion: int64(len(captured)),
				EffectiveTopics:     append([]string(nil), req.Topics...),
				EffectiveScope:      req.Scope,
			}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	scope := mcpdto.Selector{Subscription: "agent.turn.before"}
	filters := json.RawMessage(`{"rule":"only-before"}`)
	client := &Client{conn: local.Client}

	if _, err := client.SubscribeHooks(context.Background(), "sub-1", []string{"agent.turn.before"}, scope, filters, "sync"); err != nil {
		t.Fatalf("SubscribeHooks() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured subscribe count = %d, want 1", len(captured))
	}
	if !bytes.Equal(captured[0].Filters, filters) {
		t.Fatalf("SubscribeHooks() filters = %s, want %s", captured[0].Filters, filters)
	}

	client.replayHookSubscriptions(context.Background())
	if len(captured) != 2 {
		t.Fatalf("captured replay count = %d, want 2", len(captured))
	}
	if !bytes.Equal(captured[1].Filters, filters) {
		t.Fatalf("replay filters = %s, want %s", captured[1].Filters, filters)
	}
}

func TestReplayHookSubscriptions_RetriesWithBackoff(t *testing.T) {
	t.Parallel()

	attempts := 0
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHookSubscribe: platformrpc.StrictHandler(func(_ context.Context, req mcpdto.HookSubscribeRequest) (mcpdto.HookSubscribeResponse, error) {
			attempts++
			if attempts < 3 {
				return mcpdto.HookSubscribeResponse{}, errors.New("transient replay failure")
			}
			return mcpdto.HookSubscribeResponse{
				Accepted:            true,
				SubscriptionVersion: int64(attempts),
				EffectiveTopics:     append([]string(nil), req.Topics...),
				EffectiveScope:      req.Scope,
			}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{
		conn:       local.Client,
		instanceID: "instance-replay",
	}
	scope := mcpdto.Selector{Subscription: "agent.turn.before"}
	filters := json.RawMessage(`{"rule":"retry"}`)
	client.hooks.store("sub-replay", []string{"agent.turn.before"}, scope, filters, "sync")

	if err := client.replayHookSubscriptions(context.Background()); err != nil {
		t.Fatalf("replayHookSubscriptions() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("replayHookSubscriptions() attempts = %d, want 3", attempts)
	}
	if client.hooks.replayState != "" {
		t.Fatalf("replay state = %q, want cleared state", client.hooks.replayState)
	}
	if client.hooks.replayAttempts != 0 {
		t.Fatalf("replay attempts state = %d, want 0", client.hooks.replayAttempts)
	}
	if client.hooks.lastReplayErr != "" {
		t.Fatalf("last replay err = %q, want cleared error", client.hooks.lastReplayErr)
	}
}

func TestPendingHooks_Success(t *testing.T) {
	t.Parallel()

	reviews := []mcpdto.PendingHookReview{
		{
			HookCallID:      "hook-1",
			Topic:           "agent.turn.after",
			AgentID:         "agent-1",
			SubscriberLease: "instance-1/1",
			CreatedAt:       time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC),
			DeadlineAt:      time.Date(2026, 3, 24, 12, 5, 0, 0, time.UTC),
			DefaultAction:   mcpdto.HookDecisionReject,
		},
	}
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHookPending: platformrpc.StrictHandler(func(_ context.Context, req mcpdto.HookPendingRequest) (mcpdto.HookPendingResponse, error) {
			if req.AgentID != "agent-1" {
				t.Fatalf("hook pending agent_id = %q, want agent-1", req.AgentID)
			}
			return mcpdto.HookPendingResponse{Reviews: reviews}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{
		conn: local.Client,
		cfg:  Config{AgentID: "agent-1"},
	}
	got, err := client.PendingHooks(context.Background())
	if err != nil {
		t.Fatalf("PendingHooks() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("PendingHooks() len = %d, want 1", len(got))
	}
	if got[0].HookCallID != reviews[0].HookCallID {
		t.Fatalf("PendingHooks() hook_call_id = %q, want %q", got[0].HookCallID, reviews[0].HookCallID)
	}
	if got[0].SubscriberLease != reviews[0].SubscriberLease {
		t.Fatalf("PendingHooks() subscriber_lease = %q, want %q", got[0].SubscriberLease, reviews[0].SubscriberLease)
	}
}

func TestPendingHooks_NoPending(t *testing.T) {
	t.Parallel()

	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHookPending: platformrpc.StrictHandler(func(_ context.Context, req mcpdto.HookPendingRequest) (mcpdto.HookPendingResponse, error) {
			if req.AgentID != "agent-2" {
				t.Fatalf("hook pending agent_id = %q, want agent-2", req.AgentID)
			}
			return mcpdto.HookPendingResponse{Reviews: []mcpdto.PendingHookReview{}}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	// PendingHooks 只信任 cfg.AgentID，不再读取 boot.AgentID。
	// 这样缺少权威身份时会 fail-closed，避免用启动负载里的旧值冒充当前 agent。
	client := &Client{
		conn: local.Client,
		cfg:  Config{AgentID: "agent-2"},
	}
	got, err := client.PendingHooks(context.Background())
	if err != nil {
		t.Fatalf("PendingHooks() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("PendingHooks() len = %d, want 0", len(got))
	}
	if got == nil {
		t.Fatal("PendingHooks() = nil, want empty slice")
	}
}

func TestPendingHooks_RequiresAgentIDHint(t *testing.T) {
	t.Parallel()

	client := &Client{conn: &jrpc2.Client{}}
	_, err := client.PendingHooks(context.Background())
	if err == nil || err.Error() != "bootstrap: hook pending requires agent_id" {
		t.Fatalf("PendingHooks() error = %v, want agent_id required", err)
	}
}
