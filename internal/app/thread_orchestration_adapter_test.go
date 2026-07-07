package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
)

func TestMCPOrchOrchestrationFacadeRequiresTools(t *testing.T) {
	facade := newMCPOrchOrchestrationFacade(
		newToolbridgeHandlerRef(),
		contract.DependencyConfig{Profile: contract.DependencyProfileProduction},
	)
	if facade == nil {
		t.Fatal("newMCPOrchOrchestrationFacade(ref, dependency) returned nil")
	}

	ctx := context.Background()
	if err := facade.LaunchAgent(ctx, thread.LaunchAgentRequest{AgentID: "agent-1"}); err != nil {
		t.Fatalf("LaunchAgent() error = %v, want nil local provider lifecycle no-op", err)
	}
	assertFacadeNotConfigured(t, "StopAgent", facade.StopAgent(ctx, "agent-1"), "toolbridge handler")
	assertFacadeNotConfigured(t, "Recover", facade.Recover(ctx, "agent-1"), "toolbridge handler")
}

func TestBindSessionGenerationReturnsTypedUnsupportedForDesktopExternalMode(t *testing.T) {
	facade := &mcpOrchOrchestrationFacade{
		dependency: contract.DependencyConfig{Profile: contract.DependencyProfileDesktopHost},
	}

	err := facade.BindSessionGeneration(context.Background(), "agent-1", 7)
	if !contract.IsDependencyModeError(
		err,
		"thread.bind_session_generation",
		contract.DependencyProfileDesktopHost,
		contract.ErrUnsupportedDependencyMode,
	) {
		t.Fatalf("BindSessionGeneration() error = %v, want desktop typed unsupported", err)
	}
}

func TestBindSessionGenerationFailsForProductionWithoutBindingPort(t *testing.T) {
	facade := &mcpOrchOrchestrationFacade{
		dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction},
	}

	err := facade.BindSessionGeneration(context.Background(), "agent-1", 7)
	if err == nil {
		t.Fatal("BindSessionGeneration() error = nil, want production missing bind failure")
	}
	if errors.Is(err, contract.ErrUnsupportedDependencyMode) {
		t.Fatalf("BindSessionGeneration() error = %v, production must not be typed unsupported", err)
	}
}

func TestMCPOrchOrchestrationFacadeDependencyConfig(t *testing.T) {
	t.Run("test_profile_typed_unsupported", func(t *testing.T) {
		facade := &mcpOrchOrchestrationFacade{
			dependency: contract.DependencyConfig{Profile: contract.DependencyProfileTest},
		}

		err := facade.BindSessionGeneration(context.Background(), "agent-1", 7)
		if !contract.IsDependencyModeError(
			err,
			"thread.bind_session_generation",
			contract.DependencyProfileTest,
			contract.ErrUnsupportedDependencyMode,
		) {
			t.Fatalf("BindSessionGeneration() error = %v, want test typed unsupported", err)
		}
	})

	t.Run("empty_profile_fails_fast", func(t *testing.T) {
		facade := &mcpOrchOrchestrationFacade{}

		err := facade.BindSessionGeneration(context.Background(), "agent-1", 7)
		if err == nil || !strings.Contains(err.Error(), "dependency profile") {
			t.Fatalf("BindSessionGeneration() error = %v, want dependency profile failure", err)
		}
		if errors.Is(err, contract.ErrUnsupportedDependencyMode) {
			t.Fatalf("BindSessionGeneration() error = %v, empty profile must not be typed unsupported", err)
		}
	})
}

func TestMCPOrchOrchestrationFacadeConstructorInjectsDependencyConfig(t *testing.T) {
	dependency := contract.DependencyConfig{Profile: contract.DependencyProfileDesktopHost}
	facade := newMCPOrchOrchestrationFacade(newToolbridgeHandlerRef(), dependency)

	err := facade.BindSessionGeneration(context.Background(), "agent-1", 7)
	if !contract.IsDependencyModeError(
		err,
		"thread.bind_session_generation",
		contract.DependencyProfileDesktopHost,
		contract.ErrUnsupportedDependencyMode,
	) {
		t.Fatalf("BindSessionGeneration() error = %v, want desktop typed unsupported from injected dependency", err)
	}
}

func TestMCPOrchOrchestrationFacadeFXGraphInjectsDependencyConfig(t *testing.T) {
	var facade thread.OrchestrationFacade
	app := fxtest.New(t,
		fx.Provide(
			newToolbridgeHandlerRef,
			func() contract.DependencyConfig {
				return contract.DependencyConfig{Profile: contract.DependencyProfileDesktopHost}
			},
			fx.Annotate(newMCPOrchOrchestrationFacade, fx.As(new(thread.OrchestrationFacade))),
		),
		fx.Populate(&facade),
	)
	app.RequireStart()
	t.Cleanup(func() { app.RequireStop() })

	err := facade.BindSessionGeneration(context.Background(), "agent-1", 7)
	if !contract.IsDependencyModeError(
		err,
		"thread.bind_session_generation",
		contract.DependencyProfileDesktopHost,
		contract.ErrUnsupportedDependencyMode,
	) {
		t.Fatalf("BindSessionGeneration() error = %v, want desktop typed unsupported from Fx graph", err)
	}
}

func TestMCPOrchDependencyConfigRequiresProfile(t *testing.T) {
	facade := newMCPOrchOrchestrationFacade(newToolbridgeHandlerRef(), contract.DependencyConfig{})

	err := facade.BindSessionGeneration(context.Background(), "agent-1", 7)
	if err == nil || !strings.Contains(err.Error(), "dependency profile") {
		t.Fatalf("BindSessionGeneration() error = %v, want dependency profile failure", err)
	}
}

func TestMCPOrchOrchestrationFacadeLaunchAgentDoesNotCallToolbridge(t *testing.T) {
	t.Parallel()

	caller := &recordingThreadOrchToolCaller{}
	facade := &mcpOrchOrchestrationFacade{tools: caller}

	err := facade.LaunchAgent(context.Background(), thread.LaunchAgentRequest{
		AgentID:     " agent-1 ",
		Name:        " Worker ",
		ParentID:    " parent-1 ",
		AgentType:   " assistant ",
		MemoryScope: " project ",
		Cwd:         " /repo/project ",
	})
	if err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	if len(caller.params) != 0 {
		t.Fatalf("LaunchAgent called toolbridge with params=%s, want no-op", string(caller.params))
	}
}

func assertFacadeNotConfigured(t *testing.T, op string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s() error = nil, want not configured error", op)
	}
	if !strings.Contains(err.Error(), "not configured") || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s() error = %q, want not configured failure containing %q", op, err.Error(), want)
	}
}

type recordingThreadOrchToolCaller struct {
	params json.RawMessage
}

func (c *recordingThreadOrchToolCaller) HandleToolCall(_ context.Context, msg contract.ToolCallRawMessage) (any, error) {
	c.params = append(c.params[:0], msg.Params...)
	return &toolbridge.ToolCallResult{Success: true}, nil
}
