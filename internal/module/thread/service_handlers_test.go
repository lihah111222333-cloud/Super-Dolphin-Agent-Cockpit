package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	rpcpkg "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestNewServiceInitializesDefaults(t *testing.T) {
	t.Parallel()

	got, ok := NewService(nil, nil, nil, nil, nil, nil, nil, nil).(*service)
	if !ok {
		t.Fatalf("NewService() type = %T, want *service", NewService(nil, nil, nil, nil, nil, nil, nil, nil))
	}
	if got.logger == nil {
		t.Fatal("NewService() logger = nil")
	}
	if got.threadAgents == nil {
		t.Fatal("NewService() threadAgents = nil")
	}
	if len(got.threadAgents) != 0 {
		t.Fatalf("len(threadAgents) = %d, want 0", len(got.threadAgents))
	}
}

func TestNewThreadHandlersRegistersExpectedRoutes(t *testing.T) {
	t.Parallel()

	got := NewThreadHandlers(&stubThreadService{}, nil).Handlers
	if len(got) != 30 {
		t.Fatalf("len(Handlers) = %d, want 30", len(got))
	}
	for _, method := range []string{"thread/start", "thread/stop", "thread/list", "thread/model/set", "thread/realtime/start"} {
		if _, ok := got[method]; !ok {
			t.Fatalf("Handlers missing %q", method)
		}
	}
}

func TestNewThreadHandlersDispatchList(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		listResult: []Ref{{ID: "thread-1", Name: "demo", AgentID: "agent-1"}},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/list) error = %v", err)
	}
	var got []Ref
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/list) error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "thread-1" || stub.listCalls != 1 {
		t.Fatalf("Dispatch(thread/list) = %#v, calls=%d", got, stub.listCalls)
	}
}

func TestNewThreadHandlersDispatchStart(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		startResult: StartResult{ThreadID: "thread-7", AgentID: "agent-7", SessionID: "session-7", Status: "running"},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/start", json.RawMessage(`{"provider":"codex","cwd":"/tmp/demo","prompt":"hello"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/start) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/start) error = %v", err)
	}
	if got["threadId"] != "thread-7" || got["sessionId"] != "session-7" || got["status"] != "running" {
		t.Fatalf("Dispatch(thread/start) = %#v", got)
	}
	thread, _ := got["thread"].(map[string]any)
	if thread["id"] != "thread-7" || thread["status"] != "running" {
		t.Fatalf("Dispatch(thread/start).thread = %#v", thread)
	}
	if stub.startReq.Provider != "codex" || stub.startReq.CWD != "/tmp/demo" || stub.startReq.Prompt != "hello" {
		t.Fatalf("StartRequest = %#v", stub.startReq)
	}
}

func TestNewThreadHandlersDispatchConfigSet(t *testing.T) {
	t.Parallel()

	high := "high"
	empty := ""
	stub := &stubThreadService{
		setConfigResp: dto.ThreadConfig{
			ThreadID: "thread-1",
			Effective: dto.ThreadConfigValues{
				Model:  "gpt-5.4",
				Effort: "high",
			},
		},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/config/set", json.RawMessage(`{"threadId":"thread-1","model":"","effort":"high"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/config/set) error = %v", err)
	}
	var got dto.ThreadConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/config/set) error = %v", err)
	}
	if got.Effective.Model != "gpt-5.4" || got.Effective.Effort != "high" {
		t.Fatalf("Dispatch(thread/config/set) = %#v", got)
	}
	if stub.setConfigID != "thread-1" {
		t.Fatalf("setConfigID = %q, want thread-1", stub.setConfigID)
	}
	if stub.setConfigPatch.Model == nil || *stub.setConfigPatch.Model != empty {
		t.Fatalf("setConfigPatch.Model = %#v, want explicit empty string", stub.setConfigPatch.Model)
	}
	if stub.setConfigPatch.Effort == nil || *stub.setConfigPatch.Effort != high {
		t.Fatalf("setConfigPatch.Effort = %#v, want %q", stub.setConfigPatch.Effort, high)
	}
}

