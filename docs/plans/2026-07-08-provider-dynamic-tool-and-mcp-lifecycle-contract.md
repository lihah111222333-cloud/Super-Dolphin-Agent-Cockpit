# Provider Dynamic Tool And MCP Lifecycle Contract Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade Codex app dynamic tool responder parity into a provider contract and close the remaining ADR 0003 MCP/toolbridge lifecycle proof surface with production tests and arch guards.

**Architecture:** Provider-level behavior is enforced in `internal/provider/contracttest`, with each provider declaring either executable dynamic tool responder evidence or typed unsupported evidence. MCP per-tool lifecycle remains owned by `internal/module/mcp_server` and `internal/store/mcpserver`; `internal/platform/toolbridge` only consumes contract policy ports for list filtering and direct-call denial.

**Tech Stack:** Go, `testing`, provider contract harnesses, Fx graph tests, SQLite/sqlc MCP lifecycle store, toolbridge Codex dynamic tools, archtest.

**Verification Surface:** `./internal/provider/contracttest`, `./internal/provider/codexapp`, `./internal/provider/claudecli`, `./internal/provider`, `./internal/platform/toolbridge`, `./internal/module/mcp_server`, `./internal/store/mcpserver`, `./internal/app`, `./internal/archtest`, `make sqlc-verify`, `make guard`.

---

## File Structure

- Modify: `internal/provider/contracttest/suite.go`
  - Add the shared `CaseDynamicToolResponder` key, required evidence keys, and required-case ordering.
- Modify: `internal/provider/contracttest/acceptance.go`
  - Expose `AcceptanceDynamicToolResponder` so scaffold and manifest checks can require the new contract.
- Modify: `internal/provider/contracttest/evidence.go`
  - Add the typed evidence struct plus recording/assertion helper for dynamic tool responder evidence.
- Modify: `internal/provider/contracttest/acceptance_test.go` and `internal/provider/contracttest/suite_test.go`
  - Lock the new case into contracttest self-tests.
- Modify: `internal/provider/provider_contract_manifest_test.go`
  - Require every concrete provider contract spec to declare the new case.
- Modify: `internal/provider/codexapp/provider_contract_test.go`
  - Add an executable dynamic tool responder contract case using the existing inbound request path.
- Modify: `internal/provider/claudecli/provider_contract_test.go`
  - Add typed unsupported evidence for providers that do not support inbound dynamic tool responder RPCs.
- Modify: `internal/platform/toolbridge/lifecycle_enforcement_test.go`
  - Extend ADR 0003 coverage for `suspended` and `removed` error envelopes and alias denial.
- Modify: `internal/app/modules_graph_test.go`
  - Strengthen proof that toolbridge receives lifecycle owner/backfiller/policy ports through production graph wiring.
- Create: `internal/archtest/mcp_tool_lifecycle_contract_guard_test.go`
  - Guard dependency direction and forbid toolbridge from becoming lifecycle owner or writing lifecycle store directly.

## Task 1: Add Dynamic Tool Responder To Provider Contracttest

**Files:**
- Modify: `internal/provider/contracttest/suite.go`
- Modify: `internal/provider/contracttest/acceptance.go`
- Modify: `internal/provider/contracttest/evidence.go`
- Modify: `internal/provider/contracttest/acceptance_test.go`
- Modify: `internal/provider/contracttest/suite_test.go`

- [ ] **Step 1: Write failing contracttest self-tests**

Add a test in `internal/provider/contracttest/acceptance_test.go` that proves a spec without the new responder case is rejected:

```go
func TestValidateAcceptanceSpecRequiresDynamicToolResponder(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	delete(spec.RequiredCases, CaseDynamicToolResponder)

	err := ValidateAcceptanceSpec(spec)
	if err == nil || !strings.Contains(err.Error(), "dynamic_tool_responder") {
		t.Fatalf("ValidateAcceptanceSpec() error = %v, want dynamic_tool_responder requirement", err)
	}
}
```

Also add the new criterion to `TestValidateAcceptanceSpecRejectsMissingCriterion`:

```go
{name: "dynamic tool responder", omit: []AcceptanceCriterion{AcceptanceDynamicToolResponder}, want: string(AcceptanceDynamicToolResponder)},
```

