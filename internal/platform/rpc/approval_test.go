package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestRegisterPendingKeepsCompleteIdentitiesDistinct(t *testing.T) {
	manager := NewApprovalManager(nil, nil)

	first, firstOwner := manager.registerPending(testApprovalRequestWithID("test-session", "call-1", 1), nil)
	second, secondOwner := manager.registerPending(testApprovalRequestWithID("test-session", "call-1", 2), nil)

	if !firstOwner || !secondOwner {
		t.Fatalf("registerPending owner flags = %v, %v; want true, true", firstOwner, secondOwner)
	}
	if first == second {
		t.Fatal("registerPending returned the same pending for duplicate callID without requestID")
	}
	if first.requestID == nil || second.requestID == nil {
		t.Fatal("registerPending did not assign request IDs")
	}
	if *first.requestID == *second.requestID {
		t.Fatalf("request IDs collapsed: %d", *first.requestID)
	}
	if first.key == second.key {
		t.Fatalf("pending keys collapsed: %q", first.key)
	}
}

func TestRegisterPendingStoresDispatcherBeforePublish(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	dispatcher := &event.Dispatcher{}

	pending, owner := manager.registerPending(testApprovalRequest("call-1"), dispatcher)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	if pending.dispatcher != dispatcher {
		t.Fatal("registerPending did not retain dispatcher")
	}
}

func TestRegisterPendingFallsBackToManagerDispatcher(t *testing.T) {
	dispatcher := &event.Dispatcher{}
	manager := NewApprovalManager(nil, dispatcher)

	pending, owner := manager.registerPending(testApprovalRequest("call-1"), nil)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	if pending.dispatcher != dispatcher {
		t.Fatal("registerPending did not fall back to manager dispatcher")
	}
}

func TestCleanupPublishesResolvedTimeoutEvent(t *testing.T) {
	dispatcher := event.NewDispatcher()
	manager := NewApprovalManager(nil, dispatcher)
	resolved := make(chan tooldto.ToolApprovalResolved, 1)
	cancel := event.Subscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
		resolved <- ev
	})
	defer cancel()

	req := testApprovalRequest("call-1")
	req.AgentID = "agent-1"
	req.TurnID = "turn-1"
	req.Kind = "request_user_input"
	pending, owner := manager.registerPending(req, nil)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	pending.createdAt = time.Now().Add(-time.Minute)

	manager.Cleanup(time.Second)

	event := awaitResolvedEvent(t, resolved)
	if event.CallID != "call-1" {
		t.Fatalf("resolved callID = %q, want %q", event.CallID, "call-1")
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal resolved approval event: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal resolved approval event: %v", err)
	}
	if payload["request_id"] != float64(*pending.requestID) {
		t.Fatalf("resolved request_id = %#v, want %d", payload["request_id"], *pending.requestID)
	}
	if event.Decision != ErrApprovalTimeout("approval timed out").Error() {
		t.Fatalf("resolved decision = %q, want %q", event.Decision, ErrApprovalTimeout("approval timed out").Error())
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("Cleanup left pending approvals behind")
	}
}

