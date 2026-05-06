package turn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestOrchestrationTurnStarterStartsQueuedTurn(t *testing.T) {
	t.Parallel()

	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			if req.LocalID != "turn-1" {
				t.Fatalf("LocalID = %q, want turn-1", req.LocalID)
			}
			if req.ThreadID != "thread-1" {
				t.Fatalf("ThreadID = %q, want thread-1", req.ThreadID)
			}
			if len(req.Inputs) != 1 || req.Inputs[0].Content != "hello" {
				t.Fatalf("Inputs = %#v, want queued text input", req.Inputs)
			}
			if len(req.Skills) != 1 || req.Skills[0].Name != "debug" {
				t.Fatalf("Skills = %#v, want selected skill", req.Skills)
			}
			if !req.ManualSkillSelection {
				t.Fatal("ManualSkillSelection = false, want true")
			}
			if string(req.OutputSchema) != `{"type":"object"}` {
				t.Fatalf("OutputSchema = %s, want object schema", string(req.OutputSchema))
			}
			handle := newStubTurnHandle(req.LocalID, "provider-1")
			handle.complete(nil)
			return handle, nil
		},
	}
	starter := NewOrchestrationTurnStarter(
		NewService(silentLogger()),
		stubSessionProvider{session: session},
		nil,
	)

	turnID, err := starter.StartTurn(context.Background(), contract.TurnSubmission{
		AgentID:              "agent-1",
		ThreadID:             "agent-1",
		ExpectedTurnID:       "turn-1",
		Inputs:               []InputItem{{Type: "text", Content: "hello"}},
		SelectedSkills:       []string{"debug"},
		ManualSkillSelection: true,
		OutputSchema:         []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if turnID != "turn-1" {
		t.Fatalf("turnID = %q, want turn-1", turnID)
	}
}

func TestOrchestrationTurnStarterFallsBackToThreadRuntimeConfig(t *testing.T) {
	t.Parallel()

	assembly := &stubPromptAssemblyService{turn: contract.TurnAssembly{UserContextText: "assembled user context"}}
	session := &stubSession{
		threadID:      "thread-1",
		runtimeConfig: map[string]any{"provider": "codex-runtime"},
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			handle := newStubTurnHandle(req.LocalID, "provider-1")
			handle.complete(nil)
			return handle, nil
		},
	}
	starter := NewOrchestrationTurnStarter(
		NewServiceWithPromptAssembly(silentLogger(), assembly),
		stubSessionProvider{session: session},
		stubTurnRuntimeReader{cfg: map[string]any{
			"provider":                     "codex-thread",
			"gitRoot":                      "/thread-repo",
			"isWorktree":                   true,
			"language":                     "Japanese",
			"enabledTools":                 []any{"lsp_file", "lsp_grep"},
			"additionalWorkingDirectories": []any{"/repo/thread-extra"},
			"mcpTools":                     []any{"mcp__lsp__lsp_grep"},
			"mcpInstructions":              map[string]any{"lsp": "Use the LSP thread fallback."},
			"sessionFlags":                 map[string]any{"verification_required": true},
		}},
	)

	if _, err := starter.StartTurn(context.Background(), contract.TurnSubmission{AgentID: "agent-1", ThreadID: "thread-1", Inputs: []InputItem{{Type: "text", Content: "hello"}}}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if assembly.lastTurnInput.Provider != "codex-thread" || assembly.lastTurnInput.GitRoot != "/thread-repo" || !assembly.lastTurnInput.IsWorktree {
		t.Fatalf("last turn env context = %#v", assembly.lastTurnInput)
	}
	if assembly.lastTurnInput.Language != "Japanese" {
		t.Fatalf("last turn language = %q, want Japanese", assembly.lastTurnInput.Language)
	}
	if got := assembly.lastTurnInput.EnabledTools; len(got) != 2 || got[0] != "lsp_file" || got[1] != "lsp_grep" {
		t.Fatalf("EnabledTools = %#v, want thread-state tools", got)
	}
	if got := assembly.lastTurnInput.AdditionalWorkingDirectories; len(got) != 1 || got[0] != "/repo/thread-extra" {
		t.Fatalf("AdditionalWorkingDirectories = %#v, want thread-state dirs", got)
	}
	if assembly.lastTurnInput.MCPSnapshot.Instructions["lsp"] != "Use the LSP thread fallback." {
		t.Fatalf("MCPSnapshot.Instructions = %#v", assembly.lastTurnInput.MCPSnapshot.Instructions)
	}
	if !assembly.lastTurnInput.SessionFlags["verification_required"] {
		t.Fatalf("SessionFlags = %#v, want verification_required", assembly.lastTurnInput.SessionFlags)
	}
}

type stubSessionProvider struct {
	session contract.Session
	err     error
	get     func(string) (contract.Session, error)
}

type stubTurnRuntimeReader struct {
	cfg map[string]any
}

func (s stubTurnRuntimeReader) ReadThreadStateRuntimeConfig(context.Context, string) (map[string]any, error) {
	return s.cfg, nil
}

func (p stubSessionProvider) GetSession(agentID string) (contract.Session, error) {
	if p.get != nil {
		return p.get(agentID)
	}
	if p.err != nil {
		return nil, p.err
	}
	return p.session, nil
}

func TestOrchestrationTurnStarterReportsSessionNotReady(t *testing.T) {
	t.Parallel()

	starter := NewOrchestrationTurnStarter(
		NewService(silentLogger()),
		stubSessionProvider{err: contract.ErrSessionNotFound},
		nil,
	)

	_, err := starter.StartTurn(context.Background(), contract.TurnSubmission{
		AgentID: "agent-1",
	})
	if err == nil {
		t.Fatal("StartTurn() error = nil, want session-not-ready error")
	}
	if got := err.Error(); got != "agent session not ready, ensure agent/launch completed" {
		t.Fatalf("StartTurn() error = %q, want session-not-ready error", got)
	}
}

func TestOrchestrationTurnStarterPreservesNonSessionLookupErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("transport down")
	starter := NewOrchestrationTurnStarter(
		NewService(silentLogger()),
		stubSessionProvider{err: want},
		nil,
	)

	_, err := starter.StartTurn(context.Background(), contract.TurnSubmission{
		AgentID: "agent-1",
	})
	if !errors.Is(err, want) {
		t.Fatalf("StartTurn() error = %v, want %v", err, want)
	}
}