And add it to `TestRequiredAcceptanceCriteriaProjectsDeclaredRequiredCases` in the same order used by `requiredCaseOrder`:

```go
AcceptanceDynamicToolResponder,
```

Add a matching positive check in `internal/provider/contracttest/suite_test.go`:

```go
func TestRunSpecForTestAcceptsDynamicToolResponderEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseDynamicToolResponder] = Case{
		Name: "dynamic tool responder",
		Run: func(t *testing.T, e *CaseEvidence) {
			e.RecordDynamicToolResponder(t, DynamicToolResponderEvidence{
				Provider:              "test",
				Supported:             true,
				SuccessRequestID:      "req-success",
				ErrorRequestID:        "req-error",
				HandlerSuccessCalled:  true,
				HandlerErrorCalled:    true,
				SuccessReturnedToID:   true,
				ErrorReturnedToID:     true,
			})
		},
	}

	if err := RunSpecForTest(t, spec); err != nil {
		t.Fatalf("RunSpecForTest() error = %v", err)
	}
}
```

- [ ] **Step 2: Run the red tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/contracttest -run 'TestValidateAcceptanceSpecRequiresDynamicToolResponder|TestRunSpecForTestAcceptsDynamicToolResponderEvidence' -count=1
```

Expected: FAIL because `CaseDynamicToolResponder`, `DynamicToolResponderEvidence`, and `RecordDynamicToolResponder` are not yet defined.

- [ ] **Step 3: Add the contract key and evidence requirement**

In `internal/provider/contracttest/suite.go`, add:

```go
const (
	// CaseDynamicToolResponder 校验 provider 收到动态工具调用后会执行 host handler，并把成功/失败结果回复到原始模型请求 ID。
	CaseDynamicToolResponder CaseKey = "dynamic_tool_responder"
)
```

Extend evidence keys:

```go
const (
	// EvidenceDynamicToolResponder 由 RecordDynamicToolResponder 写入。
	EvidenceDynamicToolResponder EvidenceKey = "dynamic_tool_responder.outcome"
)
```

Extend maps and order:

```go
var requiredEvidenceByCase = map[CaseKey][]EvidenceKey{
	CaseDynamicToolResponder: {EvidenceDynamicToolResponder},
}

var requiredCaseOrder = []CaseKey{
	CaseDynamicToolResponder,
}

