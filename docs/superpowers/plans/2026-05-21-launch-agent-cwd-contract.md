# Launch Agent CWD Contract Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix `launch_agent` so missing `cwd` fails fast with a stable non-LSP error unless it can be inherited from a valid parent agent.

**Architecture:** Keep parent cwd inheritance in orchestration as the only legal implicit cwd path. Add stable contract-layer sentinels, validate the resolved launch request after inheritance, and map those sentinels to deterministic MCP tool error envelopes. Do not default to project root, process cwd, system time, or `.workspace`.

**Tech Stack:** Go, MCP orchestration tools, jrpc2-backed remote launcher, repository guard test wrapper.

---

## Review Status

- R1 parallel review found P1 gaps: string-only error classification, missing parent exceptional cases, and missing end-to-end tool envelope tests.
- Plan v2 incorporates those fixes.
- R2 parallel review returned **no P0/P1** from backend, testing, and enterprise-norm reviewers.

## Current Bug

`internal/sidecar/orch/tools/orchestration_tools.go` describes `launch_agent.cwd` as optional, but a launch without `cwd` and without a parent cwd inheritance path reaches `thread/start`, which rejects it with `thread start cwd is required`. The generic MCP tool error classifier then falls back to `lsp_unavailable`, making the error look retryable and related to LSP startup.

Expected behavior:

- `cwd` may be omitted only when `parent_id` resolves to an existing parent agent with a non-empty absolute cwd.
- Otherwise launch fails before `thread/start`.
- The MCP tool error envelope uses a stable cwd-specific code and non-retryable hint.

## Files

- Modify: `internal/contract/errors.go`
  - Add launch cwd error sentinels shared across orchestration and MCP error-envelope classification.
- Modify: `internal/sidecar/orch/orchestration/launch_helpers.go`
  - Validate resolved launch cwd after parent inheritance.
- Modify/Test: `internal/sidecar/orch/orchestration/launcher_test.go`
  - Lock launch and snapshot cwd behavior.
- Modify: `internal/mcpserver/common/tool_error_envelope.go`
  - Classify launch cwd sentinel errors before generic fallback.
- Create/Modify Test: `internal/mcpserver/common/tool_error_envelope_test.go`
  - Lock cwd error envelope classification.
- Modify/Test: `internal/sidecar/orch/tools/orchestration_tools.go`
  - Clarify `cwd` schema description.
- Create/Modify Test: `internal/sidecar/orch/tools/orchestration_tools_test.go`
  - Lock tool-level envelope behavior and schema wording.

## Task 1: Add Failing Orchestration CWD Tests

**Files:**
- Modify: `internal/sidecar/orch/orchestration/launcher_test.go`

- [ ] **Step 1: Add a helper that fails if `thread/start` is called**

Add or inline this pattern in the new tests:

```go
called := false
svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
	"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
		called = true
		t.Fatalf("thread/start called unexpectedly with req=%#v", req)
		return nil, nil
	}),
}), nil, nil, nil)
```

- [ ] **Step 2: Add missing-cwd LaunchAgent fail-fast test**

Add:

```go
func TestService_LaunchAgent_RejectsMissingCwdWithoutParentBeforeThreadStart(t *testing.T) {
	called := false
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			called = true
			t.Fatalf("thread/start called unexpectedly with req=%#v", req)
			return nil, nil
		}),
	}), nil, nil, nil)

	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID: "child-1",
		Name:    "child",
		Command: []string{"ignored"},
	})
	if !errors.Is(err, contract.ErrLaunchCWDRequired) {
		t.Fatalf("LaunchAgent() error = %v, want ErrLaunchCWDRequired", err)
	}
	if called {
		t.Fatal("thread/start was called")
	}
}
```

Import `github.com/anthropic-ai/super-agent-v3/internal/contract` if not already imported.

- [ ] **Step 3: Add missing-cwd LaunchAgentSnapshot fail-fast test**

Add the same assertion for:

```go
func TestService_LaunchAgentSnapshot_RejectsMissingCwdWithoutParentBeforeThreadStart(t *testing.T)
```

Call `svc.LaunchAgentSnapshot(...)`, assert `errors.Is(err, contract.ErrLaunchCWDRequired)`, and assert `thread/start` was not called.

- [ ] **Step 4: Add parent exceptional cases**

Add tests for:

```go
func TestService_LaunchAgent_RejectsMissingCwdWhenParentDoesNotExist(t *testing.T)
func TestService_LaunchAgent_RejectsMissingCwdWhenParentHasNoCwd(t *testing.T)
```

For the second test, create the parent runtime with empty cwd:

```go
parent := svc.newAgentLocked("parent-1")
parent.cwd = ""
svc.agents[parent.id] = parent
```

Both tests must assert:

