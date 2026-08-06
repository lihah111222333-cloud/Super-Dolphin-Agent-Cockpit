package turn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestOrchestrationTurnStarterStartsQueuedTurn(t *testing.T) {
	t.Parallel()

	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			require.Equal(t, "turn-1", req.LocalID)
			require.Equal(t, "thread-1", req.ThreadID)
			require.Equal(t, "/thread/worktree", req.CWD)
			require.Len(t, req.Inputs, 1)
			require.Equal(t, "hello", req.Inputs[0].Content)
			require.Len(t, req.Skills, 1)
			require.Equal(t, "debug", req.Skills[0].Name)
			require.True(t, req.ManualSkillSelection)
			require.JSONEq(t, `{"type":"object"}`, string(req.OutputSchema))
			handle := newStubTurnHandle(req.LocalID, "provider-1")
			handle.complete(nil)
			return handle, nil
		},
	}
	starter := NewOrchestrationTurnStarter(
		NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()),
		stubSessionProvider{session: session},
		stubTurnRuntimeReader{cfg: map[string]any{"cwd": "/thread/worktree"}},
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
	require.NoError(t, err)
	require.Equal(t, "turn-1", turnID)
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
		NewServiceWithPromptAssembly(silentLogger(), assembly, NewToolResultRuntime()),
		stubSessionProvider{session: session},
		stubTurnRuntimeReader{cfg: map[string]any{
			"cwd":                          "/thread/worktree",
			"provider":                     "codex-thread",
			"gitRoot":                      "/thread-repo",
			"isWorktree":                   true,
			"language":                     "Japanese",
			"enabledTools":                 []any{"file", "grep"},
			"additionalWorkingDirectories": []any{"/repo/thread-extra"},
			"mcpTools":                     []any{"mcp__lsp__grep"},
			"mcpInstructions":              map[string]any{"lsp": "Use the LSP thread fallback."},
			"sessionFlags":                 map[string]any{"verification_required": true},
		}},
	)

	_, err := starter.StartTurn(context.Background(), contract.TurnSubmission{AgentID: "agent-1", ThreadID: "thread-1", Inputs: []InputItem{{Type: "text", Content: "hello"}}})
	require.NoError(t, err)
	require.Equal(t, "codex-thread", assembly.lastTurnInput.Provider)
	require.Equal(t, "/thread/worktree", assembly.lastTurnInput.CWD)
	require.Equal(t, "/thread-repo", assembly.lastTurnInput.GitRoot)
	require.True(t, assembly.lastTurnInput.IsWorktree)
	require.Equal(t, "Japanese", assembly.lastTurnInput.Language)
	require.Equal(t, []string{"file", "grep"}, assembly.lastTurnInput.EnabledTools)
	require.Equal(t, []string{"/repo/thread-extra"}, assembly.lastTurnInput.AdditionalWorkingDirectories)
	require.Equal(t, "Use the LSP thread fallback.", assembly.lastTurnInput.MCPSnapshot.Instructions["lsp"])
	require.True(t, assembly.lastTurnInput.SessionFlags["verification_required"])
}

func TestTurnModuleInjectsThreadStateConfigReader(t *testing.T) {
	t.Parallel()

	assembly := &stubPromptAssemblyService{}
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			handle := newStubTurnHandle(req.LocalID, "provider-1")
			handle.complete(nil)
			return handle, nil
		},
	}
	var starter contract.OrchestrationTurnStarter
	app := fxtest.New(t,
		Module,
		fx.Supply(silentLogger()),
		fx.Provide(func() contract.PromptAssemblyService { return assembly }),
		fx.Provide(func() SessionProvider { return stubSessionProvider{session: session} }),
		fx.Provide(func() contract.ThreadStateConfigReader {
			return stubTurnRuntimeReader{cfg: map[string]any{
				"cwd":      "/thread/worktree",
				"provider": "codex-thread",
			}}
		}),
		fx.Populate(&starter),
	)
	app.RequireStart()
	t.Cleanup(func() { app.RequireStop() })

	_, err := starter.StartTurn(context.Background(), contract.TurnSubmission{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Inputs:   []InputItem{{Type: "text", Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "/thread/worktree", assembly.lastTurnInput.CWD)
	require.Equal(t, "codex-thread", assembly.lastTurnInput.Provider)
}

func TestTurnModuleRequiresThreadStateConfigReader(t *testing.T) {
	t.Parallel()

	var starter contract.OrchestrationTurnStarter
	app := fx.New(
		Module,
		fx.Supply(silentLogger()),
		fx.Provide(func() contract.PromptAssemblyService { return &stubPromptAssemblyService{} }),
		fx.Provide(func() SessionProvider { return stubSessionProvider{session: &stubSession{threadID: "thread-1"}} }),
		fx.Provide(func() contract.ApprovalResponder { return noopApprovalResponder{} }),
		fx.Populate(&starter),
	)
	err := app.Err()
	require.Error(t, err)
	require.Contains(t, err.Error(), "contract.ThreadStateConfigReader")
}

type stubSessionProvider struct {
	session contract.Session
	err     error
	get     func(string) (contract.Session, error)
}

type noopApprovalResponder struct{}

func (noopApprovalResponder) Respond(contract.ApprovalIdentity, contract.ApprovalDecision) error {
	return nil
}

type stubTurnRuntimeReader struct {
	cfg map[string]any
	err error
}

func (s stubTurnRuntimeReader) ReadThreadStateRuntimeConfig(context.Context, string) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.cfg, nil
}

func TestOrchestrationTurnStarterFailsFastOnRuntimeConfigError(t *testing.T) {
	t.Parallel()

	runtimeErr := errors.New("runtime config store failed")
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			t.Fatal("StartTurn should not be called after runtime config error")
			return nil, nil
		},
	}
	starter := NewOrchestrationTurnStarter(
		NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()),
		stubSessionProvider{session: session},
		stubTurnRuntimeReader{err: runtimeErr},
	)

	_, err := starter.StartTurn(context.Background(), contract.TurnSubmission{AgentID: "agent-1", ThreadID: "thread-1", Inputs: []InputItem{{Type: "text", Content: "hello"}}})
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("StartTurn() error = %v, want %v", err, runtimeErr)
	}
}

func TestOrchestrationTurnStarterRejectsSessionOnlyCWD(t *testing.T) {
	t.Parallel()

	called := false
	session := &stubSession{
		threadID:      "thread-1",
		runtimeConfig: map[string]any{"cwd": "/session/worktree"},
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			called = true
			return nil, nil
		},
	}
	starter := NewOrchestrationTurnStarter(
		NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()),
		stubSessionProvider{session: session},
		stubTurnRuntimeReader{cfg: map[string]any{"provider": "codex-thread"}},
	)

	_, err := starter.StartTurn(context.Background(), contract.TurnSubmission{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Inputs:   []InputItem{{Type: "text", Content: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "thread runtime config does not define cwd")
	require.False(t, called)
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
		NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()),
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
		NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()),
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
		NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()),
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
		NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()),
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
