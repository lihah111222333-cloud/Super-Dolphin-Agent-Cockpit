package common

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestClassifyToolErrorLaunchCWDRequired(t *testing.T) {
	err := fmt.Errorf("%w: launch_agent cwd is required", contract.ErrLaunchCWDRequired)
	for _, name := range []string{"launch_agent", "orchestration_launch_agent"} {
		env := NewToolErrorEnvelope(name, err)
		assertLaunchToolError(t, env, "cwd_required")
	}
}

func TestClassifyToolErrorLaunchCWDInvalid(t *testing.T) {
	err := fmt.Errorf("%w: launch_agent cwd must be explicit", contract.ErrLaunchCWDInvalid)
	env := NewToolErrorEnvelope("launch_agent", err)
	assertLaunchToolError(t, env, "cwd_invalid")
}

func TestClassifyToolErrorHistoricalCWDRequiredString(t *testing.T) {
	env := NewToolErrorEnvelope("launch_agent", errors.New("thread start cwd is required"))
	assertLaunchToolError(t, env, "cwd_required")
}

func TestClassifyToolErrorLaunchProviderRequired(t *testing.T) {
	for _, name := range []string{"launch_agent", "orchestration_launch_agent"} {
		env := NewToolErrorEnvelope(name, errors.New("provider is required"))
		assertLaunchToolError(t, env, "provider_required")
	}
}

func TestClassifyToolErrorLaunchProviderInvalid(t *testing.T) {
	for _, name := range []string{"launch_agent", "orchestration_launch_agent"} {
		env := NewToolErrorEnvelope(name, errors.New(`invalid provider "openai"`))
		assertLaunchToolError(t, env, "provider_invalid")
	}
}

func TestClassifyToolErrorLaunchTaskHandoffInvalid(t *testing.T) {
	for _, message := range []string{
		`root task id missing on thread "agent-parent"`,
		`task handoff title is required for task "task-demo"`,
		`task handoff file is required for task "task-demo"`,
		`task handoff config "taskId" must be a string`,
		`task handoff config "continueTask" must be a bool`,
	} {
		t.Run(message, func(t *testing.T) {
			env := NewToolErrorEnvelope("launch_agent", errors.New(message))
			assertLaunchToolError(t, env, "task_handoff_invalid")
		})
	}
}

func TestClassifyToolErrorLaunchRequestInvalid(t *testing.T) {
	for _, message := range []string{
		"name is required",
		"invalid memory_scope \"shared\": must be project, user, or local",
	} {
		t.Run(message, func(t *testing.T) {
			env := NewToolErrorEnvelope("launch_agent", errors.New(message))
			assertLaunchToolError(t, env, "launch_request_invalid")
		})
	}
}

func assertLaunchToolError(t *testing.T, env ToolErrorEnvelope, wantCode string) {
	t.Helper()
	if env.Code != wantCode {
		t.Fatalf("Code = %q, want %s", env.Code, wantCode)
	}
	if env.Retryable {
		t.Fatal("Retryable = true, want false")
	}
	hint := strings.ToLower(env.Hint)
	for _, forbidden := range []string{"lsp", "language server"} {
		if strings.Contains(hint, forbidden) {
			t.Fatalf("Hint = %q, must not mention %s", env.Hint, forbidden)
		}
	}
}

func TestClassifyToolErrorGenericCWDRequiredStringDoesNotUseLaunchHint(t *testing.T) {
	for _, err := range []error{errors.New("cwd is required"), contract.ErrLaunchCWDRequired} {
		env := NewToolErrorEnvelope("install_skill", err)
		if env.Code == "cwd_required" {
			t.Fatalf("Code = %q, want non-launch cwd error to avoid launch_agent hint", env.Code)
		}
		if strings.Contains(env.Hint, "parent_id") {
			t.Fatalf("Hint = %q, must not use launch_agent parent_id guidance", env.Hint)
		}
	}
}