```go
if !errors.Is(err, contract.ErrLaunchCWDRequired) {
	t.Fatalf("LaunchAgent() error = %v, want ErrLaunchCWDRequired", err)
}
```

Also assert `thread/start` was not called.

- [ ] **Step 5: Add invalid dot cwd test**

Add:

```go
func TestService_LaunchAgent_RejectsDotCwdBeforeThreadStart(t *testing.T)
```

Use `Cwd: "."`, assert `errors.Is(err, contract.ErrLaunchCWDInvalid)`, and assert `thread/start` was not called.

- [ ] **Step 6: Strengthen successful cwd tests**

Existing tests already cover:

- `TestService_LaunchAgent_InheritsParentCwdWhenChildOmits`
- `TestService_LaunchAgent_RespectsExplicitChildCwd`
- `TestService_LaunchAgentSnapshot_InheritsParentCwdWhenChildOmits`

Ensure each successful test asserts the final `thread/start` request cwd:

```go
if got, _ := started["cwd"].(string); got != "/repo/foo" {
	t.Fatalf("thread/start cwd = %q, want %q", got, "/repo/foo")
}
```

- [ ] **Step 7: Update existing success tests**

Any service-level launch test that expects success and has no parent cwd inheritance must pass an explicit temporary cwd:

```go
Cwd: t.TempDir(),
```

Do not change the parent-inheritance tests to pass child cwd.

- [ ] **Step 8: Run failing tests before implementation**

Run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration -run 'TestService_LaunchAgent_RejectsMissingCwd|TestService_LaunchAgentSnapshot_RejectsMissingCwd|TestService_LaunchAgent_RejectsDotCwd' -count=1
```

Expected before implementation: failures because the sentinel errors and validation do not exist yet.

## Task 2: Implement Stable Launch CWD Validation

**Files:**
- Modify: `internal/contract/errors.go`
- Modify: `internal/sidecar/orch/orchestration/launch_helpers.go`

- [ ] **Step 1: Add sentinels**

In `internal/contract/errors.go`, add:

```go
var (
	ErrLaunchCWDRequired = errors.New("launch cwd is required")
	ErrLaunchCWDInvalid  = errors.New("launch cwd is invalid")
)
```

Place these near the other shared contract sentinels.

- [ ] **Step 2: Import contract in launch helpers**

In `internal/sidecar/orch/orchestration/launch_helpers.go`, add:

```go
"github.com/anthropic-ai/super-agent-v3/internal/contract"
```

- [ ] **Step 3: Add resolved cwd validation**

Add:

```go
func validateResolvedLaunchCWD(req LaunchRequest) error {
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		parentID := strings.TrimSpace(req.ParentID)
		if parentID != "" {
			return fmt.Errorf("%w: launch_agent cwd is required; parent_id %q was not found or has no cwd", contract.ErrLaunchCWDRequired, parentID)
		}
		return fmt.Errorf("%w: launch_agent cwd is required; pass cwd or parent_id whose agent has cwd", contract.ErrLaunchCWDRequired)
	}
	if cwd == "." {
		return fmt.Errorf("%w: launch_agent cwd must be explicit; got dot", contract.ErrLaunchCWDInvalid)
	}
	return nil
}
```

Do not call `os.Getwd`, do not use project root defaults, and do not use `.workspace`.

- [ ] **Step 4: Wire validation into launcher validation**

Change:

```go
func validateLaunchRequestForLauncher(req LaunchRequest, launcher AgentLauncher) error {
	if err := validateLaunchRequestBase(req); err != nil {
		return err
	}
	if requiresCommand(launcher) && len(req.Command) == 0 {
		return errors.New("command is required")
	}
	return nil
}
```

to:

```go
func validateLaunchRequestForLauncher(req LaunchRequest, launcher AgentLauncher) error {
	if err := validateLaunchRequestBase(req); err != nil {
		return err
	}
	if err := validateResolvedLaunchCWD(req); err != nil {
		return err
	}
	if requiresCommand(launcher) && len(req.Command) == 0 {
		return errors.New("command is required")
	}
	return nil
}
```

This relies on `LaunchAgent` and `LaunchAgentSnapshot` applying parent defaults before calling the launcher path.

- [ ] **Step 5: Run orchestration tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration -count=1
```

Expected: pass.

## Task 3: Classify Launch CWD Errors in Tool Error Envelopes

**Files:**
- Modify: `internal/mcpserver/common/tool_error_envelope.go`
- Create/Modify: `internal/mcpserver/common/tool_error_envelope_test.go`

- [ ] **Step 1: Add tests for sentinel classification**

Add tests that build envelopes with wrapped sentinels:

