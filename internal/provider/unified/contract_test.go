package unified_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type mockTurnHandle struct {
	localID, providerID string
	done                chan struct{}
	err                 error
}

func newMockTurnHandle(localID, providerID string, err error) *mockTurnHandle {
	done := make(chan struct{})
	close(done)
	return &mockTurnHandle{localID: localID, providerID: providerID, done: done, err: err}
}

func (h *mockTurnHandle) LocalID() string       { return h.localID }
func (h *mockTurnHandle) ProviderID() string    { return h.providerID }
func (h *mockTurnHandle) Done() <-chan struct{} { return h.done }
func (h *mockTurnHandle) Err() error            { return h.err }

type mockSession struct {
	threadID      string
	caps          dto.CapabilitySet
	history       []dto.Message
	lastTurn      dto.TurnRequest
	lastInterrupt dto.InterruptRequest
	config        dto.ThreadConfigPatch
	closed        bool
	forceStopped  bool
}

func (m *mockSession) ThreadID() string                { return m.threadID }
func (m *mockSession) RolloutPath() string             { return "" }
func (m *mockSession) Capabilities() dto.CapabilitySet { return m.caps }
func (m *mockSession) Close(context.Context) error     { m.closed = true; return nil }
func (m *mockSession) ForceStop() error                { m.forceStopped = true; return nil }
func (m *mockSession) Interrupt(_ context.Context, req dto.InterruptRequest) error {
	m.lastInterrupt = req
	return nil
}
func (m *mockSession) ForceComplete(_ context.Context, _ dto.ForceCompleteRequest) error { return nil }
func (m *mockSession) Steer(_ context.Context, _ dto.SteerRequest) error                 { return nil }
func (m *mockSession) StartTurn(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	m.lastTurn = req
	return newMockTurnHandle(req.LocalID, "provider-"+m.threadID, nil), nil
}
func (m *mockSession) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	if !m.caps.Has(dto.CapThreadList) {
		return nil, dto.NewCapabilityError(dto.CapThreadList, "mock")
	}
	return []dto.ThreadRef{{ID: m.threadID}}, nil
}
func (m *mockSession) ForkThread(_ context.Context, req dto.ForkRequest) (dto.ForkResult, error) {
	if !m.caps.Has(dto.CapThreadFork) {
		return dto.ForkResult{}, dto.NewCapabilityError(dto.CapThreadFork, "mock")
	}
	return dto.ForkResult{NewThreadID: req.ThreadID + "-fork"}, nil
}
func (m *mockSession) ReadHistory(_ context.Context, threadID string, limit int) ([]dto.Message, error) {
	if threadID != "" && threadID != m.threadID {
		return nil, nil
	}
	out := append([]dto.Message(nil), m.history...)
	if limit > 0 && limit < len(out) {
		out = out[len(out)-limit:]
	}
	return out, nil
}
func (m *mockSession) Configure(_ context.Context, patch dto.ThreadConfigPatch) error {
	m.config = patch
	return nil
}

type mockDriver struct {
	name      string
	session   contract.Session
	started   int
	resumed   int
	startReq  dto.StartSessionRequest
	resumeReq dto.ResumeSessionRequest
}

func (d *mockDriver) Name() string { return d.name }
func (d *mockDriver) StartSession(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	d.started, d.startReq = d.started+1, req
	return d.session, nil
}
func (d *mockDriver) ResumeSession(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	d.resumed, d.resumeReq = d.resumed+1, req
	return d.session, nil
}

func TestSessionContract_StartTurn(t *testing.T) {
	s := &mockSession{threadID: "thread-1"}
	handle, err := s.StartTurn(context.Background(), dto.TurnRequest{LocalID: "local-1", ThreadID: "thread-1"})
	if err != nil || handle.LocalID() != "local-1" || handle.ProviderID() != "provider-thread-1" || s.lastTurn.ThreadID != "thread-1" {
		t.Fatalf("start turn mismatch: handle=%v err=%v req=%+v", handle, err, s.lastTurn)
	}
	select {
	case <-handle.Done():
	default:
		t.Fatal("expected completed handle")
	}
}

func TestSessionContract_Interrupt(t *testing.T) {
	s := &mockSession{threadID: "thread-1"}
	req := dto.InterruptRequest{ThreadID: "thread-1", Source: "test"}
	if err := s.Interrupt(context.Background(), req); err != nil || s.lastInterrupt != req {
		t.Fatalf("interrupt mismatch: err=%v req=%+v", err, s.lastInterrupt)
	}
}

func TestSessionContract_ThreadID(t *testing.T) {
	s := &mockSession{threadID: "thread-123"}
	if s.ThreadID() != "thread-123" {
		t.Fatal("ThreadID mismatch")
	}
}

func TestSessionContract_Capabilities(t *testing.T) {
	caps := dto.CapabilitySet{dto.CapMessageSend: true}
	s := &mockSession{caps: caps}
	if !s.Capabilities().Has(dto.CapMessageSend) {
		t.Fatal("expected CapMessageSend")
	}
}

func TestSessionContract_ReadHistory(t *testing.T) {
	s := &mockSession{threadID: "thread-1", history: []dto.Message{
		{Role: "user", Content: "one", Timestamp: time.Unix(1, 0)},
		{Role: "assistant", Content: "two", Timestamp: time.Unix(2, 0)},
	}}
	got, err := s.ReadHistory(context.Background(), "thread-1", 1)
	if err != nil || len(got) != 1 || got[0].Content != "two" {
		t.Fatalf("history mismatch: got=%+v err=%v", got, err)
	}
}

func TestSessionContract_ListThreads_Unsupported(t *testing.T) {
	_, err := (&mockSession{threadID: "thread-1"}).ListThreads(context.Background())
	var capErr *dto.CapabilityError
	if !errors.As(err, &capErr) || capErr.Capability != dto.CapThreadList {
		t.Fatalf("expected thread list capability error, got %v", err)
	}
}

func TestSessionContract_ForkThread_Unsupported(t *testing.T) {
	_, err := (&mockSession{threadID: "thread-1"}).ForkThread(context.Background(), dto.ForkRequest{ThreadID: "thread-1"})
	var capErr *dto.CapabilityError
	if !errors.As(err, &capErr) || capErr.Capability != dto.CapThreadFork {
		t.Fatalf("expected thread fork capability error, got %v", err)
	}
}

func TestSessionContract_Configure(t *testing.T) {
	s, model := &mockSession{threadID: "thread-1"}, "sonnet"
	if err := s.Configure(context.Background(), dto.ThreadConfigPatch{Model: &model}); err != nil || s.config.Model == nil || *s.config.Model != model {
		t.Fatalf("configure mismatch: patch=%+v err=%v", s.config, err)
	}
}

func TestSessionContract_Close(t *testing.T) {
	s := &mockSession{threadID: "thread-1"}
	if err := s.Close(context.Background()); err != nil || !s.closed {
		t.Fatalf("close mismatch: closed=%v err=%v", s.closed, err)
	}
}

func TestSessionContract_ForceStop(t *testing.T) {
	s := &mockSession{threadID: "thread-1"}
	if err := s.ForceStop(); err != nil || !s.forceStopped {
		t.Fatalf("force stop mismatch: stopped=%v err=%v", s.forceStopped, err)
	}
}
