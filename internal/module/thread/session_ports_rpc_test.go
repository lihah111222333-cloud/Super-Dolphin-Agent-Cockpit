package thread

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	rpcpkg "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

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

	stub := &stubThreadService{}
	sessionPorts := &recordingSessionPorts{
		readMessagesResult: dto.ThreadMessagesResult{
			Messages:   []dto.Message{{ID: 2, AgentID: "thread-1", Role: "assistant", EventType: "agent_message", Content: "world"}},
			Total:      7,
			HasMore:    true,
			NextBefore: "opaque-cursor",
		},
	}
	server := newThreadTestServerWithSessionPorts(stub, sessionPorts)
	raw, err := server.Dispatch(context.Background(), "thread/messages", json.RawMessage(`{"threadId":"thread-1","limit":2,"before":3}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/messages) error = %v", err)
	}
	var got dto.ThreadMessagesResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/messages) error = %v", err)
	}
	requireThreadMessagesEnvelope(t, got)
	requireThreadMessagesPortCall(t, sessionPorts, "thread-1", 2, "3")
	if stub.readMessagesThread != "" || stub.readMessagesLimit != 0 || stub.readMessagesBefore != "" {
		t.Fatalf("thread/messages called Service.ReadMessages directly: %#v", stub)
	}
}

func TestNewThreadHandlersDispatchListUsesSessionPorts(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{}
	sessionPorts := &recordingSessionPorts{
		listResult: []contract.SessionThreadSummary{{
			ID:      "thread-1",
			Name:    "demo",
			AgentID: "agent-1",
			Status:  "archived",
		}},
	}
	server := newThreadTestServerWithSessionPorts(stub, sessionPorts)
	raw, err := server.Dispatch(context.Background(), "thread/list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/list) error = %v", err)
	}
	var got []contract.SessionThreadSummary
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/list) error = %v", err)
	}
	if !sessionPorts.listCalled || len(got) != 1 || got[0].ID != "thread-1" || got[0].Status != "archived" {
		t.Fatalf("Dispatch(thread/list) = %#v, listCalled=%v", got, sessionPorts.listCalled)
	}
	if stub.listCalls != 0 {
		t.Fatalf("thread/list called Service.List directly: calls=%d", stub.listCalls)
	}
}

func TestNewThreadHandlersDispatchResumeUsesSessionPorts(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{}
	sessionPorts := &recordingSessionPorts{
		resumeResult: contract.SessionStartResult{
			ThreadID:  "thread-9",
			SessionID: "session-9",
			Status:    "resumed",
			Model:     "gpt-5.5",
			CWD:       "/tmp/resume",
		},
	}
	server := newThreadTestServerWithSessionPorts(stub, sessionPorts)
	raw, err := server.Dispatch(context.Background(), "thread/resume", json.RawMessage(`{"threadId":"thread-9","path":"/tmp/legacy","cwd":"/tmp/resume","model":"gpt-5.5","provider":"codex"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/resume) error = %v", err)
	}
	got := decodeThreadHandlerMap(t, "thread/resume", raw)
	requireThreadResumePortResponse(t, got)
	requireThreadResumePortCall(t, sessionPorts)
	requireServiceResumeNotCalled(t, stub)
}

func TestNewThreadHandlersDispatchForkUsesSessionPorts(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{}
	sessionPorts := &recordingSessionPorts{
		forkResult: contract.SessionForkResult{
			NewThreadID:  "thread-7-fork",
			ForkedFrom:   "thread-7",
			KickoffState: "created_only",
		},
	}
	server := newThreadTestServerWithSessionPorts(stub, sessionPorts)
	raw, err := server.Dispatch(context.Background(), "thread/fork", json.RawMessage(`{"threadId":"thread-7"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/fork) error = %v", err)
	}
	var got struct {
		Thread            threadInfo `json:"thread"`
		KickoffState      string     `json:"kickoff_state"`
		KickoffStateCamel string     `json:"kickoffState"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/fork) error = %v", err)
	}
	requireThreadForkPortResponse(t, got.Thread, got.KickoffState, got.KickoffStateCamel)
	if sessionPorts.forkThread != "thread-7" {
		t.Fatalf("SessionPorts.ForkSession thread = %q, want thread-7", sessionPorts.forkThread)
	}
	if stub.forkThreadID != "" {
		t.Fatalf("thread/fork called Service.Fork directly: %q", stub.forkThreadID)
	}
}

