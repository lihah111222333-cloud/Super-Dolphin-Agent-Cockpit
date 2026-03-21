package thread

import (
	"context"
	"encoding/json"
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
		startResult: StartResult{ThreadID: "thread-7", AgentID: "agent-7"},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/start", json.RawMessage(`{"provider":"codex","cwd":"/tmp/demo","prompt":"hello"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/start) error = %v", err)
	}
	var got StartResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/start) error = %v", err)
	}
	if got.ThreadID != "thread-7" || got.AgentID != "agent-7" {
		t.Fatalf("Dispatch(thread/start) = %#v", got)
	}
	if stub.startReq.Provider != "codex" || stub.startReq.CWD != "/tmp/demo" || stub.startReq.Prompt != "hello" {
		t.Fatalf("StartRequest = %#v", stub.startReq)
	}
}

func newThreadTestServer(svc Service) *rpcpkg.Server {
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewThreadHandlers(svc, nil).Handlers)
	return server
}

type stubThreadService struct {
	startReq    StartRequest
	startResult StartResult
	listResult  []Ref
	listCalls   int
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
func (s *stubThreadService) ReadMessages(context.Context, string, int, string) ([]dto.Message, error) {
	return nil, nil
}
func (s *stubThreadService) GetConfig(context.Context, string) (dto.ThreadConfig, error) {
	return dto.ThreadConfig{}, nil
}
func (s *stubThreadService) SetModel(context.Context, string, string) (dto.ThreadConfig, error) {
	return dto.ThreadConfig{}, nil
}
func (s *stubThreadService) Compact(context.Context, string, string) (dto.ThreadCompactResult, error) {
	return dto.ThreadCompactResult{}, nil
}
func (s *stubThreadService) Archive(context.Context, string) error               { return nil }
func (s *stubThreadService) Unarchive(context.Context, string) error             { return nil }
func (s *stubThreadService) ListByStatus(context.Context, string) ([]Ref, error) { return nil, nil }
func (s *stubThreadService) ListByCWD(context.Context, string) ([]Ref, error)    { return nil, nil }
func (s *stubThreadService) SendCommand(context.Context, string, string, string) (any, error) {
	return nil, nil
}
func (s *stubThreadService) SetName(context.Context, string, string) error { return nil }
func (s *stubThreadService) Delete(context.Context, string) error          { return nil }

func (s *stubThreadService) List(context.Context) ([]Ref, error) {
	s.listCalls++
	return s.listResult, nil
}