func TestNewThreadHandlersDispatchModelSetReturnsServiceCapabilityError(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		setModelErr: newFriendlyCapabilityError(dto.CapModelSwitch, "claude", errRuntimeModelSwitchUnsupported),
	}
	server := newThreadTestServer(stub)
	_, err := server.Dispatch(context.Background(), "thread/model/set", json.RawMessage(`{"threadId":"thread-1","model":"sonnet"}`))
	if err == nil {
		t.Fatal("Dispatch(thread/model/set) error = nil, want capability error")
	}
	if stub.setModelID != "thread-1" || stub.setModelArg != "sonnet" {
		t.Fatalf("SetModel call = %q/%q", stub.setModelID, stub.setModelArg)
	}
	if !strings.Contains(err.Error(), errRuntimeModelSwitchUnsupported) {
		t.Fatalf("Dispatch(thread/model/set) error = %v, want contains %q", err, errRuntimeModelSwitchUnsupported)
	}
	if errors.Is(err, rpcpkg.ErrCapabilityGate("capability not supported by active provider")) {
		t.Fatalf("Dispatch(thread/model/set) error = %v, want service capability error", err)
	}
}

func TestNewThreadHandlersDispatchApprovalsSetAcceptsPolicyAlias(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{sendCommandResult: map[string]any{"ok": true}}
	server := newThreadTestServer(stub)
	if _, err := server.Dispatch(context.Background(), "thread/approvals/set", json.RawMessage(`{"threadId":"thread-3","policy":"on-request"}`)); err != nil {
		t.Fatalf("Dispatch(thread/approvals/set) error = %v", err)
	}
	if stub.sendCommandThread != "thread-3" || stub.sendCommandName != "/approvals" || stub.sendCommandArgs != "on-request" {
		t.Fatalf("SendCommand(thread/approvals/set) = (%q, %q, %q)", stub.sendCommandThread, stub.sendCommandName, stub.sendCommandArgs)
	}
}

func TestNewThreadHandlersDispatchApprovalsSetRejectsConflictingArgs(t *testing.T) {
	t.Parallel()

	server := newThreadTestServer(&stubThreadService{})
	_, err := server.Dispatch(context.Background(), "thread/approvals/set", json.RawMessage(`{"threadId":"thread-3","policy":"never","args":"always"}`))
	if err == nil || !strings.Contains(err.Error(), errApprovalsSetArgsConflict.Error()) {
		t.Fatalf("Dispatch(thread/approvals/set) error = %v", err)
	}
}

func TestNewThreadHandlersDispatchReadReturnsHistoryPayload(t *testing.T) {
	t.Parallel()

	server := newThreadTestServer(&stubThreadService{})
	raw, err := server.Dispatch(context.Background(), "thread/read", json.RawMessage(`{"threadId":"thread-1"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/read) error = %v", err)
	}
	var got ReadHistoryResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/read) error = %v", err)
	}
	want := ReadHistoryResult{History: []ReadHistoryThread{{ThreadID: "thread-1"}}}
	if got.History == nil || len(got.History) != 1 || got.History[0] != want.History[0] {
		t.Fatalf("Dispatch(thread/read) = %#v, want %#v", got, want)
	}
}

func TestNewThreadHandlersDispatchMessagesReturnsEnvelope(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		readMessagesResult: dto.ThreadMessagesResult{
			Messages: []dto.Message{{ID: 2, AgentID: "thread-1", Role: "assistant", EventType: "agent_message", Content: "world"}},
			Total:    7,
		},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/messages", json.RawMessage(`{"threadId":"thread-1","limit":2,"before":3}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/messages) error = %v", err)
	}
	var got dto.ThreadMessagesResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/messages) error = %v", err)
	}
	if got.Total != 7 || len(got.Messages) != 1 || got.Messages[0].ID != 2 {
		t.Fatalf("Dispatch(thread/messages) = %#v", got)
	}
	if stub.readMessagesThread != "thread-1" || stub.readMessagesLimit != 2 || stub.readMessagesBefore != "3" {
		t.Fatalf("ReadMessages call = (%q, %d, %q)", stub.readMessagesThread, stub.readMessagesLimit, stub.readMessagesBefore)
	}
}