var reservedEvidenceKeys = map[EvidenceKey]bool{
	EvidenceDynamicToolResponder: true,
}
```

Merge these entries into the existing maps and slice; do not replace existing cases.

- [ ] **Step 4: Add acceptance alias**

In `internal/provider/contracttest/acceptance.go`, add:

```go
const (
	AcceptanceDynamicToolResponder AcceptanceCriterion = CaseDynamicToolResponder
)
```

- [ ] **Step 5: Add evidence type and recorder**

In `internal/provider/contracttest/evidence.go`, add the evidence type near the other typed evidence structs:

```go
type DynamicToolResponderEvidence struct {
	Provider              string `json:"provider"`
	Supported             bool   `json:"supported"`
	SuccessRequestID      string `json:"success_request_id,omitempty"`
	ErrorRequestID        string `json:"error_request_id,omitempty"`
	HandlerSuccessCalled  bool   `json:"handler_success_called"`
	HandlerErrorCalled    bool   `json:"handler_error_called"`
	SuccessReturnedToID   bool   `json:"success_returned_to_id"`
	ErrorReturnedToID     bool   `json:"error_returned_to_id"`
	Unsupported           *UnsupportedOutcomeEvidence
}
```

In `internal/provider/contracttest/evidence.go`, add:

```go
func (e *CaseEvidence) RecordDynamicToolResponder(t *testing.T, evidence DynamicToolResponderEvidence) {
	t.Helper()
	if strings.TrimSpace(evidence.Provider) == "" {
		t.Fatal("dynamic tool responder provider is required")
	}
	if evidence.Supported {
		if evidence.SuccessRequestID == "" || evidence.ErrorRequestID == "" {
			t.Fatalf("dynamic tool responder request ids are required: %#v", evidence)
		}
		if !evidence.HandlerSuccessCalled || !evidence.HandlerErrorCalled {
			t.Fatalf("dynamic tool responder handler was not called for both paths: %#v", evidence)
		}
		if !evidence.SuccessReturnedToID || !evidence.ErrorReturnedToID {
			t.Fatalf("dynamic tool responder did not reply to original ids: %#v", evidence)
		}
	} else if err := validateUnsupportedOutcome(evidence.Unsupported); err != nil {
		t.Fatalf("dynamic tool responder unsupported evidence must be typed: %v", err)
	}
	parts := []string{
		strings.TrimSpace(evidence.Provider),
		fmt.Sprintf("supported=%t", evidence.Supported),
		"success_id=" + strings.TrimSpace(evidence.SuccessRequestID),
		"error_id=" + strings.TrimSpace(evidence.ErrorRequestID),
		fmt.Sprintf("success_handler=%t", evidence.HandlerSuccessCalled),
		fmt.Sprintf("error_handler=%t", evidence.HandlerErrorCalled),
		fmt.Sprintf("success_returned=%t", evidence.SuccessReturnedToID),
		fmt.Sprintf("error_returned=%t", evidence.ErrorReturnedToID),
	}
	if evidence.Unsupported != nil {
		parts = append(parts, "unsupported="+evidence.Unsupported.dependencyName+"/"+string(evidence.Unsupported.profile))
	}
	e.assertions[EvidenceDynamicToolResponder] = strings.Join(parts, "/")
}
```

Do not route this through `RecordOutcome`: current `OutcomeEvidence` is deliberately scoped to existing outcome cases and has no generic payload field.

- [ ] **Step 6: Update fixture spec**

In `internal/provider/contracttest/fixture_test.go`, add the new required case to `CompleteFixtureSpec`:

```go
CaseDynamicToolResponder: fixtureDynamicToolResponderCase(name),
```

Add the fixture case:

```go
func fixtureDynamicToolResponderCase(provider string) Case {
	return Case{Name: "dynamic tool responder", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordDynamicToolResponder(t, DynamicToolResponderEvidence{
			Provider:             provider,
			Supported:            true,
			SuccessRequestID:     "fixture-success",
			ErrorRequestID:       "fixture-error",
			HandlerSuccessCalled: true,
			HandlerErrorCalled:   true,
			SuccessReturnedToID:  true,
			ErrorReturnedToID:    true,
		})
	}}
}
```

In `internal/provider/contracttest/evidence.go`, keep `EvidenceDynamicToolResponder` in `reservedEvidenceKeys` so supplemental `AssertEqual` cannot fake this case.

- [ ] **Step 7: Run contracttest package**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/contracttest -count=1
```

Expected: PASS.

## Task 2: Enforce The New Contract In Provider Manifest Guard

**Files:**
- Modify: `internal/provider/provider_contract_manifest_test.go`

- [ ] **Step 1: Write the failing manifest guard expectation**

Add `AcceptanceDynamicToolResponder` to `acceptanceCriterionSelector`:

```go
case contracttest.AcceptanceDynamicToolResponder:
	return "CaseDynamicToolResponder"
```

The existing `providerAcceptanceRequiredSelectors` flow derives required provider cases from `contracttest.RequiredAcceptanceCriteria`, so do not add a parallel hard-coded required-case list.

- [ ] **Step 2: Run the red guard**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider -run TestProviderPackagesHaveContractTests -count=1
```

Expected: FAIL for `codexapp` and `claudecli` because their specs do not yet declare `CaseDynamicToolResponder`.

- [ ] **Step 3: Leave provider fixes to Tasks 3 and 4**

Do not weaken this guard. The next tasks make it green by adding real provider declarations.

## Task 3: Add Codex App Executable Dynamic Tool Responder Contract

**Files:**
- Modify: `internal/provider/codexapp/provider_contract_test.go`

- [ ] **Step 1: Add the Codex contract case to the spec**

In `CompleteCodexAppContractSpec`, add:

```go
contracttest.CaseDynamicToolResponder: codexAppDynamicToolResponderContractCase(),
```

- [ ] **Step 2: Add a helper responder and handler capture**

Add `fmt` to the existing `internal/provider/codexapp/provider_contract_test.go` import block because the contract case below returns `fmt.Errorf` for unexpected tool names.

Add test-only helper types near the existing provider contract helpers:

```go
type codexContractResponderCall struct {
	id     string
	result any
	err    error
}

