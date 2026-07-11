package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformhooks "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/hooks"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"

	jrpcserver "github.com/creachadair/jrpc2/server"
)

var _ contract.HookReviewStore = (*integrationHookReviewStore)(nil)

type integrationResolveCall struct {
	hookCallID     string
	decision       string
	reason         string
	idempotencyKey string
	resolvedBy     string
}

type integrationResolvedReview struct {
	decision        string
	resolvedAt      time.Time
	subscriberLease string
}

type integrationHookReviewStore struct {
	mu           sync.Mutex
	pending      map[string]dto.PendingHookReview
	saved        []dto.PendingHookReview
	resolveCalls []integrationResolveCall
	resolved     map[string]integrationResolvedReview
}

func (s *integrationHookReviewStore) SavePendingReview(_ context.Context, review dto.PendingHookReview) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = make(map[string]dto.PendingHookReview)
	}
	s.saved = append(s.saved, review)
	s.pending[review.HookCallID] = review
	return nil
}

func (s *integrationHookReviewStore) GetPendingReview(_ context.Context, hookCallID string) (dto.PendingHookReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	review, ok := s.pending[hookCallID]
	if !ok {
		return dto.PendingHookReview{}, contract.ErrHookReviewNotFound
	}
	return review, nil
}

func (s *integrationHookReviewStore) ListPendingReviews(_ context.Context, agentID string) ([]dto.PendingHookReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reviews := make([]dto.PendingHookReview, 0, len(s.pending))
	for _, review := range s.pending {
		if review.AgentID == agentID {
			reviews = append(reviews, review)
		}
	}
	slices.SortFunc(reviews, func(a, b dto.PendingHookReview) int {
		switch {
		case a.HookCallID < b.HookCallID:
			return -1
		case a.HookCallID > b.HookCallID:
			return 1
		default:
			return 0
		}
	})
	return reviews, nil
}

func (s *integrationHookReviewStore) ResolvePendingReview(_ context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	review, ok := s.pending[hookCallID]
	if !ok {
		return errors.New("pending review not found")
	}
	delete(s.pending, hookCallID)
	if s.resolved == nil {
		s.resolved = make(map[string]integrationResolvedReview)
	}
	resolvedAt := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	s.resolveCalls = append(s.resolveCalls, integrationResolveCall{
		hookCallID:     hookCallID,
		decision:       decision,
		reason:         reason,
		idempotencyKey: idempotencyKey,
		resolvedBy:     resolvedBy,
	})
	s.resolved[hookCallID] = integrationResolvedReview{
		decision:        decision,
		resolvedAt:      resolvedAt,
		subscriberLease: review.SubscriberLease,
	}
	return nil
}

func (s *integrationHookReviewStore) CancelPendingReviewsByLease(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *integrationHookReviewStore) CancelPendingReviewsByAgent(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *integrationHookReviewStore) CancelExpiredReviews(_ context.Context) (int, error) {
	return 0, nil
}

func (s *integrationHookReviewStore) RecoverOnStartup(_ context.Context) ([]dto.PendingHookReview, error) {
	return nil, nil
}

func (s *integrationHookReviewStore) GetResolvedReview(_ context.Context, hookCallID string) (string, time.Time, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	review, ok := s.resolved[hookCallID]
	if !ok {
		return "", time.Time{}, "", contract.ErrHookReviewNotFound
	}
	return review.decision, review.resolvedAt, review.subscriberLease, nil
}

type hookHandlerHarness struct {
	controlRegistry *ToolRegistry
	hookRegistry    *platformhooks.HookRegistry
	store           *integrationHookReviewStore
	local           jrpcserver.Local
	lease           dto.LeaseKey
}

