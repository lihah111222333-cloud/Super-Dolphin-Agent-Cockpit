package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	directpeerstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type directPeerStub struct {
	grantInput                  manageDirectPeerInput
	send                        directpeerstore.SendRequest
	readWorkspace, readReceiver string
	readCursor                  int64
	readLimit                   int
	eventRead                   bool
	markSubmitted, markFailed   int
	failure                     string
	sendCalls                   int
	replayStatus                string
	markSubmittedErr            error
}

type directPeerAgentStub struct{ workspace string }

func (s directPeerAgentStub) ListAgents(context.Context) ([]contract.AgentSnapshot, error) {
	return nil, nil
}
func (s directPeerAgentStub) Snapshot(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
	if agentID != "a" && agentID != "b" {
		return contract.AgentSnapshot{}, contract.ErrAgentNotFound
	}
	return contract.AgentSnapshot{AgentID: agentID, Cwd: s.workspace}, nil
}
func (s directPeerAgentStub) GetState(context.Context, string) (contract.AgentStateResult, error) {
	return contract.AgentStateResult{}, nil
}

func (s *directPeerStub) GrantPeer(_ context.Context, workspace, sender, receiver, key string, canSend, canRead bool) (directpeerstore.Grant, error) {
	s.grantInput = manageDirectPeerInput{SenderAgentID: sender, ReceiverAgentID: receiver, IdempotencyKey: key, CanSend: canSend, CanRead: canRead}
	return directpeerstore.Grant{WorkspaceRoot: workspace, SenderAgentID: sender, ReceiverAgentID: receiver, Revision: 1}, nil
}
func (*directPeerStub) RevokePeer(context.Context, string, string, string, string) (directpeerstore.Grant, error) {
	return directpeerstore.Grant{}, nil
}
func (s *directPeerStub) Send(_ context.Context, request directpeerstore.SendRequest) (directpeerstore.Message, error) {
	s.sendCalls++
	s.send = request
	status, fresh := "accepted", s.sendCalls == 1
	if s.sendCalls > 1 && s.replayStatus != "" {
		status = s.replayStatus
	}
	return directpeerstore.Message{Cursor: 1, MessageID: "message", ReceiverAgentID: request.ReceiverAgentID, Body: request.Body, Status: status, Fresh: fresh}, nil
}
func (s *directPeerStub) ReadReceiver(_ context.Context, workspace, receiver string, cursor int64, limit int) ([]directpeerstore.Message, error) {
	s.readWorkspace, s.readReceiver, s.readCursor, s.readLimit = workspace, receiver, cursor, limit
	return []directpeerstore.Message{}, nil
}

func (s *directPeerStub) ListEventsForReceiver(context.Context, string, string, int64, int) ([]directpeerstore.Event, error) {
	s.eventRead = true
	return []directpeerstore.Event{}, nil
}
func (s *directPeerStub) MarkSubmitted(_ context.Context, messageID, submissionID string) (directpeerstore.Message, error) {
	s.markSubmitted++
	if s.markSubmittedErr != nil {
		return directpeerstore.Message{MessageID: messageID, SubmissionID: submissionID, Status: "accepted"}, s.markSubmittedErr
	}
	return directpeerstore.Message{MessageID: messageID, SubmissionID: submissionID, Status: "submitted"}, nil
}

func (s *directPeerStub) MarkDeliveryFailed(_ context.Context, _ string, failure string) (directpeerstore.Message, error) {
	s.markFailed++
	s.failure = failure
	return directpeerstore.Message{Status: "delivery_failed"}, nil
}

type directPeerTurnStub struct {
	submissions []contract.TurnSubmission
	err         error
}

func (s *directPeerTurnStub) SubmitTurn(_ context.Context, submission contract.TurnSubmission) error {
	s.submissions = append(s.submissions, submission)
	return s.err
}

func (*directPeerTurnStub) CompleteTurn(context.Context, string, string, bool, string) error {
	return nil
}