func TestOrchestrationTurnStarterWaitForSessionReadyEventuallySucceeds(t *testing.T) {
	t.Parallel()

	calls := 0
	starter := NewOrchestrationTurnStarter(
		NewService(silentLogger()),
		stubSessionProvider{get: func(string) (contract.Session, error) {
			calls++
			if calls < 3 {
				return nil, contract.ErrSessionNotFound
			}
			return &stubSession{}, nil
		}},
		nil,
	)
	waiter, ok := starter.(interface {
		WaitForSessionReady(context.Context, string, time.Duration) error
	})
	if !ok {
		t.Fatal("turn starter does not implement session wait contract")
	}
	if err := waiter.WaitForSessionReady(context.Background(), "agent-1", 200*time.Millisecond); err != nil {
		t.Fatalf("WaitForSessionReady() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("GetSession() calls = %d, want 3", calls)
	}
}

func TestOrchestrationTurnStarterWaitForSessionReadyTimeout(t *testing.T) {
	t.Parallel()

	starter := NewOrchestrationTurnStarter(
		NewService(silentLogger()),
		stubSessionProvider{err: contract.ErrSessionNotFound},
		nil,
	)
	waiter, ok := starter.(interface {
		WaitForSessionReady(context.Context, string, time.Duration) error
	})
	if !ok {
		t.Fatal("turn starter does not implement session wait contract")
	}
	err := waiter.WaitForSessionReady(context.Background(), "agent-1", 20*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForSessionReady() error = nil, want session-not-ready error")
	}
	if got := err.Error(); got != "agent session not ready, ensure agent/launch completed" {
		t.Fatalf("WaitForSessionReady() error = %q, want session-not-ready error", got)
	}
}
