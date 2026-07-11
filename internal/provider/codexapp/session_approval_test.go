package codexapp

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"
	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestRequestApprovalDecisionAutoDeclinesWithoutFrontend(t *testing.T) {
	s := &session{
		approvals: rpc.NewApprovalManager(nil, nil),
		ctx:       context.Background(),
	}

	decision, err := s.requestApprovalDecision(rpc.ApprovalRequest{CallID: "call-1"})
	if err != nil {
		t.Fatalf("requestApprovalDecision() error = %v", err)
	}
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("requestApprovalDecision() approved = %v, want false", decision.Approved)
	}
	if decision.Reason != "decline" {
		t.Fatalf("requestApprovalDecision() reason = %q, want %q", decision.Reason, "decline")
	}
}

func TestRequestApprovalDecisionAutoRespondsRequestUserInputWhenPolicyNever(t *testing.T) {
	s := &session{
		agentID:    "agent-1",
		approvals:  rpc.NewApprovalManager(nil, nil),
		ctx:        context.Background(),
		suppressed: map[string]struct{}{},
		turns:      map[string]*turnHandle{},
	}
	s.setApprovalPolicy("never")

	req, _, ok := s.buildApprovalRequest("request_user_input", map[string]any{"requestId": int64(1), "message": "continue"})
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	if req.ApprovalPolicy != "never" {
		t.Fatalf("buildApprovalRequest() approval policy = %q, want %q", req.ApprovalPolicy, "never")
	}

	decision, err := s.requestApprovalDecision(req)
	if err != nil {
		t.Fatalf("requestApprovalDecision() error = %v", err)
	}
	if decision.Approved == nil || !*decision.Approved {
		t.Fatalf("requestApprovalDecision() approved = %v, want true", decision.Approved)
	}
	if decision.Reason != "auto_approved" {
		t.Fatalf("requestApprovalDecision() reason = %q, want %q", decision.Reason, "auto_approved")
	}
}

