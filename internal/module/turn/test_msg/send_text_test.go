package testmsg

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	moduleturn "github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestServiceSendsTextMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input moduleturn.PrepareInput
		want  string
	}{
		{
			name:  "prompt text",
			input: moduleturn.PrepareInput{Prompt: "  hello from prompt  "},
			want:  "hello from prompt",
		},
		{
			name: "input text",
			input: moduleturn.PrepareInput{
				Inputs: []moduleturn.InputItem{{Type: "text", Content: "  hello from input  "}},
			},
			want: "hello from input",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			session := newMessageSession()
			svc := moduleturn.NewServiceWithPromptAssembly(pkglogger.Get(), &messagePromptAssembly{})
			t.Cleanup(func() {
				if shutdowner, ok := svc.(interface{ Shutdown() }); ok {
					shutdowner.Shutdown()
				}
			})

			req, err := svc.PrepareTurn(context.Background(), session, tt.input)
			if err != nil {
				t.Fatalf("PrepareTurn() error = %v", err)
			}
			handle, err := svc.StartTurn(context.Background(), session, req)
			if err != nil {
				t.Fatalf("StartTurn() error = %v", err)
			}
			t.Cleanup(func() { session.complete(nil) })

			if handle.LocalID() != req.LocalID {
				t.Fatalf("handle.LocalID() = %q, want %q", handle.LocalID(), req.LocalID)
			}
			if len(session.sent) != 1 {
				t.Fatalf("sent requests = %d, want 1", len(session.sent))
			}
			assertSentTextTurn(t, session.sent[0], req.LocalID, tt.want)
		})
	}
}

func assertSentTextTurn(t *testing.T, req dto.TurnRequest, wantLocalID string, wantText string) {
	t.Helper()

	if req.LocalID != wantLocalID {
		t.Fatalf("sent LocalID = %q, want %q", req.LocalID, wantLocalID)
	}
	if req.ThreadID != "thread-1" {
		t.Fatalf("sent ThreadID = %q, want thread-1", req.ThreadID)
	}
	if len(req.Inputs) != 1 {
		t.Fatalf("sent Inputs = %#v, want one text input", req.Inputs)
	}
	if req.Inputs[0].Type != "text" || req.Inputs[0].Content != wantText {
		t.Fatalf("sent input = %#v, want text %q", req.Inputs[0], wantText)
	}
}

type messagePromptAssembly struct{}

func (*messagePromptAssembly) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (*messagePromptAssembly) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (*messagePromptAssembly) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (*messagePromptAssembly) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

type messageSession struct {
	threadID string
	caps     dto.CapabilitySet
	handle   *messageHandle
	sent     []dto.TurnRequest
}

func newMessageSession() *messageSession {
	return &messageSession{
		threadID: "thread-1",
		caps:     dto.CapabilitySet{dto.CapMessageSend: true},
		handle:   &messageHandle{localID: "turn-local", providerID: "provider-1", done: make(chan struct{})},
	}
}

func (s *messageSession) ThreadID() string {
	return s.threadID
}

func (s *messageSession) RolloutPath() string {
	return ""
}

func (s *messageSession) Capabilities() dto.CapabilitySet {
	return s.caps
}

func (s *messageSession) StartTurn(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	s.sent = append(s.sent, req)
	s.handle.localID = req.LocalID
	return s.handle, nil
}

func (s *messageSession) Interrupt(context.Context, dto.InterruptRequest) error {
	return errors.New("Interrupt is not used by this test")
}

func (s *messageSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return errors.New("ForceComplete is not used by this test")
}

func (s *messageSession) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, errors.New("ListThreads is not used by this test")
}

func (s *messageSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, errors.New("ForkThread is not used by this test")
}

func (s *messageSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, errors.New("ReadHistory is not used by this test")
}

func (s *messageSession) Configure(context.Context, dto.ThreadConfigPatch) error {
	return errors.New("Configure is not used by this test")
}

func (s *messageSession) Close(context.Context) error {
	return nil
}

func (s *messageSession) ForceStop() error {
	return nil
}

func (s *messageSession) complete(err error) {
	s.handle.complete(err)
}

type messageHandle struct {
	localID    string
	providerID string
	done       chan struct{}
	err        error
}

func (h *messageHandle) LocalID() string {
	return h.localID
}

func (h *messageHandle) ProviderID() string {
	return h.providerID
}

func (h *messageHandle) Done() <-chan struct{} {
	return h.done
}

func (h *messageHandle) Err() error {
	return h.err
}

func (h *messageHandle) complete(err error) {
	h.err = err
	close(h.done)
}