func newHookHandlerHarness(t *testing.T) *hookHandlerHarness {
	t.Helper()

	controlRegistry := NewRegistry()
	hookRegistry := platformhooks.NewHookRegistry()
	store := &integrationHookReviewStore{
		pending:  make(map[string]dto.PendingHookReview),
		resolved: make(map[string]integrationResolvedReview),
	}
	dispatcher, err := platformhooks.NewHookDispatcher(hookRegistry, controlRegistry, platformhooks.WithDispatcherParallelism(1))
	if err != nil {
		t.Fatalf("NewHookDispatcher() error = %v", err)
	}
	resolver, err := platformhooks.NewHookResolver(store)
	if err != nil {
		t.Fatalf("NewHookResolver() error = %v", err)
	}
	manager, err := platformhooks.NewManager(hookRegistry, dispatcher, resolver)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	local := jrpcserver.NewLocal(NewHandlers(HandlerDeps{
		Registry:    controlRegistry,
		HookManager: manager,
	}).Handlers, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	t.Cleanup(func() {
		local.Close()
	})

	var regResp dto.RegisterResponse
	err = local.Client.CallResult(context.Background(), dto.MethodRegister, dto.RegisterRequest{
		InstanceID:          "instance-hook",
		BinaryName:          "mcp-orch",
		AgentID:             "agent-hook",
		ThreadID:            "thread-hook",
		ClientKind:          dto.ClientKindOrch,
		PeerKind:            dto.PeerKindTool,
		PID:                 1234,
		CapabilitiesOffered: []string{"hooks"},
	}, &regResp)
	if err != nil {
		t.Fatalf("CallResult(register) error = %v", err)
	}

	return &hookHandlerHarness{
		controlRegistry: controlRegistry,
		hookRegistry:    hookRegistry,
		store:           store,
		local:           local,
		lease:           dto.LeaseKey{InstanceID: regResp.InstanceID, Generation: regResp.Generation},
	}
}

func TestNewHandlers_HookSubscribe_Integration(t *testing.T) {
	t.Parallel()

	harness := newHookHandlerHarness(t)

	var resp dto.HookSubscribeResponse
	err := harness.local.Client.CallResult(context.Background(), dto.MethodHookSubscribe, dto.HookSubscribeRequest{
		SubscriptionID: "sub-hook",
		Topics:         []string{" topic.before ", "topic.before"},
		Scope:          dto.Selector{Scope: &dto.SelectorScope{AgentID: "agent-hook"}},
		Mode:           " sync ",
	}, &resp)
	if err != nil {
		t.Fatalf("CallResult(hook/subscribe) error = %v", err)
	}
	if !resp.Accepted {
		t.Fatal("HookSubscribeResponse.Accepted = false, want true")
	}
	subscription, ok := harness.hookRegistry.GetSubscription(harness.lease)
	if !ok {
		t.Fatal("GetSubscription() ok = false, want true")
	}
	if subscription.SubscriptionID != "sub-hook" {
		t.Fatalf("subscription id = %q, want %q", subscription.SubscriptionID, "sub-hook")
	}
	if !slices.Equal(subscription.Topics, []string{"topic.before"}) {
		t.Fatalf("subscription topics = %#v, want %#v", subscription.Topics, []string{"topic.before"})
	}
	if subscription.Scope.Scope == nil || subscription.Scope.Scope.AgentID != "agent-hook" {
		t.Fatalf("subscription scope = %#v, want agent-hook", subscription.Scope.Scope)
	}
}

func TestNewHandlers_HookResolve_Integration(t *testing.T) {
	t.Parallel()

	harness := newHookHandlerHarness(t)
	harness.store.pending["call-resolve"] = dto.PendingHookReview{
		HookCallID:      "call-resolve",
		Topic:           "tool.after",
		AgentID:         "agent-hook",
		SubscriberLease: "instance-hook/1",
		CreatedAt:       time.Date(2026, 3, 24, 11, 0, 0, 0, time.UTC),
		DeadlineAt:      time.Date(2026, 3, 24, 11, 30, 0, 0, time.UTC),
		DefaultAction:   dto.HookDecisionReject,
	}

	var resp dto.HookResolveResponse
	err := harness.local.Client.CallResult(context.Background(), dto.MethodHookResolve, dto.HookResolveRequest{
		HookCallID:     "call-resolve",
		Decision:       dto.HookDecisionApprove,
		Reason:         "looks good",
		IdempotencyKey: "idem-resolve",
		ResolvedBy:     "reviewer-handler",
	}, &resp)
	if err != nil {
		t.Fatalf("CallResult(hook/resolve) error = %v", err)
	}
	if !resp.Accepted {
		t.Fatal("HookResolveResponse.Accepted = false, want true")
	}
	if resp.CanonicalDecision != dto.HookDecisionApprove {
		t.Fatalf("canonical decision = %q, want %q", resp.CanonicalDecision, dto.HookDecisionApprove)
	}
	if len(harness.store.resolveCalls) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(harness.store.resolveCalls))
	}
	call := harness.store.resolveCalls[0]
	if call.hookCallID != "call-resolve" {
		t.Fatalf("hookCallID = %q, want %q", call.hookCallID, "call-resolve")
	}
	if call.decision != dto.HookDecisionApprove {
		t.Fatalf("decision = %q, want %q", call.decision, dto.HookDecisionApprove)
	}
	if call.reason != "looks good" {
		t.Fatalf("reason = %q, want %q", call.reason, "looks good")
	}
	if call.idempotencyKey != "idem-resolve" {
		t.Fatalf("idempotencyKey = %q, want %q", call.idempotencyKey, "idem-resolve")
	}
	if call.resolvedBy != "reviewer-handler" {
		t.Fatalf("resolvedBy = %q, want %q", call.resolvedBy, "reviewer-handler")
	}
}

func TestNewHandlers_HookMethods_InvalidParams(t *testing.T) {
	t.Parallel()

	harness := newHookHandlerHarness(t)

	cases := []struct {
		name   string
		method string
		req    any
	}{
		{
			name:   "subscribe_missing_subscription_id",
			method: dto.MethodHookSubscribe,
			req:    dto.HookSubscribeRequest{Topics: []string{"topic.before"}},
		},
		{
			name:   "subscribe_missing_topics",
			method: dto.MethodHookSubscribe,
			req:    dto.HookSubscribeRequest{SubscriptionID: "sub-hook"},
		},
		{
			name:   "resolve_missing_hook_call_id",
			method: dto.MethodHookResolve,
			req: dto.HookResolveRequest{
				Decision:       dto.HookDecisionApprove,
				IdempotencyKey: "idem-resolve",
			},
		},
		{
			name:   "resolve_missing_idempotency_key",
			method: dto.MethodHookResolve,
			req: dto.HookResolveRequest{
				HookCallID: "call-resolve",
				Decision:   dto.HookDecisionApprove,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var raw json.RawMessage
			err := harness.local.Client.CallResult(context.Background(), tc.method, tc.req, &raw)
			if err == nil {
				t.Fatal("CallResult() error = nil, want invalid params")
			}
			var rpcErr *jrpc2.Error
			if !errors.As(err, &rpcErr) {
				t.Fatalf("CallResult() error = %T, want *jrpc2.Error", err)
			}
			if got := int(rpcErr.Code); got != platformrpc.CodeInvalidParams {
				t.Fatalf("CallResult() code = %d, want %d", got, platformrpc.CodeInvalidParams)
			}

		})
	}
}