// TestDirectPeerToolsTrustedScope 覆盖 operator grant 与 agent send/read 的服务端身份来源。
func TestDirectPeerToolsTrustedScope(t *testing.T) {
	stub := &directPeerStub{}
	workspace := t.TempDir()
	operator := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: workspace})
	turns := &directPeerTurnStub{}
	ports := DirectPeerPorts{Store: stub, Agents: directPeerAgentStub{workspace: workspace}, Turns: turns}
	assertDirectPeerOperatorGrant(t, ports, stub, operator)
	agent := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: workspace, AgentID: "a"})
	assertDirectPeerAgentSendRead(t, ports, stub, turns, agent, workspace)
	if _, err := handleManageDirectPeer(ports)(agent, json.RawMessage(`{"action":"grant","sender_agent_id":"a","receiver_agent_id":"b","idempotency_key":"agent-key","can_send":true}`)); err == nil {
		t.Fatal("agent scope granted peer authority")
	}
}

func assertDirectPeerOperatorGrant(t *testing.T, ports DirectPeerPorts, stub *directPeerStub, operator context.Context) {
	t.Helper()
	if _, err := handleManageDirectPeer(ports)(operator, json.RawMessage(`{"action":"grant","sender_agent_id":"a","receiver_agent_id":"b","idempotency_key":"grant-key","can_send":true,"can_read":true}`)); err != nil {
		t.Fatal(err)
	}
	if stub.grantInput.SenderAgentID != "a" || stub.grantInput.IdempotencyKey != "grant-key" || !stub.grantInput.CanSend || !stub.grantInput.CanRead {
		t.Fatalf("grant mapping=%#v", stub.grantInput)
	}
}

func assertDirectPeerAgentSendRead(t *testing.T, ports DirectPeerPorts, stub *directPeerStub, turns *directPeerTurnStub, agent context.Context, workspace string) {
	t.Helper()
	assertDirectPeerAgentSend(t, ports, stub, turns, agent, workspace)
	assertDirectPeerAgentRead(t, stub, agent, workspace)
}

func assertDirectPeerAgentSend(t *testing.T, ports DirectPeerPorts, stub *directPeerStub, turns *directPeerTurnStub, agent context.Context, workspace string) {
	t.Helper()
	result, err := handleSendDirectPeer(ports)(agent, json.RawMessage(`{"receiver_agent_id":"b","idempotency_key":"k","message":"hello"}`))
	assertDirectPeerUnknown(t, result, err, "send")
	if stub.send.WorkspaceRoot != workspace || stub.send.SenderAgentID != "a" || stub.send.ReceiverAgentID != "b" || stub.send.IdempotencyKey != "k" || stub.send.Body != "hello" {
		t.Fatalf("send mapping=%#v", stub.send)
	}
	if len(turns.submissions) != 1 || turns.submissions[0].AgentID != "b" || stub.markSubmitted != 0 {
		t.Fatalf("delivery submissions=%#v marks=%d", turns.submissions, stub.markSubmitted)
	}
}

func assertDirectPeerUnknown(t *testing.T, result any, err error, phase string) {
	t.Helper()
	if result != nil || !errors.Is(err, directpeerstore.ErrDeliveryOutcomeUnknown) {
		t.Fatalf("%s result=%#v err=%v, want non-authorizing delivery_outcome_unknown", phase, result, err)
	}
}

func assertDirectPeerAgentRead(t *testing.T, stub *directPeerStub, agent context.Context, workspace string) {
	t.Helper()
	if _, err := handleReadDirectPeer(stub)(agent, json.RawMessage(`{"after_cursor":7}`)); err != nil {
		t.Fatal(err)
	}
	if stub.readWorkspace != workspace || stub.readReceiver != "a" || stub.readCursor != 7 || stub.readLimit != 50 {
		t.Fatalf("read mapping=%#v", stub)
	}
	if _, err := handleReadDirectPeerEvents(stub)(agent, json.RawMessage(`{"after_cursor":8}`)); err != nil {
		t.Fatal(err)
	}
	if !stub.eventRead {
		t.Fatal("event read did not reach direct peer store")
	}
}

// TestDirectPeerDeliveryPortFailureMarksReceipt 防止已持久 accepted 消息因投递依赖缺失而没有失败 receipt。
func TestDirectPeerDeliveryPortFailureMarksReceipt(t *testing.T) {
	stub := &directPeerStub{}
	message := directpeerstore.Message{MessageID: "message", ReceiverAgentID: "b", Body: "hello", Status: "accepted"}
	if _, err := submitDirectPeerTurn(context.Background(), DirectPeerPorts{Store: stub}, message); err == nil {
		t.Fatal("missing delivery ports did not fail")
	}
	if stub.markFailed != 1 || stub.failure != "delivery ports unavailable" {
		t.Fatalf("failure receipt=%d %q", stub.markFailed, stub.failure)
	}
}