func TestOnNotificationApprovalRequestPublishesRequestedOnce(t *testing.T) {
	bus := event.NewDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &session{
		agentID:    "agent-1",
		approvals:  rpc.NewApprovalManager(nil, bus),
		ctx:        ctx,
		suppressed: map[string]struct{}{},
		turns:      map[string]*turnHandle{},
	}

	requested := make(chan tooldto.ToolApprovalRequested, 4)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) {
		requested <- ev
	})
	defer cancelSub()

	s.onNotification("item/commandExecution/requestApproval", []byte(`{"requestId":1,"command":"echo hi","toolName":"shell","turnId":"turn-1"}`))

	var first tooldto.ToolApprovalRequested
	select {
	case first = <-requested:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalRequested")
	}
	if first.RequestID != 1 {
		t.Fatalf("first requestID = %d, want 1", first.RequestID)
	}

	select {
	case extra := <-requested:
		t.Fatalf("received duplicate ToolApprovalRequested event: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
}

func TestAlienThreadEventThreadReportsIncomingThreadID(t *testing.T) {
	s := &session{}
	s.threadID.Store("own-thread")

	eventThread, ok := s.alienThreadEventThread([]byte(`{"threadId":"other-thread"}`))
	if !ok {
		t.Fatal("alienThreadEventThread() ok = false, want true")
	}
	if eventThread != "other-thread" {
		t.Fatalf("alienThreadEventThread() eventThread = %q, want other-thread", eventThread)
	}
}

func TestAlienThreadEventThreadIgnoresOwnOrMissingThreadID(t *testing.T) {
	s := &session{}
	s.threadID.Store("own-thread")

	for _, params := range []string{
		`{"threadId":"own-thread"}`,
		`{"turnId":"turn-1"}`,
		`{"threadId":" "}`,
		`{`,
	} {
		if eventThread, ok := s.alienThreadEventThread([]byte(params)); ok {
			t.Fatalf("alienThreadEventThread(%s) = (%q, true), want false", params, eventThread)
		}
	}
}

func TestBeginProcessedApprovalDedupesByCallIDAndRequestID(t *testing.T) {
	s := &session{processedApprovals: map[string]*processedApprovalEntry{}}

	key := processedApprovalKey("call-1", 1)
	first, firstOwner := s.beginProcessedApproval(key)
	second, secondOwner := s.beginProcessedApproval(key)

	if !firstOwner || secondOwner {
		t.Fatalf("owners = %v, %v; want true, false", firstOwner, secondOwner)
	}
	if first == nil || first != second {
		t.Fatalf("entries = %p, %p; want same non-nil entry", first, second)
	}
}

func TestBeginProcessedApprovalClearsCacheAtCapacity(t *testing.T) {
	s := &session{processedApprovals: map[string]*processedApprovalEntry{}}

	finishProcessedApprovalRange(t, s, 0, processedApprovalLimit)

	lastKey := processedApprovalKey("call-overflow", int64(processedApprovalLimit+1))
	lastEntry, owner := s.beginProcessedApproval(lastKey)
	if lastEntry == nil || !owner {
		t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want new owner entry", lastKey, lastEntry, owner)
	}

	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if got := len(s.processedApprovals); got != 1 {
		t.Fatalf("processed approvals = %d, want 1 after capacity reset", got)
	}
	if s.processedApprovals[lastKey] != lastEntry {
		t.Fatalf("processed approvals[%q] = %#v, want %#v", lastKey, s.processedApprovals[lastKey], lastEntry)
	}
}

func TestBeginProcessedApprovalCapacityKeepsPendingEntries(t *testing.T) {
	s := &session{processedApprovals: map[string]*processedApprovalEntry{}}

	pendingKey := processedApprovalKey("call-pending", 1)
	pendingEntry, owner := s.beginProcessedApproval(pendingKey)
	if pendingEntry == nil || !owner {
		t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want pending owner entry", pendingKey, pendingEntry, owner)
	}

	finishProcessedApprovalRange(t, s, 1, processedApprovalLimit)

	lastKey := processedApprovalKey("call-overflow-pending", int64(processedApprovalLimit+1))
	lastEntry, owner := s.beginProcessedApproval(lastKey)
	if lastEntry == nil || !owner {
		t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want new owner entry", lastKey, lastEntry, owner)
	}

	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if got := len(s.processedApprovals); got != 2 {
		t.Fatalf("processed approvals = %d, want 2 after purging completed entries", got)
	}
	if s.processedApprovals[pendingKey] != pendingEntry {
		t.Fatalf("pending entry for %q was removed", pendingKey)
	}
	if s.processedApprovals[lastKey] != lastEntry {
		t.Fatalf("processed approvals[%q] = %#v, want %#v", lastKey, s.processedApprovals[lastKey], lastEntry)
	}
}

func finishProcessedApprovalRange(t *testing.T, s *session, start, stop int) {
	t.Helper()
	for i := start; i < stop; i++ {
		key := processedApprovalKey("call-"+strconv.Itoa(i), int64(i+1))
		entry, owner := s.beginProcessedApproval(key)
		if entry == nil || !owner {
			t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want new owner entry", key, entry, owner)
		}
		s.finishProcessedApproval(key, entry, rpcDecision(false, "decline"), nil)
	}
}

func rpcDecision(approved bool, reason string) contract.ApprovalDecision {
	return contract.ApprovalDecision{Approved: &approved, Reason: reason}
}

type blockingApprovalRequester struct {
	mu       sync.Mutex
	once     sync.Once
	calls    int
	requests []rpc.ApprovalRequest
	started  chan struct{}
	release  chan struct{}
	decision contract.ApprovalDecision
}

func (b *blockingApprovalRequester) RequestApproval(ctx context.Context, req rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
	b.mu.Lock()
	b.calls++
	b.requests = append(b.requests, req)
	b.mu.Unlock()
	b.once.Do(func() { close(b.started) })
	select {
	case <-ctx.Done():
		return contract.ApprovalDecision{}, ctx.Err()
	case <-b.release:
		return b.decision, nil
	}
}

func (b *blockingApprovalRequester) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func TestProcessedApprovalRequestKeyPreservesNestedCallID(t *testing.T) {
	base := rpc.ApprovalRequest{
		SourceMethod: "item/commandExecution/requestApproval",
		CallID:       "volatile-top-level-a",
		Payload: map[string]any{
			"requestId": int64(5),
			"callId":    "volatile-top-level-a",
			"toolName":  "custom",
			"arguments": map[string]any{"callId": "business-a"},
		},
	}
	replayed := base
	replayed.CallID = "volatile-top-level-b"
	replayed.Payload = map[string]any{
		"requestId": int64(5),
		"callId":    "volatile-top-level-b",
		"toolName":  "custom",
		"arguments": map[string]any{"callId": "business-a"},
	}
	if got, want := processedApprovalRequestKey(base, 5), processedApprovalRequestKey(replayed, 5); got != want {
		t.Fatalf("top-level volatile callId changed key: got %q want %q", got, want)
	}

	differentNested := base
	differentNested.Payload = map[string]any{
		"requestId": int64(5),
		"callId":    "volatile-top-level-a",
		"toolName":  "custom",
		"arguments": map[string]any{"callId": "business-b"},
	}
	if got, wantDifferentFrom := processedApprovalRequestKey(differentNested, 5), processedApprovalRequestKey(base, 5); got == wantDifferentFrom {
		t.Fatalf("nested business callId was ignored in key: both %q", got)
	}
}

func TestSanitizeProviderPayloadApprovalRespondCanonicalizesRequestID(t *testing.T) {
	got := requireSanitizedPayloadMap(t, "approval/respond", map[string]any{
		"request_id": int64(11),
		"approved":   true,
	})
	if got["requestId"] != int64(11) {
		t.Fatalf("requestId = %#v, want 11", got["requestId"])
	}
	assertSanitizedPayloadOmitsKey(t, got, "request_id", "approval/respond payload")

	got = requireSanitizedPayloadMap(t, "approval/respond", map[string]any{
		"request_id": int64(11),
		"requestId":  int64(12),
	})
	if got["requestId"] != int64(12) {
		t.Fatalf("requestId = %#v, want explicit camelCase 12", got["requestId"])
	}
	assertSanitizedPayloadOmitsKey(t, got, "request_id", "camelCase was present")

	got = requireSanitizedPayloadMap(t, "turn/start", map[string]any{
		"request_id": int64(11),
		"requestId":  int64(12),
		"threadId":   "thread-1",
	})
	assertSanitizedPayloadOmitsKey(t, got, "requestId", "non-approval method")
	assertSanitizedPayloadOmitsKey(t, got, "request_id", "non-approval method")
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId = %#v, want thread-1", got["threadId"])
	}
}

func requireSanitizedPayloadMap(t *testing.T, method string, payload map[string]any) map[string]any {
	t.Helper()
	got, ok := sanitizeProviderPayload(method, payload).(map[string]any)
	if !ok {
		t.Fatalf("sanitizeProviderPayload() type = %T, want map[string]any", got)
	}
	return got
}

func assertSanitizedPayloadOmitsKey(t *testing.T, got map[string]any, key, scenario string) {
	t.Helper()
	if _, ok := got[key]; ok {
		t.Fatalf("%s leaked for %s: %#v", key, scenario, got)
	}
}