func TestRequestApprovalUsesDefaultTimeoutWithCallbackPath(t *testing.T) {
	previous := DefaultApprovalTimeout
	DefaultApprovalTimeout = 25 * time.Millisecond
	defer func() { DefaultApprovalTimeout = previous }()

	manager := NewApprovalManager(nil, nil)
	local, bridge := newBlockingApprovalLocal(t)

	start := time.Now()
	_, err := manager.RequestApproval(context.Background(), bridge, local.Server, testApprovalRequest("call-1"))
	if err == nil {
		t.Fatal("RequestApproval() error = nil, want timeout")
	}
	if !errors.Is(err, ErrApprovalTimeout("approval timed out")) {
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.Code(CodeApprovalTimeout) {
			t.Fatalf("RequestApproval() error = %v, want approval timeout", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("RequestApproval() elapsed = %s, want default timeout to apply quickly in test", elapsed)
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestApproval left pending approvals behind after timeout")
	}
}

func TestRequestApprovalAutoDeclinesWithoutFrontend(t *testing.T) {
	manager := NewApprovalManager(nil, nil)

	decision, err := manager.RequestApproval(context.Background(), nil, nil, testApprovalRequest("call-1"))
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("RequestApproval() approved = %v, want false", decision.Approved)
	}
	if decision.Reason != "decline" {
		t.Fatalf("RequestApproval() reason = %q, want %q", decision.Reason, "decline")
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestApproval left pending approvals behind after auto-decline")
	}
}

func TestRequestApprovalWarnsAndDeclinesOnPartialFrontendConfig(t *testing.T) {
	local, bridge := newBlockingApprovalLocal(t)
	for _, tc := range []struct {
		name   string
		bridge *PushBridge
		server *jrpc2.Server
	}{
		{name: "missing_bridge", bridge: nil, server: local.Server},
		{name: "missing_server", bridge: bridge, server: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logger := pkglogger.New(pkglogger.NewTextHandler(&logBuf, nil))
			manager := NewApprovalManager(logger, nil)

			decision, err := manager.RequestApproval(context.Background(), tc.bridge, tc.server, testApprovalRequest("call-1"))
			if err != nil {
				t.Fatalf("RequestApproval() error = %v", err)
			}
			if decision.Approved == nil || *decision.Approved {
				t.Fatalf("RequestApproval() approved = %v, want false", decision.Approved)
			}
			if !strings.Contains(logBuf.String(), "approval dispatch misconfigured") {
				t.Fatalf("logs = %q, want partial frontend warning", logBuf.String())
			}
		})
	}
}

func TestRequestUserInputAutoApprovesWhenApprovalPolicyNever(t *testing.T) {
	manager := NewApprovalManager(nil, nil)

	req := testApprovalRequest("call-1")
	req.ApprovalPolicy = "never"
	decision, err := manager.RequestUserInput(context.Background(), nil, nil, req)
	if err != nil {
		t.Fatalf("RequestUserInput() error = %v", err)
	}
	if decision.Approved == nil || !*decision.Approved {
		t.Fatalf("RequestUserInput() approved = %v, want true", decision.Approved)
	}
	if decision.Reason != "auto_approved" {
		t.Fatalf("RequestUserInput() reason = %q, want %q", decision.Reason, "auto_approved")
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestUserInput left pending approvals behind after auto-approve")
	}
}

func TestApprovalRequestIgnoresPeerControlledApprovalPolicy(t *testing.T) {
	manager := NewApprovalManager(nil, nil)

	req := testApprovalRequest("call-1")
	req.Payload = map[string]any{"approvalPolicy": "never"}
	decision, err := manager.RequestUserInput(context.Background(), nil, nil, req)
	if err != nil {
		t.Fatalf("RequestUserInput() error = %v", err)
	}
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("RequestUserInput() approved = %v, want false when policy only came from payload", decision.Approved)
	}
	if decision.Reason == "auto_approved" {
		t.Fatalf("RequestUserInput() reason = %q, want non-auto approval decision", decision.Reason)
	}
}

func TestRequestApprovalCanceledContextPublishesResolvedEvent(t *testing.T) {
	dispatcher := event.NewDispatcher()
	manager := NewApprovalManager(nil, dispatcher)
	resolved := make(chan tooldto.ToolApprovalResolved, 1)
	cancelSub := event.Subscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
		resolved <- ev
	})
	defer cancelSub()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	local, bridge := newBlockingApprovalLocal(t)

	req := testApprovalRequest("call-1")
	req.AgentID = "agent-1"
	req.TurnID = "turn-1"
	req.Kind = "request_user_input"
	_, err := manager.RequestApproval(ctx, bridge, local.Server, req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestApproval() error = %v, want %v", err, context.Canceled)
	}

	event := awaitResolvedEvent(t, resolved)
	if event.CallID != "call-1" {
		t.Fatalf("resolved callID = %q, want %q", event.CallID, "call-1")
	}
	if event.Decision != context.Canceled.Error() {
		t.Fatalf("resolved decision = %q, want %q", event.Decision, context.Canceled.Error())
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestApproval left pending approvals behind")
	}
}

