package turn

import (
	"context"
	"errors"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildPrepareInputSupportsExpandedFields(t *testing.T) {
	t.Parallel()

	session := &rpcHelperSession{caps: dto.CapabilitySet{dto.CapMessageSend: true}}
	items, inputSkills := buildTurnStartInputs([]turnInputItemParams{
		{Type: "text", Text: "typed text"},
		{Type: "skill", Name: "debug"},
		{Type: "mention", Path: "doc.md"},
	})
	input := buildPrepareInput(prepareInputSpec{
		Prompt:               "flat prompt",
		Images:               []string{"img-1"},
		Files:                []string{"file-1"},
		Inputs:               items,
		ManualSkillSelection: true,
		CWD:                  "/tmp/work",
		Model:                "gpt-5",
		Effort:               "high",
		OutputSchema:         []byte(`{"type":"object"}`),
	}, prepareSkillSpec{
		Selected: []string{"review", "debug"},
		Derived:  inputSkills,
	}, session.Capabilities())

	if len(input.Inputs) != 2 {
		t.Fatalf("len(input.Inputs) = %d, want 2", len(input.Inputs))
	}
	if input.Inputs[0].Type != "text" || input.Inputs[0].Content != "typed text" {
		t.Fatalf("first input = %#v, want typed text input", input.Inputs[0])
	}
	if input.Inputs[1].Type != "mention" || input.Inputs[1].Path != "doc.md" {
		t.Fatalf("second input = %#v, want mention input", input.Inputs[1])
	}
	if got := skillNames(input.Skills); len(got) != 2 || got[0] != "review" || got[1] != "debug" {
		t.Fatalf("skill names = %#v, want [review debug]", got)
	}
	if !input.ManualSkillSelection || input.CWD != "/tmp/work" || string(input.OutputSchema) != `{"type":"object"}` {
		t.Fatalf("prepare input = %#v", input)
	}
}

func TestResolveTurnSessionRejectsNilSession(t *testing.T) {
	t.Parallel()

	_, err := resolveTurnSession(context.Background(), rpcHelperResolver{})
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("resolveTurnSession() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Message != "thread session is not available; start or resume the thread first" {
		t.Fatalf("rpcErr.Message = %q", rpcErr.Message)
	}
}

func TestApplyTurnStartConfigUsesApprovalPolicy(t *testing.T) {
	t.Parallel()

	session := &rpcHelperSession{}
	err := applyTurnStartConfig(context.Background(), session, turnStartParams{ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatalf("applyTurnStartConfig() error = %v", err)
	}
	if session.lastPatch.Approvals == nil || *session.lastPatch.Approvals != "on-request" {
		t.Fatalf("last approvals patch = %#v, want on-request", session.lastPatch.Approvals)
	}
}

type rpcHelperResolver struct {
	session contract.Session
	err     error
}

func (r rpcHelperResolver) ResolveSession(context.Context, string) (contract.Session, error) {
	return r.session, r.err
}

type rpcHelperSession struct {
	caps      dto.CapabilitySet
	lastPatch dto.ThreadConfigPatch
}

func (s rpcHelperSession) ThreadID() string { return "thread-1" }

func (s rpcHelperSession) RolloutPath() string { return "" }

func (s rpcHelperSession) Capabilities() dto.CapabilitySet { return s.caps }

func (s rpcHelperSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, errors.New("unexpected StartTurn call")
}

func (s rpcHelperSession) Steer(context.Context, dto.SteerRequest) error { return nil }

func (s rpcHelperSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (s rpcHelperSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error { return nil }

func (s rpcHelperSession) ListThreads(context.Context) ([]dto.ThreadRef, error) { return nil, nil }

func (s rpcHelperSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}

func (s rpcHelperSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}

func (s *rpcHelperSession) Configure(_ context.Context, patch dto.ThreadConfigPatch) error {
	s.lastPatch = patch
	return nil
}

func (s rpcHelperSession) Close(context.Context) error { return nil }

func (s rpcHelperSession) ForceStop() error { return nil }