type codexContractResponder struct {
	mu    sync.Mutex
	calls []codexContractResponderCall
	ch    chan codexContractResponderCall
}

func newCodexContractResponder() *codexContractResponder {
	return &codexContractResponder{ch: make(chan codexContractResponderCall, 4)}
}

func (r *codexContractResponder) RespondWithID(id json.RawMessage, result any, callErr error) error {
	call := codexContractResponderCall{id: string(id), result: result, err: callErr}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	r.ch <- call
	return nil
}

func waitCodexContractResponderCall(t *testing.T, ch <-chan codexContractResponderCall) codexContractResponderCall {
	t.Helper()
	select {
	case call := <-ch:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Codex dynamic tool response")
		return codexContractResponderCall{}
	}
}
```

- [ ] **Step 3: Implement the executable case**

Add:

```go
func codexAppDynamicToolResponderContractCase() contracttest.Case {
	return contracttest.Case{Name: "dynamic tool responder", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		ctx := context.Background()
		manager := &ServerManager{}
		var successCalled bool
		var errorCalled bool
		manager.SetToolHandler(func(_ context.Context, msg RawMessage) (any, error) {
			name := toolCallParamString(msg.Params, "name")
			switch name {
			case "contract_success":
				successCalled = true
				return map[string]any{"success": true, "result": "ok"}, nil
			case "contract_error":
				errorCalled = true
				return nil, errors.New("contract dynamic tool failed")
			default:
				return nil, fmt.Errorf("unexpected contract tool %q", name)
			}
		})
		s := newInboundTestSession(ctx, nil, manager)

		successResp := newCodexContractResponder()
		s.onInboundMessage(ctx, successResp, RawMessage{
			ID:     json.RawMessage(`"req-success"`),
			Method: "dynamic_tool_call",
			Params: json.RawMessage(`{"name":"contract_success","arguments":{"value":1},"turnId":"turn-contract","callId":"call-success"}`),
		})
		successCall := waitCodexContractResponderCall(t, successResp.ch)

		errorResp := newCodexContractResponder()
		s.onInboundMessage(ctx, errorResp, RawMessage{
			ID:     json.RawMessage(`"req-error"`),
			Method: "tools/call",
			Params: json.RawMessage(`{"name":"contract_error","arguments":{"value":2},"turnId":"turn-contract","callId":"call-error"}`),
		})
		errorCall := waitCodexContractResponderCall(t, errorResp.ch)

		e.RecordDynamicToolResponder(t, contracttest.DynamicToolResponderEvidence{
			Provider:             "codex",
			Supported:            true,
			SuccessRequestID:     `"req-success"`,
			ErrorRequestID:       `"req-error"`,
			HandlerSuccessCalled: successCalled,
			HandlerErrorCalled:   errorCalled,
			SuccessReturnedToID:  successCall.id == `"req-success"` && successCall.err == nil,
			ErrorReturnedToID:    errorCall.id == `"req-error"` && errorCall.err != nil,
		})
	}}
}
```

If `newInboundTestSession` is only in `session_test.go`, keep this contract case in package `codexapp` and reuse it directly; do not duplicate session construction.

- [ ] **Step 4: Run the Codex contract**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -run TestCodexAppProviderContract -count=1
```

Expected: PASS.

## Task 4: Add Claude Typed Unsupported Dynamic Tool Responder Contract

**Files:**
- Modify: `internal/provider/claudecli/provider_contract_test.go`

- [ ] **Step 1: Add the Claude contract case to the spec**

In `CompleteClaudeContractSpec`, add:

```go
contracttest.CaseDynamicToolResponder: claudeDynamicToolResponderUnsupportedContractCase(),
```

- [ ] **Step 2: Add typed unsupported evidence**

Add:

```go
func claudeDynamicToolResponderUnsupportedContractCase() contracttest.Case {
	return contracttest.Case{Name: "dynamic tool responder unsupported", Run: func(t *testing.T, e *contracttest.CaseEvidence) {
		unsupported := contracttest.CaptureUnsupportedOutcome(
			t,
			"claude-dynamic-tool-responder",
			"dynamic_tool_responder",
			contract.DependencyProfileTest,
			func() error {
				return contract.NewDependencyModeError(
					contract.ErrUnsupportedDependencyMode,
					"dynamic_tool_responder",
					contract.DependencyProfileTest,
				)
			},
		)
		e.RecordDynamicToolResponder(t, contracttest.DynamicToolResponderEvidence{
			Provider:    "claude",
			Supported:   false,
			Unsupported: unsupported,
		})
	}}
}
```

