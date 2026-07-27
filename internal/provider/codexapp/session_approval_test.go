package codexapp

import (
	"context"
	"errors"
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
	requestID := int64(1)
	s := &session{
		approvalSessionScope: "test-session-scope",
		approvals:            rpc.NewApprovalManager(nil, nil),
		ctx:                  context.Background(),
	}

	decision, err := s.requestApprovalDecision(rpc.ApprovalRequest{
		SessionScope: "test-session-scope",
		CallID:       "call-1",
		RequestID:    &requestID,
	}, false)
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

func TestHandleApprovalRequestWithNilManagerFailsTurnAndCancelsSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := &turnHandle{localID: "turn-1", done: make(chan struct{})}
	s := &session{
		ctx:          ctx,
		cancel:       cancel,
		turns:        map[string]*turnHandle{"turn-1": handle},
		activeTurnID: "turn-1",
	}

	s.handleApprovalRequest("item/commandExecution/requestApproval", []byte(`{"requestId":1}`))

	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("approval request with nil manager left turn pending")
	}
	if !errors.Is(handle.Err(), errApprovalManagerRequired) {
		t.Fatalf("turn error = %v, want %v", handle.Err(), errApprovalManagerRequired)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("approval request with nil manager did not cancel session")
	}
}

func TestRequestApprovalDecisionAutoRespondsRequestUserInputWhenPolicyNever(t *testing.T) {
	s := &session{
		agentID:              "agent-1",
		approvalSessionScope: "test-session-scope",
		approvals:            rpc.NewApprovalManager(nil, nil),
		ctx:                  context.Background(),
		suppressed:           map[string]struct{}{},
		turns:                map[string]*turnHandle{},
	}
	s.setApprovalPolicy("never")

	req, _, ok := s.buildApprovalRequest("request_user_input", map[string]any{"requestId": int64(1), "callId": "call-1", "message": "continue"})
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	if req.ApprovalPolicy != "never" {
		t.Fatalf("buildApprovalRequest() approval policy = %q, want %q", req.ApprovalPolicy, "never")
	}

	decision, err := s.requestApprovalDecision(req, false)
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

func TestBuildApprovalRequestUsesPublicThreadIDForUIProjection(t *testing.T) {
	s := &session{
		agentID:              "agent-public-thread",
		approvalSessionScope: "test-session-scope",
	}
	s.setApprovalPolicy("on-request")

	req, _, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", map[string]any{
		"requestId": int64(1),
		"itemId":    "call-1",
		"threadId":  "provider-internal-thread",
		"turnId":    "provider-turn-1",
	})
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	if req.ThreadID != "agent-public-thread" {
		t.Fatalf("buildApprovalRequest() ThreadID = %q, want public thread ID", req.ThreadID)
	}
	if got := stringValue(req.Payload, "threadId"); got != "provider-internal-thread" {
		t.Fatalf("buildApprovalRequest() payload threadId = %q, want provider-internal-thread", got)
	}
}

func TestOnNotificationApprovalRequestPublishesRequestedOnce(t *testing.T) {
	bus := event.NewDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &session{
		agentID:              "agent-1",
		approvalSessionScope: "test-session-scope",
		approvals:            rpc.NewApprovalManager(nil, bus),
		ctx:                  ctx,
		suppressed:           map[string]struct{}{},
		turns:                map[string]*turnHandle{},
	}
	s.setApprovalPolicy("on-request")

	requested := make(chan tooldto.ToolApprovalRequested, 4)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) {
		requested <- ev
	})
	defer cancelSub()

	s.onNotification("item/commandExecution/requestApproval", []byte(`{"requestId":1,"callId":"call-1","command":"echo hi","toolName":"shell","turnId":"turn-1"}`))

	var first tooldto.ToolApprovalRequested
	select {
	case first = <-requested:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalRequested")
	}
	if first.RequestID != 1 {
		t.Fatalf("first requestID = %d, want 1", first.RequestID)
	}
	if first.SessionScope != "test-session-scope" || first.CallID != "call-1" {
		t.Fatalf("first identity = (%q, %q, %d), want (%q, %q, %d)", first.SessionScope, first.CallID, first.RequestID, "test-session-scope", "call-1", 1)
	}

	select {
	case extra := <-requested:
		t.Fatalf("received duplicate ToolApprovalRequested event: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
}

func TestBuildApprovalRequestRequiresFullBackendIdentity(t *testing.T) {
	s := &session{
		agentID:              "agent-1",
		approvalSessionScope: "session-scope-a",
	}
	s.setApprovalPolicy("on-request")
	req, requestID, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", map[string]any{
		"requestId": int64(17),
		"callId":    "call-17",
	})
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	if requestID != 17 || req.SessionScope != "session-scope-a" || req.CallID != "call-17" {
		t.Fatalf("buildApprovalRequest() identity = (%q, %q, %d), want (%q, %q, %d)", req.SessionScope, req.CallID, requestID, "session-scope-a", "call-17", 17)
	}

	for _, payload := range []map[string]any{
		{"requestId": int64(17)},
		{"requestId": int64(17), "approvalId": "approval-17"},
		{"callId": "call-17"},
	} {
		if _, _, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", payload); ok {
			t.Fatalf("buildApprovalRequest(%v) ok = true, want fail closed", payload)
		}
	}
}