func TestDirectPeerDeliveryMissingStoreFailsClosed(t *testing.T) {
	message := directpeerstore.Message{MessageID: "message", ReceiverAgentID: "b", Body: "hello", Status: "accepted"}
	if _, err := submitDirectPeerTurn(context.Background(), DirectPeerPorts{}, message); err == nil {
		t.Fatal("missing direct peer store did not fail")
	}

	var typedNilStore *directPeerStub
	if _, err := submitDirectPeerTurn(context.Background(), DirectPeerPorts{Store: typedNilStore}, message); err == nil {
		t.Fatal("typed-nil direct peer store did not fail")
	}
}

func TestDirectPeerDeliveryTypedNilDependencyMarksFailure(t *testing.T) {
	stub := &directPeerStub{}
	var typedNilAgents *directPeerAgentStub
	turns := &directPeerTurnStub{}
	message := directpeerstore.Message{MessageID: "message", ReceiverAgentID: "b", Body: "hello", Status: "accepted"}
	_, err := submitDirectPeerTurn(context.Background(), DirectPeerPorts{Store: stub, Agents: typedNilAgents, Turns: turns}, message)
	if err == nil || stub.markFailed != 1 || stub.failure != "delivery ports unavailable" {
		t.Fatalf("typed-nil dependency err=%v marks=%d failure=%q", err, stub.markFailed, stub.failure)
	}
}

// TestDirectPeerAcceptedReplayAfterMarkFailureIsFailClosed retains the
// existing regression name while covering the stricter no-fake-receipt path.
func TestDirectPeerAcceptedReplayAfterMarkFailureIsFailClosed(t *testing.T) {
	stub := &directPeerStub{}
	turns := &directPeerTurnStub{}
	ports := DirectPeerPorts{Store: stub, Agents: directPeerAgentStub{workspace: t.TempDir()}, Turns: turns}
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: t.TempDir(), AgentID: "a"})
	assertFreshSubmitUnknown(t, ports, turns, stub, ctx)
	replay, err := handleSendDirectPeer(ports)(ctx, directPeerSendPayload())
	assertAcceptedReplayUnknown(t, replay, err, turns, stub)
}

func assertFreshSubmitUnknown(t *testing.T, ports DirectPeerPorts, turns *directPeerTurnStub, stub *directPeerStub, ctx context.Context) {
	t.Helper()
	result, err := handleSendDirectPeer(ports)(ctx, directPeerSendPayload())
	assertDirectPeerUnknown(t, result, err, "fresh send")
	if len(turns.submissions) != 1 || stub.markSubmitted != 0 {
		t.Fatalf("fresh delivery calls=%d marks=%d, want one provider call and no mark", len(turns.submissions), stub.markSubmitted)
	}
}

func assertAcceptedReplayUnknown(t *testing.T, replay any, err error, turns *directPeerTurnStub, stub *directPeerStub) {
	t.Helper()
	if replay != nil {
		t.Fatalf("accepted replay returned an authorizing payload: %#v", replay)
	}
	if !errors.Is(err, directpeerstore.ErrDeliveryOutcomeUnknown) {
		t.Fatalf("accepted replay error=%v, want delivery_outcome_unknown", err)
	}
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "delivery_outcome_unknown" || coded.Retryable {
		t.Fatalf("accepted replay classification=%#v, want stable non-retryable code", coded)
	}
	if len(turns.submissions) != 1 || stub.markSubmitted != 0 {
		t.Fatalf("accepted replay resubmitted: calls=%d marks=%d", len(turns.submissions), stub.markSubmitted)
	}
}

func directPeerSendPayload() json.RawMessage {
	return json.RawMessage(`{"receiver_agent_id":"b","idempotency_key":"k","message":"hello"}`)
}