- [ ] **Step 3: Run the Claude contract**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/claudecli -run TestClaudeProviderContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Run the provider manifest guard**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider -run TestProviderPackagesHaveContractTests -count=1
```

Expected: PASS.

## Task 5: Strengthen ADR 0003 Lifecycle State Matrix

**Files:**
- Modify: `internal/platform/toolbridge/lifecycle_enforcement_test.go`

- [ ] **Step 1: Add suspended and removed direct-call denial cases**

Extend `TestCodexSurfaceToolCallDeniesDisabledLifecycleAliases` or add a new table test:

```go
func TestCodexSurfaceToolCallDeniesNonEnabledLifecycleStates(t *testing.T) {
	root := t.TempDir()
	states := []struct {
		state contract.MCPToolLifecycleState
		code  string
	}{
		{state: contract.MCPToolLifecycleDisabled, code: contract.MCPToolLifecycleDenyCodeDisabled},
		{state: contract.MCPToolLifecycleSuspended, code: contract.MCPToolLifecycleDenyCodeSuspended},
		{state: contract.MCPToolLifecycleRemoved, code: contract.MCPToolLifecycleDenyCodeRemoved},
	}
	for _, tt := range states {
		t.Run(string(tt.state), func(t *testing.T) {
			client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: strictEmptyObjectSchema()}}}
			owner := newFakeMCPToolLifecycleOwner()
			owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleEnabled, "")
			h := &Handler{
				stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: client}),
				lifecycle:          owner,
				lifecyclePolicy:    owner,
			}
			_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
				AgentID: "agent-1",
				CWD:     root,
				Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
					{Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"}},
				}},
			})
			if err != nil {
				t.Fatalf("PrepareCodexToolSurface() error = %v", err)
			}
			owner.setDecision(root, mcpdto.ClientKindLSP, "grep", tt.state, "blocked by contract")

			result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
				Params: mustRawJSON(t, map[string]any{
					"name":     "mcp__lsp__grep",
					"arguments": map[string]any{},
					"_agentId": "agent-1",
					"_cwd":     root,
				}),
			})
			if err != nil {
				t.Fatalf("HandleToolCall() error = %v", err)
			}
			got, ok := result.(*ToolCallResult)
			if !ok {
				t.Fatalf("HandleToolCall() result = %T, want *ToolCallResult", result)
			}
			assertLifecycleDeniedResult(t, got, mcpdto.ClientKindLSP, "grep", tt.code)
			if len(client.calls) != 0 {
				t.Fatalf("denied lifecycle call reached MCP client: calls=%#v", client.calls)
			}
		})
	}
}
```

- [ ] **Step 2: Run lifecycle tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge -run 'TestCodexSurfaceToolCallDeniesNonEnabledLifecycleStates|Test.*Lifecycle' -count=1
```

Expected: PASS.

## Task 6: Add Production Graph And Arch Guards For Lifecycle Ownership

**Files:**
- Modify: `internal/app/modules_graph_test.go`
- Create: `internal/archtest/mcp_tool_lifecycle_contract_guard_test.go`

- [ ] **Step 1: Strengthen app graph tests**

`internal/app/modules_graph_test.go` already has these production graph tests:

```go
func TestAppModuleGraphProvidesToolbridgeMCPToolLifecycleBackfiller(t *testing.T) {
	t.Parallel()

	var backfiller toolbridge.MCPToolLifecycleBackfiller
	opts := append(appGraphValidationOptions(), fx.Populate(&backfiller))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing MCP tool lifecycle backfiller: %v", err)
	}
}

func TestAppModuleGraphProvidesToolbridgeMCPToolLifecyclePolicyReader(t *testing.T) {
	t.Parallel()

	var reader toolbridge.MCPToolLifecyclePolicyReader
	opts := append(appGraphValidationOptions(), fx.Populate(&reader))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing MCP tool lifecycle policy reader: %v", err)
	}
}
```