```go
func TestClassifyToolErrorLaunchCWDRequired(t *testing.T) {
	err := fmt.Errorf("%w: launch_agent cwd is required", contract.ErrLaunchCWDRequired)
	env := NewToolErrorEnvelope("launch_agent", err)
	if env.Code != "cwd_required" {
		t.Fatalf("Code = %q, want cwd_required", env.Code)
	}
	if env.Retryable {
		t.Fatal("Retryable = true, want false")
	}
	if strings.Contains(strings.ToLower(env.Hint), "lsp") {
		t.Fatalf("Hint = %q, must not mention LSP", env.Hint)
	}
}
```

Add the equivalent test for `contract.ErrLaunchCWDInvalid` expecting `cwd_invalid`.

- [ ] **Step 2: Add historical string compatibility test**

Add:

```go
func TestClassifyToolErrorHistoricalCWDRequiredString(t *testing.T) {
	env := NewToolErrorEnvelope("launch_agent", errors.New("thread start cwd is required"))
	if env.Code != "cwd_required" {
		t.Fatalf("Code = %q, want cwd_required", env.Code)
	}
	if env.Retryable {
		t.Fatal("Retryable = true, want false")
	}
}
```

- [ ] **Step 3: Implement classifiers**

Import `internal/contract` in `tool_error_envelope.go`.

Add classifier entries before the generic fallback and before path/file classifiers:

```go
{
	code: "cwd_required",
	hint: staticToolHint("Pass a non-empty cwd, or pass parent_id for an existing parent agent with cwd."),
	match: func(err error, message string, _ string) bool {
		return errors.Is(err, contract.ErrLaunchCWDRequired) ||
			strings.Contains(message, "cwd is required")
	},
},
{
	code: "cwd_invalid",
	hint: staticToolHint("Pass an explicit cwd path; dot cwd is not accepted."),
	match: func(err error, message string, _ string) bool {
		return errors.Is(err, contract.ErrLaunchCWDInvalid) ||
			strings.Contains(message, "cwd must be explicit")
	},
},
```

Do not set `retryable: true`; the zero value must remain `false`.

- [ ] **Step 4: Run common tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/mcpserver/common -count=1
```

Expected: pass.

## Task 4: Update `launch_agent` Tool Contract and End-to-End Envelope Tests

**Files:**
- Modify: `internal/sidecar/orch/tools/orchestration_tools.go`
- Create/Modify: `internal/sidecar/orch/tools/orchestration_tools_test.go`

- [ ] **Step 1: Update cwd schema description**

Change the `cwd` field description to:

```go
    "cwd": StringSchema("Optional only when parent_id resolves to an existing parent agent with cwd; otherwise required. Use an explicit absolute project or workspace path."),
```

Do not add `cwd` to `ObjectSchema(..., "name")` required fields.

- [ ] **Step 2: Add schema description regression test**

Add a test that finds `launch_agent` via `NewRegistry(...).Lookup("launch_agent")` or `orchestrationToolDefinitions(...)`, extracts `InputSchema["properties"]["cwd"]["description"]`, and asserts it contains:

```go
"parent_id"
"otherwise required"
```

- [ ] **Step 3: Add tool-level envelope test**

Use `HandleLaunchAgent` with a service implementation whose launch path returns `contract.ErrLaunchCWDRequired`, or use the real service when that is cheaper in the test package. The test must call the handler with:

```json
{"name":"child"}
```

Then wrap the returned error through:

```go
env := common.NewToolErrorEnvelope("launch_agent", err)
```

Assert:

```go
if env.Code != "cwd_required" { ... }
if env.Retryable { ... }
if strings.Contains(strings.ToLower(env.Hint), "lsp") { ... }
```

This is the endpoint-level regression for the original user-visible bug.

- [ ] **Step 4: Run tools tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/tools -count=1
```

Expected: pass.

## Task 5: Final Verification

**Files:**
- No additional edits unless prior tasks reveal failures.

- [ ] **Step 1: Run affected package tests with guard**

Run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/tools ./internal/mcpserver/common -count=1
```

Expected: pass.

- [ ] **Step 2: Run guard**

Run:

```bash
make guard
```

Expected: pass.

- [ ] **Step 3: Inspect git status and baseline changes**

Run:

```bash
git status --short
git diff -- internal/archtest/baseline.json
```

Expected:

- Only owned implementation/test files are modified.
- No `internal/archtest/baseline.json` diff unless the task explicitly requires it.
- Existing unrelated changes must not be staged, reverted, or formatted.

## Non-Goals

- Do not change `.workspace` semantics. It remains an isolated filesystem workspace for edits and merge conflict detection, not agent metadata storage.
- Do not auto-create workspace runs for `launch_agent`.
- Do not infer cwd from process cwd, project root, source root, system time, or the current shell.
- Do not weaken guard thresholds.
- Do not classify this error as LSP-related or retryable.
