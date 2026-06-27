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
	readMessagesThread string
	readMessagesLimit  int
	readMessagesBefore string
	readMessagesResult dto.ThreadMessagesResult
}

var errUnexpectedSessionPortCall = errors.New("unexpected session port call")

func (*recordingSessionPorts) StartSession(context.Context, contract.SessionStartRequest) (contract.SessionStartResult, error) {
	return contract.SessionStartResult{}, errUnexpectedSessionPortCall
}

func (*recordingSessionPorts) ResumeSession(context.Context, string) (contract.SessionStartResult, error) {
	return contract.SessionStartResult{}, errUnexpectedSessionPortCall
}

func (*recordingSessionPorts) ForkSession(context.Context, string) (contract.SessionStartResult, error) {
	return contract.SessionStartResult{}, errUnexpectedSessionPortCall
}

func (*recordingSessionPorts) ArchiveSession(context.Context, string) error {
	return errUnexpectedSessionPortCall
}

func (*recordingSessionPorts) ListSessions(context.Context) ([]contract.SessionThreadSummary, error) {
	return nil, errUnexpectedSessionPortCall
}

func (p *recordingSessionPorts) ReadMessages(_ context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	p.readMessagesThread = threadID
	p.readMessagesLimit = limit
	p.readMessagesBefore = before
	return p.readMessagesResult, nil
}
