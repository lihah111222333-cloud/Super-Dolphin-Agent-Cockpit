# P1a Codex Identity Inheritance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make managed `launch_agent` / `orchestration_launch_agent` inherit the full parent Codex identity.

**Architecture:** Reuse the existing `injectManagedLaunchArgs` helper and existing `setArgStringIfMissing` behavior. Add only the two missing snake_case identity args, keeping explicit caller values authoritative.

**Tech Stack:** Go, existing `internal/platform/toolbridge` tests, repository `test_with_guard.ps1`.

---

## File Structure

- Modify: `internal/platform/toolbridge/handler_launch_args.go`
  - Add `codex_home` and `codex_instance_key` to the existing injection list.
- Modify: `internal/platform/toolbridge/handler_launch_args_test.go`
  - Expand helper-level tests to cover full identity injection and no-overwrite behavior.
- Modify: `internal/platform/toolbridge/handler_test.go`
  - Update the orchestration launch expected args to include full Codex identity.

### Task 1: Lock Helper Behavior With Tests

**Files:**
- Modify: `internal/platform/toolbridge/handler_launch_args_test.go`

- [x] **Step 1: Replace the helper tests with full identity coverage**

```go
func TestInjectManagedLaunchArgsAddsCodexIdentity(t *testing.T) {
	args := map[string]any{}

	changed := injectManagedLaunchArgs(args, toolCallBinding{
		AgentID:            "agent-parent",
		CodexHome:          " /Users/test/.codex ",
		CodexInstanceKey:   " default ",
		CodexModelProvider: " openai ",
	}, "codex", "gpt-5.5", "xhigh")

	if !changed {
		t.Fatal("injectManagedLaunchArgs changed = false, want true")
	}
	for key, want := range map[string]string{
		"codex_home":           "/Users/test/.codex",
		"codex_instance_key":   "default",
		"codex_model_provider": "openai",
	} {
		if got := mapString(args, key); got != want {
			t.Fatalf("%s = %q, want %q; args=%#v", key, got, want, args)
		}
	}
}

func TestInjectManagedLaunchArgsDoesNotOverwriteCodexIdentity(t *testing.T) {
	args := map[string]any{
		"codex_home":           "/custom/.codex",
		"codex_instance_key":   "custom-key",
		"codex_model_provider": "custom-provider",
	}

	injectManagedLaunchArgs(args, toolCallBinding{
		AgentID:            "agent-parent",
		CodexHome:          "/Users/test/.codex",
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}, "codex", "gpt-5.5", "xhigh")

	for key, want := range map[string]string{
		"codex_home":           "/custom/.codex",
		"codex_instance_key":   "custom-key",
		"codex_model_provider": "custom-provider",
	} {
		if got := mapString(args, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
```

- [x] **Step 2: Run the focused helper test and confirm it fails**

Run:

```powershell
go test ./internal/platform/toolbridge -run TestInjectManagedLaunchArgs -count=1
```

Expected: FAIL because `codex_home` and `codex_instance_key` are not injected yet.

### Task 2: Lock Orchestration Launch Behavior

**Files:**
- Modify: `internal/platform/toolbridge/handler_test.go`

- [x] **Step 1: Add the missing expected launch args**

```go
wantArgs := mustRawJSON(t, map[string]any{
	"codex_home":           "/Users/test/.codex",
	"codex_instance_key":   "default",
	"codex_model_provider": "openai",
	"name":                 "idle-agent",
	"parent_id":            "agent-parent",
	"provider":             "codex",
})
```

- [x] **Step 2: Run the focused orchestration test and confirm it fails**

Run:

```powershell
go test ./internal/platform/toolbridge -run TestToolBridge_OrchestrationLaunchInheritsParentContextFromProviderThread -count=1
```

Expected: FAIL because the routed args still lack `codex_home` and `codex_instance_key`.

### Task 3: Implement Minimal Injection

**Files:**
- Modify: `internal/platform/toolbridge/handler_launch_args.go`

- [x] **Step 1: Add the two missing identity args**

```go
{key: "codex_home", value: binding.CodexHome},
{key: "codex_instance_key", value: binding.CodexInstanceKey},
{key: "codex_model_provider", value: binding.CodexModelProvider},
```

- [x] **Step 2: Run single-file guards**

Run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\platform\toolbridge\handler_launch_args.go
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\platform\toolbridge\handler_launch_args_test.go
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\platform\toolbridge\handler_test.go
```

Expected: exit 0 for each command.

Execution note: on this Windows PowerShell run, the `test_with_guard.ps1 <file.go>` entry ran the repository guard successfully but then mis-dispatched into `go test file.go`, which fails for non-standalone package files. The equivalent underlying single-file guard was run directly:

```powershell
go run ./scripts/code_size_guard.go -- internal\platform\toolbridge\handler_launch_args.go internal\platform\toolbridge\handler_launch_args_test.go internal\platform\toolbridge\handler_test.go
```

Observed: exit 0.

- [x] **Step 3: Run affected package tests**

Run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/platform/toolbridge -count=1
```

Expected: exit 0.

- [x] **Step 4: Review and commit owned files**

Run:

```powershell
git status --short --branch
git diff --stat
git add docs\superpowers\plans\2026-06-23-p1a-codex-identity-inheritance.md internal\platform\toolbridge\handler_launch_args.go internal\platform\toolbridge\handler_launch_args_test.go internal\platform\toolbridge\handler_test.go
git commit -m "fix: 补齐 Codex 子代理 identity 继承"
```

Expected: commit succeeds and working tree is clean.
