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

func TestClassifyToolErrorLaunchRequestInvalid(t *testing.T) {
	for _, message := range []string{
		"name is required",
		"context_mode=focused requires non-empty context field",
		"invalid memory_scope \"shared\": must be project, user, or local",
	} {
		t.Run(message, func(t *testing.T) {
			env := NewToolErrorEnvelope("launch_agent", errors.New(message))
			assertLaunchToolError(t, env, "launch_request_invalid")
		})
	}
}

func TestClassifyToolErrorDAGApplyOpsInvalidInput(t *testing.T) {
	env := NewToolErrorEnvelope(
		"task_dag_apply_ops",
		errors.New("orchestration: apply_ops invalid request: ops[0]: missing 'op' discriminator"),
	)
	if env.Code != "invalid_input" {
		t.Fatalf("Code = %q, want invalid_input", env.Code)
	}
	if env.Retryable {
		t.Fatal("Retryable = true, want false")
	}
	if strings.Contains(strings.ToLower(env.Hint), "lsp") {
		t.Fatalf("Hint = %q, must not mention lsp", env.Hint)
	}
}

func TestClassifyToolErrorDAGNoRowsDoesNotUseLSP(t *testing.T) {
	env := NewToolErrorEnvelope(
		"task_get_dag",
		errors.New("get dag version for detail: get_version task_dag: no rows in result set"),
	)
	if env.Code != "file_not_found" {
		t.Fatalf("Code = %q, want file_not_found", env.Code)
	}
	if env.Retryable {
		t.Fatal("Retryable = true, want false")
	}
	if strings.Contains(strings.ToLower(env.Hint), "lsp") {
		t.Fatalf("Hint = %q, must not mention lsp", env.Hint)
	}
}

func TestClassifyToolErrorDatabaseUndefinedTableDoesNotUseLSP(t *testing.T) {
	env := NewToolErrorEnvelope(
		"task_list_dags",
		errors.New(`ERROR: relation "task_dags" does not exist (SQLSTATE 42P01)`),
	)
	if env.Code != "database_schema_missing" {
		t.Fatalf("Code = %q, want database_schema_missing", env.Code)
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

func TestClassifyToolErrorTaskUpdateNodeTransitionInvalidInput(t *testing.T) {
	env := NewToolErrorEnvelope(
		"task_update_node",
		errors.New(`transition: "ready" → "done" 非法 (见 nodeexec/status.go legalTransitions)`),
	)
	if env.Code != "invalid_input" {
		t.Fatalf("Code = %q, want invalid_input", env.Code)
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

func TestClassifyToolErrorNonUpdateNodeTransitionDoesNotBecomeInvalidInput(t *testing.T) {
	env := NewToolErrorEnvelope(
		"task_start_dag",
		errors.New(`transition: "ready" → "done" 非法 (见 nodeexec/status.go legalTransitions)`),
	)
	if env.Code == "invalid_input" {
		t.Fatalf("Code = %q, want non-invalid_input for non task_update_node transition errors", env.Code)
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

func TestClassifyToolErrorRegularFileRequired(t *testing.T) {
	env := NewToolErrorEnvelope("file", errors.New(`file_path "/Applications/Super Dolphin.app/Contents/Resources" must reference a regular file`))
	if env.Code != "path_invalid" {
		t.Fatalf("Code = %q, want path_invalid", env.Code)
	}
	if env.Retryable {
		t.Fatal("Retryable = true, want false")
	}
	if strings.Contains(strings.ToLower(env.Hint), "language server") {
		t.Fatalf("Hint = %q, must not describe this as language-server availability", env.Hint)
	}
}

func TestClassifyToolErrorDatabaseSchemaMissing(t *testing.T) {
	env := NewToolErrorEnvelope("task_get_dag", errors.New(`pq: relation "task_dags" does not exist`))
	if env.Code != "database_schema_missing" {
		t.Fatalf("Code = %q, want database_schema_missing", env.Code)
	}
	if env.Retryable {
		t.Fatal("Retryable = true, want false")
	}
	if strings.Contains(strings.ToLower(env.Hint), "language server") {
		t.Fatalf("Hint = %q, must not describe DB schema errors as LSP availability", env.Hint)
	}
}

func TestClassifyToolErrorTaskInvalidInput(t *testing.T) {
	for _, message := range []string{
		`orchestration: apply_ops invalid request: base_version mismatch`,
		`transition: same state "done" (上层应去重；不允许 idempotent update)`,
	} {
		t.Run(message, func(t *testing.T) {
			env := NewToolErrorEnvelope("task_update_node", errors.New(message))
			if env.Code != "invalid_input" {
				t.Fatalf("Code = %q, want invalid_input", env.Code)
			}
			if env.Retryable {
				t.Fatal("Retryable = true, want false")
			}
			if strings.Contains(strings.ToLower(env.Hint), "language server") {
				t.Fatalf("Hint = %q, must not describe invalid task input as LSP availability", env.Hint)
			}
		})
	}
}

func TestClassifyToolErrorLSPStartingRemainsUnavailable(t *testing.T) {
	env := NewToolErrorEnvelope("grep", errors.New("language server is starting"))
	if env.Code != "lsp_unavailable" {
		t.Fatalf("Code = %q, want lsp_unavailable", env.Code)
	}
	if !env.Retryable {
		t.Fatal("Retryable = false, want true")
	}
}