If they still exist unchanged, do not add duplicate tests. Only update the failure messages or nearby comments to mention ADR 0003 owner/backfill/policy proof.

- [ ] **Step 2: Add archtest guard**

Create `internal/archtest/mcp_tool_lifecycle_contract_guard_test.go`:

```go
package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestToolbridgeDoesNotOwnMCPToolLifecycleStore(t *testing.T) {
	root := repoRoot(t)
	for _, file := range parseImportFiles(t, root, "internal/platform/toolbridge") {
		for _, imp := range file.Imports {
			if imp == internalPrefix("internal/store/mcpserver") {
				t.Fatalf("%s imports store/mcpserver; toolbridge must consume contract lifecycle policy ports only", file.RelPath)
			}
		}
	}
}

func TestMCPToolLifecyclePolicyPortStaysInContractLayer(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "internal", "contract", "mcp_control.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	foundReader := false
	foundRequest := false
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.TypeSpec:
			if x.Name.Name == "MCPToolLifecyclePolicyReader" {
				foundReader = true
			}
			if x.Name.Name == "MCPToolLifecyclePolicyRequest" {
				foundRequest = true
			}
		}
		return true
	})
	if !foundReader || !foundRequest {
		t.Fatalf("contract mcp lifecycle policy port missing: reader=%v request=%v", foundReader, foundRequest)
	}
}
```

Use the existing `archtest_test` helper pattern from `internal/archtest/sqlc_boundary_test.go`: `repoRoot`, `parseImportFiles`, and `internalPrefix`. Do not add shell-based arch checks.

- [ ] **Step 3: Run app and arch tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/archtest -run 'TestAppModuleGraphProvidesToolbridgeMCPToolLifecycle|TestToolbridgeDoesNotOwnMCPToolLifecycleStore|TestMCPToolLifecyclePolicyPortStaysInContractLayer' -count=1
```

Expected: PASS.

## Task 7: Final Verification

**Files:**
- No new source files beyond previous tasks.

- [ ] **Step 1: Run provider contract matrix**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/contracttest ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider -run 'Test.*ProviderContract|TestProviderPackagesHaveContractTests|Test.*DynamicToolResponder' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run lifecycle matrix**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/module/mcp_server ./internal/store/mcpserver ./internal/app ./internal/archtest -run 'Test.*MCPToolLifecycle|Test.*Lifecycle|TestAppModuleGraphProvidesToolbridgeMCPToolLifecycle|TestToolbridgeDoesNotOwnMCPToolLifecycleStore|TestMCPToolLifecyclePolicyPortStaysInContractLayer' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run SQL verification if lifecycle store or query files changed**

Run:

```bash
make sqlc-verify
```

Expected: PASS. If no SQL, migration, or generated sqlc file changed, record `not run: no SQL/store generation changes` in the execution notes.

- [ ] **Step 4: Run architecture guard**

Run:

```bash
make guard
```

Expected: PASS.

- [ ] **Step 5: Run diff hygiene**

Run:

```bash
git diff --check
git status --short
```

Expected: `git diff --check` exits 0. `git status --short` only lists files intentionally modified by this plan.

## Stop Conditions

- Stop if any LSP diagnostic for touched Go files returns Error, Warning, Information, or Hint that cannot be fixed in-scope.
- Stop if a provider can only satisfy `CaseDynamicToolResponder` by silently ignoring inbound calls; it must either execute the handler and respond to the original ID or record typed unsupported evidence.
- Stop if lifecycle policy lookup fails and any list or direct-call path treats the tool as enabled.
- Stop if `toolbridge` needs to import `internal/store/mcpserver`; route through `internal/contract` and app adapters instead.
- Stop if SQL/store files drift without `make sqlc-verify` evidence.

## Review Checklist

- The new contract proves success and error tool handler execution, not only event translation.
- Error result is returned to the same model request ID, not just published as a lifecycle event.
- Unsupported provider behavior is typed and visible in contract evidence.
- `disabled`, `suspended`, and `removed` are all hidden from list surfaces and denied on direct call.
- Direct-call denial covers bare names, legacy aliases, and `mcp__server__tool` names.
- Lifecycle owner remains `internal/module/mcp_server`; store owner remains `internal/store/mcpserver`; toolbridge remains a policy consumer.
