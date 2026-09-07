package tools

import (
	"context"
	"errors"
	"testing"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type nativePeerStoreStub struct {
	directPeerStub
	receipt   contract.ProviderSubmissionReceipt
	recordErr error
	records   int
}

func (*nativePeerStoreStub) OwnerCapability() (directpeer.OwnerCapability, error) {
	return directpeer.OwnerCapability{}, nil
}

func (s *nativePeerStoreStub) RecordNativeReceipt(_ context.Context, _ directpeer.OwnerCapability, id string, r contract.ProviderSubmissionReceipt) (directpeer.Message, error) {
	s.records++
	if s.recordErr != nil {
		return directpeer.Message{}, s.recordErr
	}
	s.receipt, s.replayStatus = r, "submitted"
	return directpeer.Message{MessageID: id, ReceiverAgentID: r.AgentID, SubmissionID: r.TurnID, Status: "submitted"}, nil
}

func (s *nativePeerStoreStub) ReadNativeReceipt(context.Context, directpeer.Message) (contract.ProviderSubmissionReceipt, error) {
	return s.receipt, s.receipt.Validate()
}

type nativePeerTurnStub struct {
	directPeerTurnStub
	receipt contract.ProviderSubmissionReceipt
	calls   int
}

func (s *nativePeerTurnStub) SubmitTurnWithReceipt(context.Context, contract.TurnSubmission) (contract.ProviderSubmissionReceipt, error) {
	s.calls++
	return s.receipt, s.err
}

type nativePeerAgentStub struct{ directPeerAgentStub }

func (s nativePeerAgentStub) Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error) {
	r, err := s.directPeerAgentStub.Snapshot(ctx, agentID)
	r.ThreadID = "thread"
	return r, err
}

// TestDirectPeerNativeReceipts 验证原生确认唯一提交、身份拒绝及未知不重发。
func TestDirectPeerNativeReceipts(t *testing.T) {
	t.Run("success-replay", testNativePeerSuccessReplay)
	for _, name := range []string{"agent", "thread", "provider", "generation", "submit-error", "store-error"} {
		t.Run(name, func(t *testing.T) { testNativePeerFailure(t, name) })
	}
	t.Run("legacy-submitted", TestDirectPeerSubmittedReplayWithoutNativeReceiptFailsClosed)
	t.Run("accepted-unknown", TestDirectPeerAcceptedReplayAfterMarkFailureIsFailClosed)
	t.Run("error-only", TestDirectPeerSubmitErrorIsOutcomeUnknown)
}

func newNativePeerTest(t *testing.T) (*nativePeerStoreStub, *nativePeerTurnStub, ToolHandler, context.Context) {
	t.Helper()
	store := &nativePeerStoreStub{}
	turns := &nativePeerTurnStub{receipt: contract.ProviderSubmissionReceipt{Provider: "codex", AgentID: "b", ThreadID: "thread", TurnID: "turn", ReceiptID: "turn", LaunchSeq: 1}}
	workspace := t.TempDir()
	handler := handleSendDirectPeer(DirectPeerPorts{Store: store, Turns: turns, Agents: nativePeerAgentStub{directPeerAgentStub{workspace: workspace}}})
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: workspace, AgentID: "a"})
	return store, turns, handler, ctx
}

func testNativePeerSuccessReplay(t *testing.T) {
	store, turns, handler, ctx := newNativePeerTest(t)
	for range 2 {
		result, err := handler(ctx, directPeerSendPayload())
		if err != nil {
			t.Fatal(err)
		}
		message, ok := result.(directpeer.Message)
		if !ok || message.Status != "submitted" {
			t.Fatalf("receipt result=%#v", result)
		}
	}
	if turns.calls != 1 || store.records != 1 || len(turns.submissions) != 0 {
		t.Fatalf("native calls=%d records=%d error-only calls=%d", turns.calls, store.records, len(turns.submissions))
	}
}

func testNativePeerFailure(t *testing.T, name string) {
	store, turns, handler, ctx := newNativePeerTest(t)
	switch name {
	case "agent":
		turns.receipt.AgentID = "other"
	case "thread":
		turns.receipt.ThreadID = "old-thread"
	case "provider":
		turns.receipt.Provider = "unknown"
	case "generation":
		turns.receipt.LaunchSeq = 0
	case "submit-error":
		turns.err = errors.New("secret-provider-detail")
	case "store-error":
		store.recordErr = errors.New("secret-store-detail")
	}
	assertNativePeerUnknown(t, handler, ctx)
	assertNativePeerUnknown(t, handler, ctx)
	if turns.calls != 1 {
		t.Fatalf("unknown replay called provider %d times", turns.calls)
	}
}

func assertNativePeerUnknown(t *testing.T, handler ToolHandler, ctx context.Context) {
	t.Helper()
	result, err := handler(ctx, directPeerSendPayload())
	var coded *mcpcommon.CodedToolError
	if result != nil || !errors.As(err, &coded) || coded.Retryable || coded.Code != "delivery_outcome_unknown" {
		t.Fatalf("unknown result=%#v error=%v", result, err)
	}
}
