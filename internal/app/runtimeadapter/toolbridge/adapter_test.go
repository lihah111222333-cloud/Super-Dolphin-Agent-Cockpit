package toolbridgeadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	mcpserver "github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	uipreferencestore "github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"go.uber.org/fx"
)

func TestToolCallBindingFromStoreProjectsAndTrimsFields(t *testing.T) {
	t.Parallel()

	got := toolCallBindingFromStore(&bindingstore.Binding{
		AgentID: " agent-child ", Provider: " codex ", ProviderThreadID: " provider-thread ",
		CodexThreadID: " codex-thread ", Cwd: " /repo ", ParentAgentID: " agent-root ",
		CodexHome: " /codex ", CodexInstanceKey: " instance ", CodexModelProvider: " openai ",
	})
	if got.AgentID != "agent-child" || got.Provider != "codex" || got.ProviderThreadID != "provider-thread" ||
		got.CodexThreadID != "codex-thread" || got.CWD != "/repo" || got.ParentAgentID != "agent-root" ||
		got.CodexHome != "/codex" || got.CodexInstanceKey != "instance" || got.CodexModelProvider != "openai" {
		t.Fatalf("toolCallBindingFromStore() = %#v, want trimmed field projection", got)
	}
}

func TestToolbridgeReadySessionStarterBlocksCodexBeforeInnerStarter(t *testing.T) {
	inner := &recordingSessionStarter{}
	starter := toolbridgeReadySessionStarter{inner: inner, readiness: &codexToolbridgeReadinessProbe{}}

	_, err := starter.StartSession(context.Background(), dto.StartSessionRequest{Provider: "codex"})
	if err == nil {
		t.Fatal("StartSession() error = nil, want codex toolbridge readiness failure")
	}
	if !strings.Contains(err.Error(), "codex binding is not ready") {
		t.Fatalf("StartSession() error = %v, want codex binding readiness failure", err)
	}
	if inner.startCalls != 0 {
		t.Fatalf("inner StartSession calls = %d, want 0 before readiness", inner.startCalls)
	}
}

func TestToolbridgeReadySessionStarterDelegatesAfterCodexReady(t *testing.T) {
	inner := &recordingSessionStarter{}
	readiness := &codexToolbridgeReadinessProbe{}
	readiness.markReady()
	starter := toolbridgeReadySessionStarter{inner: inner, readiness: readiness}

	if _, err := starter.StartSession(context.Background(), dto.StartSessionRequest{Provider: "codex"}); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if inner.startCalls != 1 {
		t.Fatalf("inner StartSession calls = %d, want 1 after readiness", inner.startCalls)
	}
}

func TestModuleRequiresCodexBindingCriticalDependencies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		opts []fx.Option
		want string
	}{
		{
			name: "missing ServerManager",
			opts: []fx.Option{fx.Supply(newTestCodexDriverFactory()), fx.Supply(toolbridge.NewHandlerForTesting(nil, nil))},
			want: "*codexapp.ServerManager",
		},
		{
			name: "missing DriverFactory",
			opts: []fx.Option{fx.Supply(&codexapp.ServerManager{}), fx.Supply(toolbridge.NewHandlerForTesting(nil, nil))},
			want: "*codexapp.DriverFactory",
		},
		{
			name: "missing toolbridge Handler",
			opts: []fx.Option{fx.Supply(&codexapp.ServerManager{}), fx.Supply(newTestCodexDriverFactory())},
			want: "*toolbridge.Handler",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := append([]fx.Option{Module, adapterDependencyOptions(), fx.Provide(func() contract.SessionStarter { return testSessionStarter{} })}, tc.opts...)
			err := fx.ValidateApp(opts...)
			if err == nil {
				t.Fatalf("fx.ValidateApp() error = nil, want missing %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fx.ValidateApp() error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestModuleWrapsSessionStarterAtRootScope(t *testing.T) {
	t.Parallel()

	inner := testSessionStarter{}
	var starter contract.SessionStarter
	app := fx.New(
		Module,
		adapterDependencyOptions(),
		fx.Provide(func() contract.SessionStarter { return inner }),
		fx.Supply(&codexapp.ServerManager{}),
		fx.Supply(newTestCodexDriverFactory()),
		fx.Supply(toolbridge.NewHandlerForTesting(nil, nil)),
		fx.Populate(&starter),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	if starter == nil {
		t.Fatal("contract.SessionStarter = nil, want readiness wrapped starter")
	}
	if starter == contract.SessionStarter(inner) {
		t.Fatal("contract.SessionStarter was not wrapped with toolbridge readiness")
	}
}

func adapterDependencyOptions() fx.Option {
	return fx.Provide(
		func() bindingstore.Store { return bindingStoreStub{} },
		func() threadstore.Store { return threadStoreStub{} },
		func() uipreferencestore.Store { return uiPreferenceStoreStub{} },
		func() mcpserver.Service { return mcpServerServiceStub{} },
	)
}

func newTestCodexDriverFactory() *codexapp.DriverFactory {
	return codexapp.NewDriverFactory(nil, nil, nil, nil, nil, nil, nil, nil)
}

type recordingSessionStarter struct {
	startCalls  int
	resumeCalls int
}

func (s *recordingSessionStarter) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	s.startCalls++
	return nil, nil
}

func (s *recordingSessionStarter) ResumeSession(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	s.resumeCalls++
	return nil, nil
}

type testSessionStarter struct{}

func (testSessionStarter) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	return nil, nil
}

func (testSessionStarter) ResumeSession(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	return nil, nil
}

type bindingStoreStub struct{ bindingstore.Store }
type threadStoreStub struct{ threadstore.Store }
type uiPreferenceStoreStub struct{ uipreferencestore.Store }
type mcpServerServiceStub struct{ mcpserver.Service }

func (uiPreferenceStoreStub) List(context.Context, string) ([]uipreferencestore.UIPreference, error) {
	return nil, nil
}

func (threadStoreStub) GetByThreadID(context.Context, string) (*threadstore.Thread, error) {
	return &threadstore.Thread{ConfigOverride: json.RawMessage(`{}`)}, nil
}