func TestRequestApprovalAutoDeclinesWhenFailClosedContextCanceled(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	local, bridge := newBlockingApprovalLocal(t)

	ctx, cancel := context.WithCancel(WithApprovalAutoDeclineOnCancel(context.Background()))
	time.AfterFunc(10*time.Millisecond, cancel)

	decision, err := manager.RequestApproval(ctx, bridge, local.Server, testApprovalRequest("call-1"))
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("RequestApproval() approved = %v, want false", decision.Approved)
	}
	if decision.Reason != "decline" {
		t.Fatalf("RequestApproval() reason = %q, want %q", decision.Reason, "decline")
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestApproval left pending approvals behind after fail-closed cancel")
	}
}

func TestRequestApprovalReplayWaitsForExistingPending(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	requestID := int64(7)
	request := testApprovalRequestWithID("test-session", "call-1", requestID)
	pending, owner := manager.registerPending(request, nil)
	if !owner || pending == nil {
		t.Fatal("registerPending() did not create initial pending")
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(20 * time.Millisecond)
		_ = manager.Respond(contract.ApprovalIdentity{SessionScope: "test-session", CallID: "call-1", RequestID: requestID}, contract.ApprovalDecision{
			Approved: boolPtr(true),
			Reason:   "approved",
		})
	})
	defer wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	decision, err := manager.RequestApproval(ctx, nil, nil, request)
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if decision.Approved == nil || !*decision.Approved {
		t.Fatalf("RequestApproval() approved = %v, want true", decision.Approved)
	}
	if decision.Reason != "approved" {
		t.Fatalf("RequestApproval() reason = %q, want %q", decision.Reason, "approved")
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestApproval left replayed pending approval behind")
	}
}

func TestApprovalRespondIsIdempotentForCompletedRequest(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	requestID := int64(9)
	request := testApprovalRequestWithID("test-session", "call-1", requestID)
	pending, owner := manager.registerPending(request, nil)
	if !owner || pending == nil {
		t.Fatal("registerPending() did not create initial pending")
	}

	decision := contract.ApprovalDecision{
		Approved: boolPtr(true),
		Reason:   "approved",
	}
	identity := contract.ApprovalIdentity{SessionScope: "test-session", CallID: "call-1", RequestID: requestID}
	if err := manager.Respond(identity, decision); err != nil {
		t.Fatalf("first Respond() error = %v", err)
	}
	if err := manager.Respond(identity, decision); err != nil {
		t.Fatalf("retry Respond() error = %v, want idempotent success", err)
	}
}

func TestApprovalConcurrentRespondsReportOnlyWinningDecisionAsSuccess(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	pending, owner := manager.registerPending(testApprovalRequestWithID("test-session", "call-race", 11), nil)
	if !owner || pending == nil {
		t.Fatal("registerPending() did not create initial pending")
	}

	approved := contract.ApprovalDecision{Approved: boolPtr(true), Reason: "approved"}
	rejected := contract.ApprovalDecision{Approved: boolPtr(false), Reason: "rejected"}
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	var workers sync.WaitGroup
	ready.Add(2)
	for _, decision := range []contract.ApprovalDecision{approved, rejected} {
		workers.Go(func() {
			ready.Done()
			<-start
			results <- manager.respondPending(pending, decision)
		})
	}
	ready.Wait()
	close(start)
	workers.Wait()

	errorsSeen := []error{<-results, <-results}
	successes := 0
	conflicts := 0
	wantConflict := ErrInvalidState("approval already resolved with a different decision").Error()
	for _, err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case err.Error() == wantConflict:
			conflicts++
		default:
			t.Fatalf("respondPending() error = %v, want nil or %q", err, wantConflict)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results = %v; successes = %d, conflicts = %d, want 1 and 1", errorsSeen, successes, conflicts)
	}
}

func TestApprovalRespondPendingPropagatesWinningTimeout(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	pending, owner := manager.registerPending(testApprovalRequestWithID("test-session", "call-timeout-race", 12), nil)
	if !owner || pending == nil {
		t.Fatal("registerPending() did not create initial pending")
	}

	ready := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	var worker sync.WaitGroup
	worker.Go(func() {
		close(ready)
		<-release
		result <- manager.respondPending(pending, contract.ApprovalDecision{Approved: boolPtr(true), Reason: "approved"})
	})
	<-ready
	timeoutErr := ErrApprovalTimeout("approval timed out")
	manager.failPending(pending, timeoutErr)
	close(release)
	worker.Wait()

	if err := <-result; err == nil || err.Error() != timeoutErr.Error() {
		t.Fatalf("respondPending() error = %v, want winning timeout %q", err, timeoutErr)
	}
}