func newThreadTestServer(svc Service) *rpcpkg.Server {
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewThreadHandlers(svc, nil).Handlers)
	return server
}

type stubThreadService struct {
	startReq           StartRequest
	startResult        StartResult
	listResult         []Ref
	listCalls          int
	setConfigPatch     dto.ThreadConfigPatch
	setConfigID        string
	setConfigResp      dto.ThreadConfig
	setModelID         string
	setModelArg        string
	setModelErr        error
	readMessagesThread string
	readMessagesLimit  int
	readMessagesBefore string
	readMessagesResult dto.ThreadMessagesResult
	sendCommandThread  string
	sendCommandName    string
	sendCommandArgs    string
	sendCommandResult  any
}

func (s *stubThreadService) Start(_ context.Context, req StartRequest) (StartResult, error) {
	s.startReq = req
	return s.startResult, nil
}
func (s *stubThreadService) Stop(context.Context, string) error { return nil }
func (s *stubThreadService) Resume(context.Context, ResumeRequest) (ResumeResult, error) {
	return ResumeResult{}, nil
}
func (s *stubThreadService) Fork(context.Context, string) (ForkResult, error) {
	return ForkResult{}, nil
}
func (s *stubThreadService) Recover(context.Context, string) error { return nil }
func (s *stubThreadService) Get(context.Context, string) (*Ref, error) {
	return &Ref{ID: "thread-1", AgentID: "agent-1"}, nil
}
func (s *stubThreadService) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}
func (s *stubThreadService) ReadMessages(_ context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	s.readMessagesThread = threadID
	s.readMessagesLimit = limit
	s.readMessagesBefore = before
	return s.readMessagesResult, nil
}
func (s *stubThreadService) GetConfig(context.Context, string) (dto.ThreadConfig, error) {
	return dto.ThreadConfig{}, nil
}
func (s *stubThreadService) SetConfig(_ context.Context, threadID string, patch dto.ThreadConfigPatch) (dto.ThreadConfig, error) {
	s.setConfigID = threadID
	s.setConfigPatch = patch
	return s.setConfigResp, nil
}
func (s *stubThreadService) SetModel(_ context.Context, threadID, model string) (dto.ThreadConfig, error) {
	s.setModelID = threadID
	s.setModelArg = model
	return dto.ThreadConfig{}, s.setModelErr
}
func (s *stubThreadService) Compact(context.Context, string, string) (dto.ThreadCompactResult, error) {
	return dto.ThreadCompactResult{}, nil
}
func (s *stubThreadService) Archive(context.Context, string) error               { return nil }
func (s *stubThreadService) Unarchive(context.Context, string) error             { return nil }
func (s *stubThreadService) ListByStatus(context.Context, string) ([]Ref, error) { return nil, nil }
func (s *stubThreadService) ListByCWD(context.Context, string) ([]Ref, error)    { return nil, nil }
func (s *stubThreadService) SendCommand(_ context.Context, threadID, command, args string) (any, error) {
	s.sendCommandThread = threadID
	s.sendCommandName = command
	s.sendCommandArgs = args
	return s.sendCommandResult, nil
}
func (s *stubThreadService) SetName(context.Context, string, string) error { return nil }
func (s *stubThreadService) Delete(context.Context, string) error          { return nil }

func (s *stubThreadService) List(context.Context) ([]Ref, error) {
	s.listCalls++
	return s.listResult, nil
}