func TestBuildApprovalRequestRejectsAmbiguousProviderIdentity(t *testing.T) {
	s := &session{agentID: "agent-1", approvalSessionScope: "session-scope-a"}
	s.setApprovalPolicy("on-request")
	for _, payload := range []map[string]any{
		{"requestId": int64(17), "request_id": int64(18), "callId": "call-17"},
		{"requestId": int64(17), "callId": "call-17", "call_id": "call-18"},
		{"requestId": int64(17), "callId": "call-17", "item": map[string]any{"callId": "call-18"}},
		{"requestId": int64(17), "request_id": nil, "callId": "call-17"},
		{"requestId": int64(17), "callId": "", "call_id": "call-17"},
	} {
		if _, _, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", payload); ok {
			t.Fatalf("buildApprovalRequest(%v) ok = true, want ambiguous identity rejected", payload)
		}
	}

	for _, payload := range []map[string]any{
		{"requestId": int64(17), "request_id": int64(17), "callId": "call-17", "call_id": "call-17"},
		{"requestId": int64(17), "callId": "call-17", "item": map[string]any{"call_id": "call-17"}},
	} {
		if _, _, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", payload); !ok {
			t.Fatalf("buildApprovalRequest(%v) ok = false, want identical aliases accepted", payload)
		}
	}
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
	s := &session{agentID: "public-thread"}
	s.threadID.Store("own-thread")

	for _, params := range []string{
		`{"threadId":"own-thread"}`,
		`{"thread_id":"public-thread"}`,
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
	first, firstOwner := mustBeginProcessedApproval(t, s, key)
	second, secondOwner := mustBeginProcessedApproval(t, s, key)

	if !firstOwner || secondOwner {
		t.Fatalf("owners = %v, %v; want true, false", firstOwner, secondOwner)
	}
	if first == nil || first != second {
		t.Fatalf("entries = %p, %p; want same non-nil entry", first, second)
	}
}

func mustBeginProcessedApproval(t *testing.T, s *session, key string) (*processedApprovalEntry, bool) {
	t.Helper()
	entry, owner, err := s.beginProcessedApproval(key, key)
	if err != nil {
		t.Fatalf("beginProcessedApproval(%q) error = %v", key, err)
	}
	return entry, owner
}

func TestBeginProcessedApprovalClearsCacheAtCapacity(t *testing.T) {
	s := &session{processedApprovals: map[string]*processedApprovalEntry{}}

	finishProcessedApprovalRange(t, s, 0, processedApprovalLimit)

	lastKey := processedApprovalKey("call-overflow", int64(processedApprovalLimit+1))
	lastEntry, owner := mustBeginProcessedApproval(t, s, lastKey)
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
	pendingEntry, owner := mustBeginProcessedApproval(t, s, pendingKey)
	if pendingEntry == nil || !owner {
		t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want pending owner entry", pendingKey, pendingEntry, owner)
	}

	finishProcessedApprovalRange(t, s, 1, processedApprovalLimit)

	lastKey := processedApprovalKey("call-overflow-pending", int64(processedApprovalLimit+1))
	lastEntry, owner := mustBeginProcessedApproval(t, s, lastKey)
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
		entry, owner := mustBeginProcessedApproval(t, s, key)
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

func TestProcessedApprovalRequestKeyUsesCanonicalIdentityAndSeparateFingerprint(t *testing.T) {
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
	if got, wantDifferentFrom := processedApprovalRequestKey(base, 5), processedApprovalRequestKey(replayed, 5); got == wantDifferentFrom {
		t.Fatalf("different canonical callIds shared key %q", got)
	}

	differentNested := base
	differentNested.Payload = map[string]any{
		"requestId": int64(5),
		"callId":    "volatile-top-level-a",
		"toolName":  "custom",
		"arguments": map[string]any{"callId": "business-b"},
	}
	if got, want := processedApprovalRequestKey(differentNested, 5), processedApprovalRequestKey(base, 5); got != want {
		t.Fatalf("payload changed canonical identity key: got %q want %q", got, want)
	}
	if got, wantDifferentFrom := approvalRequestFingerprint(differentNested, 5), approvalRequestFingerprint(base, 5); got == wantDifferentFrom {
		t.Fatalf("nested business callId was ignored in fingerprint: both %q", got)
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