// TestDirectPeerSubmittedReplayRemainsReceipt proves a replay that already
// carries a submitted receipt is returned without another provider call.
func TestDirectPeerSubmittedReplayRemainsReceipt(t *testing.T) {
	stub := &directPeerStub{replayStatus: "submitted"}
	turns := &directPeerTurnStub{}
	ports := DirectPeerPorts{Store: stub, Agents: directPeerAgentStub{workspace: t.TempDir()}, Turns: turns}
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: t.TempDir(), AgentID: "a"})
	first, err := handleSendDirectPeer(ports)(ctx, json.RawMessage(`{"receiver_agent_id":"b","idempotency_key":"k","message":"hello"}`))
	if first != nil || !errors.Is(err, directpeerstore.ErrDeliveryOutcomeUnknown) {
		t.Fatalf("first submit result=%#v err=%v, want non-authorizing unknown", first, err)
	}
	replay, err := handleSendDirectPeer(ports)(ctx, json.RawMessage(`{"receiver_agent_id":"b","idempotency_key":"k","message":"hello"}`))
	if err != nil {
		t.Fatalf("submitted replay error=%v", err)
	}
	message, ok := replay.(directpeerstore.Message)
	if !ok || message.Status != "submitted" {
		t.Fatalf("submitted replay=%#v", replay)
	}
	if len(turns.submissions) != 1 || stub.markSubmitted != 0 {
		t.Fatalf("submitted replay invoked provider: calls=%d marks=%d", len(turns.submissions), stub.markSubmitted)
	}
}

// TestDirectPeerSubmitErrorIsOutcomeUnknown 防止 error-only 提交端口伪造 definitive failure。
func TestDirectPeerSubmitErrorIsOutcomeUnknown(t *testing.T) {
	stub := &directPeerStub{}
	turns := &directPeerTurnStub{err: errors.New("provider acknowledgement unavailable bearer=secret-value")}
	ports := DirectPeerPorts{Store: stub, Agents: directPeerAgentStub{workspace: t.TempDir()}, Turns: turns}
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: t.TempDir(), AgentID: "a"})
	result, err := handleSendDirectPeer(ports)(ctx, directPeerSendPayload())
	if result != nil || !errors.Is(err, directpeerstore.ErrDeliveryOutcomeUnknown) {
		t.Fatalf("submit error result=%#v err=%v, want non-authorizing unknown", result, err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("submit error leaked provider detail: %v", err)
	}
	if stub.markFailed != 0 || len(turns.submissions) != 1 {
		t.Fatalf("submit error mutated failure receipt or duplicated call: marks=%d calls=%d", stub.markFailed, len(turns.submissions))
	}
}

// TestDirectPeerToolInputFieldCoverage 动态枚举 producer 字段并要求 schema 登记一致。
func TestDirectPeerToolInputFieldCoverage(t *testing.T) {
	definitions := directPeerToolDefinitions(DirectPeerPorts{Store: &directPeerStub{}, Agents: directPeerAgentStub{workspace: t.TempDir()}, Turns: &directPeerTurnStub{}})
	assertDirectPeerSchemaFields(t, definitions, "manage_direct_peer", reflect.TypeFor[manageDirectPeerInput]())
	assertDirectPeerSchemaFields(t, definitions, "send_direct_peer", reflect.TypeFor[sendDirectPeerInput]())
	assertDirectPeerSchemaFields(t, definitions, "read_direct_peer", reflect.TypeFor[readDirectPeerInput]())
	assertDirectPeerSchemaFields(t, definitions, "read_direct_peer_events", reflect.TypeFor[readDirectPeerInput]())
}

func assertDirectPeerSchemaFields(t *testing.T, definitions []ToolDefinition, name string, producer reflect.Type) {
	t.Helper()
	definition := mustFindToolDefinition(t, definitions, name)
	properties, ok := definition.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s schema properties missing", name)
	}
	for field := range producer.NumField() {
		jsonName := strings.Split(producer.Field(field).Tag.Get("json"), ",")[0]
		if jsonName == "" {
			t.Fatalf("%s producer field %s lacks json tag", name, producer.Field(field).Name)
		}
		if _, exists := properties[jsonName]; !exists {
			t.Fatalf("%s producer field %s missing from schema", name, jsonName)
		}
	}
	if len(properties) != producer.NumField() {
		t.Fatalf("%s schema has stale fields: got=%d producer=%d", name, len(properties), producer.NumField())
	}
}