func TestApprovalIdentitySeparatesSameRequestIDAcrossSessionsAndCalls(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	requestID := int64(41)
	firstIdentity := contract.ApprovalIdentity{SessionScope: "session-scope-a", CallID: "call-a", RequestID: requestID}
	secondIdentity := contract.ApprovalIdentity{SessionScope: "session-scope-b", CallID: "call-b", RequestID: requestID}
	first, firstOwner := manager.registerPending(ApprovalRequest{SessionScope: firstIdentity.SessionScope, CallID: firstIdentity.CallID, RequestID: &requestID}, nil)
	second, secondOwner := manager.registerPending(ApprovalRequest{SessionScope: secondIdentity.SessionScope, CallID: secondIdentity.CallID, RequestID: &requestID}, nil)
	if !firstOwner || !secondOwner || first == second {
		t.Fatalf("registerPending() owners = %v, %v, same = %v; want true, true, false", firstOwner, secondOwner, first == second)
	}
	if got := len(manager.PendingSnapshot()); got != 2 {
		t.Fatalf("pending approvals = %d, want 2", got)
	}
	decision := contract.ApprovalDecision{Approved: boolPtr(true), Reason: "approved"}
	if err := manager.Respond(firstIdentity, decision); err != nil {
		t.Fatalf("Respond(first) error = %v", err)
	}
	if got := len(manager.PendingSnapshot()); got != 1 {
		t.Fatalf("pending approvals after first response = %d, want 1", got)
	}
	select {
	case <-second.done:
		t.Fatal("responding to first identity completed the second session approval")
	default:
	}
	if err := manager.Respond(secondIdentity, decision); err != nil {
		t.Fatalf("Respond(second) error = %v", err)
	}
}

func TestApprovalRespondFailsClosedForIncompleteIdentity(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	requestID := int64(51)
	identity := contract.ApprovalIdentity{SessionScope: "session-scope", CallID: "call-1", RequestID: requestID}
	if _, owner := manager.registerPending(ApprovalRequest{SessionScope: identity.SessionScope, CallID: identity.CallID, RequestID: &requestID}, nil); !owner {
		t.Fatal("registerPending() owner = false, want true")
	}
	for _, incomplete := range []contract.ApprovalIdentity{
		{CallID: identity.CallID, RequestID: identity.RequestID},
		{SessionScope: identity.SessionScope, RequestID: identity.RequestID},
		{SessionScope: identity.SessionScope, CallID: identity.CallID},
	} {
		if err := manager.Respond(incomplete, contract.ApprovalDecision{Approved: boolPtr(true)}); err == nil {
			t.Fatalf("Respond(%+v) error = nil, want fail-closed error", incomplete)
		}
	}
	if got := len(manager.PendingSnapshot()); got != 1 {
		t.Fatalf("pending approvals after incomplete responses = %d, want 1", got)
	}
}

func awaitResolvedEvent(t *testing.T, resolved <-chan tooldto.ToolApprovalResolved) tooldto.ToolApprovalResolved {
	t.Helper()

	select {
	case event := <-resolved:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalResolved event")
		return tooldto.ToolApprovalResolved{}
	}
}

func newBlockingApprovalLocal(t *testing.T) (jrpcserver.Local, *PushBridge) {
	t.Helper()

	local := jrpcserver.NewLocal(handler.Map{
		DefaultApprovalCallbackMethod: StrictHandler(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{AllowPush: true}})
	t.Cleanup(func() {
		_ = local.Close()
	})
	return local, NewPushBridge(nil, nil)
}

func testApprovalRequest(callID string) ApprovalRequest {
	return testApprovalRequestWithID("test-session", callID, 1)
}

func testApprovalRequestWithID(sessionScope, callID string, requestID int64) ApprovalRequest {
	requestIDRef := requestID
	return ApprovalRequest{
		SessionScope: sessionScope,
		CallID:       callID,
		RequestID:    &requestIDRef,
	}
}