func requireThreadResumePortResponse(t *testing.T, got map[string]any) {
	t.Helper()
	if got["threadId"] != "thread-9" || got["sessionId"] != "session-9" || got["status"] != "resumed" {
		t.Fatalf("Dispatch(thread/resume) = %#v", got)
	}
}

func requireThreadResumePortCall(t *testing.T, ports *recordingSessionPorts) {
	t.Helper()
	want := contract.SessionResumeRequest{ThreadID: "thread-9", Path: "/tmp/legacy", CWD: "/tmp/resume", Model: "gpt-5.5", Provider: "codex"}
	if ports.resumeReq != want {
		t.Fatalf("SessionPorts.ResumeSession req = %#v, want %#v", ports.resumeReq, want)
	}
}

func requireServiceResumeNotCalled(t *testing.T, stub *stubThreadService) {
	t.Helper()
	if stub.resumeReq.ThreadID != "" || stub.resumeReq.Path != "" || stub.resumeReq.CWD != "" || stub.resumeReq.Model != "" || stub.resumeReq.Provider != "" {
		t.Fatalf("thread/resume called Service.Resume directly: %#v", stub.resumeReq)
	}
}

func requireThreadForkPortResponse(t *testing.T, thread threadInfo, kickoffState, kickoffStateCamel string) {
	t.Helper()
	if thread.ID != "thread-7-fork" || thread.ForkedFrom != "thread-7" || kickoffState != "created_only" || kickoffStateCamel != "created_only" {
		t.Fatalf("Dispatch(thread/fork) = thread:%#v kickoff:%q/%q", thread, kickoffState, kickoffStateCamel)
	}
}

func requireThreadMessagesEnvelope(t *testing.T, got dto.ThreadMessagesResult) {
	t.Helper()
	if got.Total != 7 {
		t.Fatalf("total = %d, want 7", got.Total)
	}
	if !got.HasMore || got.NextBefore != "opaque-cursor" {
		t.Fatalf("page metadata = hasMore:%v nextBefore:%q", got.HasMore, got.NextBefore)
	}
	if len(got.Messages) != 1 || got.Messages[0].ID != 2 {
		t.Fatalf("messages = %#v, want id 2", got.Messages)
	}
}

func requireThreadMessagesPortCall(t *testing.T, ports *recordingSessionPorts, threadID string, limit int, before string) {
	t.Helper()
	if ports.readMessagesThread != threadID {
		t.Fatalf("SessionPorts.ReadMessages thread = %q, want %q", ports.readMessagesThread, threadID)
	}
	if ports.readMessagesLimit != limit {
		t.Fatalf("SessionPorts.ReadMessages limit = %d, want %d", ports.readMessagesLimit, limit)
	}
	if ports.readMessagesBefore != before {
		t.Fatalf("SessionPorts.ReadMessages before = %q, want %q", ports.readMessagesBefore, before)
	}
}

func newThreadTestServerWithSessionPorts(svc Service, sessionPorts contract.SessionPorts) *rpcpkg.Server {
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(newThreadHandlers(svc, sessionPorts, nil).Handlers)
	return server
}

type recordingSessionPorts struct {
	listCalled         bool
	listResult         []contract.SessionThreadSummary
	resumeReq          contract.SessionResumeRequest
	resumeResult       contract.SessionStartResult
	forkThread         string
	forkResult         contract.SessionForkResult
	readMessagesThread string
	readMessagesLimit  int
	readMessagesBefore string
	readMessagesResult dto.ThreadMessagesResult
}

var errUnexpectedSessionPortCall = errors.New("unexpected session port call")

func (*recordingSessionPorts) StartSession(context.Context, contract.SessionStartRequest) (contract.SessionStartResult, error) {
	return contract.SessionStartResult{}, errUnexpectedSessionPortCall
}

func (p *recordingSessionPorts) ResumeSession(_ context.Context, req contract.SessionResumeRequest) (contract.SessionStartResult, error) {
	p.resumeReq = req
	return p.resumeResult, nil
}

func (p *recordingSessionPorts) ForkSession(_ context.Context, threadID string) (contract.SessionForkResult, error) {
	p.forkThread = threadID
	return p.forkResult, nil
}

func (*recordingSessionPorts) ArchiveSession(context.Context, string) error {
	return errUnexpectedSessionPortCall
}

func (p *recordingSessionPorts) ListSessions(context.Context) ([]contract.SessionThreadSummary, error) {
	p.listCalled = true
	return p.listResult, nil
}

func (p *recordingSessionPorts) ReadMessages(_ context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	p.readMessagesThread = threadID
	p.readMessagesLimit = limit
	p.readMessagesBefore = before
	return p.readMessagesResult, nil
}
