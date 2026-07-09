# Backend Boundary Hardening Single-Path Implementation Plan

> **Status (2026-07-09 current HEAD):** This document is no longer safe to execute verbatim. Task 3's sidecar DB-boundary repair has already been implemented with one common classifier hook plus an mcp-orch-owned classifier in `cmd/mcp-orch/tools/task_tools.go`; do not create another `error_classifier.go`, do not add another classifier API, and do not re-add LSP/IDA DB allowlist entries. AI workers must first compare this plan against current code and then execute only the remaining unfixed items.

> **For agentic workers:** 强制使用 `superpowers:子代理驱动开发` 执行本计划。每个 Task 使用一个新子代理，主会话在 Task 间审查 diff 和验证输出；不得切换执行流程，不得把步骤改写成宽松建议。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the three backend-boundary hardening gaps with one mandatory repair path: constructor-time dependency absence is centralized, AI visual hotspot files are ratcheted through the existing code-size metric, and non-orchestration sidecars lose the transitive DB dependency before their DB allowlist entries are removed.

**Architecture:** Constructor-time dependency checks move to `internal/contract.RequireDependency`; runtime/report operations keep `MissingDependencyModeError` when callers must observe `ErrDependencyDeferred` / `ErrUnsupportedDependencyMode`. Optional dependency auditing extends the existing `internal/archtest/dependency_optional_boundary_test.go` source of truth. MCP common code must not import `internal/platform/db`; DB-aware DAG conflict classification belongs to `cmd/mcp-orch`.

**Tech Stack:** Go 1.25+, Fx, repository archtests, `./scripts/test_with_guard.sh`, existing `internal/contract.DependencyProfile`, existing `internal/archtest.CountEffectiveLines`.

**Verification Surface:** `internal/contract`, `internal/app`, `internal/platform/toolbridge`, `internal/mcpserver/common`, `cmd/mcp-orch`, `cmd/mcp-lsp`, `cmd/mcp-ida`, `internal/archtest`, `git diff --check`.

---

## Non-Negotiable Decision

This plan has exactly one execution path.

1. Dependency absence is fixed by a contract-level constructor helper and by extending the existing optional-boundary archtest. Do not add a second optional seam guard file.
2. AI visual review debt is fixed by effective-line ratcheting through the existing code-size metric. Do not add a raw-line metric and do not raise any size budget.
3. Sidecar stateful boundary debt is fixed by removing the DB dependency from shared MCP common code first. Delete the `cmd/mcp-lsp` and `cmd/mcp-ida` DB allowlist entries only after `go list -deps` proves those sidecars no longer depend on `internal/platform/db`.

Do not solve any task by lowering a guard, broadening an allowlist, introducing a second dependency-policy registry, adding a noop implementation, routing DB through a new shared readiness package.

## Current Evidence

| Gap | Current anchor | Required outcome |
| --- | --- | --- |
| Constructor dependency absence has local handling | `internal/app/dependency_contract.go`, `internal/platform/toolbridge/handler.go`, `internal/contract/config.go` | Constructor checks use `contract.RequireDependency`; runtime typed errors stay at runtime/report operation boundaries. |
| Optional dependency auditing already has a fact source | `internal/archtest/dependency_optional_boundary_test.go` | Existing `TestOptionalDependencyBoundary` is extended; no new `dependency_profile_optional_guard_test.go` is created. |
| AI visual hotspots remain large | `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`, `internal/provider/codexapp/driver.go`, `internal/module/thread/lifecycle.go`, `internal/platform/toolbridge/handler.go`, `cmd/mcp-lsp/tools/factory.go` | Named files are split below the AI visual effective-line limit while following the fixed Task 2 package-count budget table. |
| Sidecar DB allowlist is not stale yet | `internal/mcpserver/common/tool_error_envelope.go`, `internal/archtest/backend_boundary_matrix_test.go:mcpSidecarImportAllowances` | `cmd/mcp-lsp` and `cmd/mcp-ida` have no direct and no transitive DB dependency; only `cmd/mcp-orch` keeps DB ownership. |

## File Structure

| Path | Action | Responsibility |
| --- | --- | --- |
| `internal/contract/config.go` | Modify | Add `RequireDependency` for constructor-time dependency enforcement only. |
| `internal/contract/dependency_policy_test.go` | Modify | Lock helper behavior: registered non-production absence passes constructors; production and unknown absence fail. |
| `internal/app/dependency_contract.go` | Modify | Delegate app constructor checks to `contract.RequireDependency`. |
| `internal/platform/toolbridge/handler.go` | Modify | Use `contract.RequireDependency` in `validateToolbridgeDependencies` and `validateToolbridgeConfigDependency`. |
| `internal/platform/toolbridge/dependency_contract_test.go` | Modify | Update constructor-absence tests so registered non-production absence expects `nil`, while production and runtime typed-error tests remain strict. |
| `internal/archtest/dependency_optional_boundary_test.go` | Modify | Extend existing optional-boundary checks to prove dependency-absence classifications pass `RequireDependency` and production remains fail-fast. |
| `internal/archtest/code_size_guard_test.go` | Modify | Add an AI visual hotspot ratchet using `archtest.CountEffectiveLines`; do not create a raw-line metric. |
| `cmd/mcp-orch/orchestration/nodeexec/*.go` | Modify | Split automation executor internals while preserving package behavior. |
| `internal/provider/codexapp/*.go` | Modify | Move driver internals into existing owner files before creating new production files. |
| `internal/module/thread/*.go` | Modify | Split lifecycle helpers without changing runtime typed dependency semantics. |
| `internal/platform/toolbridge/*.go` | Modify | Split handler internals while keeping constructor wiring in `handler.go`. |
| `cmd/mcp-lsp/tools/*.go` | Modify | Split factory helpers while preserving the seven-tool public surface. |
| `internal/mcpserver/common/tool_error_envelope.go` | Modify | Remove the DB import and make tool-error classification extensible without DB ownership. |
| `internal/mcpserver/common/server.go` | Modify | Add stdio `ServerOption` for tool-error classifiers. |
| `internal/mcpserver/common/http_transport.go` | Modify | Reuse the same classifier option for legacy HTTP transport. |
| `internal/mcpserver/common/server_test.go` | Modify | Prove stdio and legacy HTTP `tools/call` both honor caller-provided tool-error classifiers. |
| `cmd/mcp-orch/runtime.go` | Modify | Inject the orchestration DB-aware classifier into the mcp-orch MCP server. |
| `cmd/mcp-orch/runtime_stdio_test.go` | Modify | Prove mcp-orch stdio `tools/call` classifies duplicate-DAG DB conflicts through the real stdio server wiring. |
| `cmd/mcp-orch/fx.go` | Modify | Use the orchestration classifier for bootstrap scoped `tools/call` error envelopes. |
| `cmd/mcp-orch/fx_test.go` | Modify | Prove bootstrap scoped `tools/call` keeps duplicate-DAG DB conflict as `invalid_input`. |
| `cmd/mcp-orch/http_runner.go` | Modify | Use the orchestration classifier for legacy HTTP `tools/call` error envelopes. |
| `cmd/mcp-orch/http_runner_test.go` | Modify | Prove mcp-orch legacy HTTP `tools/call` classifies duplicate-DAG DB conflicts through the mcp-orch HTTP runner helper. |
| `cmd/mcp-orch/tools/task_tools.go` | Current implementation | Owns DB conflict classification for DAG tools through `ToolErrorClassifier`; do not create a duplicate classifier file. |
| `cmd/mcp-orch/tools/create_dag_contract_test.go` | Modify | Move DAG conflict envelope expectations from common defaults to the orchestration classifier. |
| `cmd/mcp-orch/tools/task_create_dag_launch_validation_test.go` | Modify | Prove duplicate-DAG DB conflicts remain `invalid_input` through the orchestration classifier. |
| `internal/archtest/backend_boundary_matrix_test.go` | Modify | Remove non-orchestration DB allowances and add fixtures for LSP/IDA DB dependency rejection. |

---

### Task 1: Centralize Constructor Dependency Absence

**Files:**
- Modify: `internal/contract/config.go`
- Modify: `internal/contract/dependency_policy_test.go`
- Modify: `internal/app/dependency_contract.go`
- Modify: `internal/platform/toolbridge/handler.go`
- Modify: `internal/platform/toolbridge/dependency_contract_test.go`
- Modify: `internal/archtest/dependency_optional_boundary_test.go`

- [ ] **Step 1: Add contract helper tests**

Add these tests to `internal/contract/dependency_policy_test.go`:

```go
func TestRequireDependencyAllowsRegisteredAbsenceForConstructors(t *testing.T) {
	if err := RequireDependency("runtime_reporter.orchestration_service", DependencyProfileDesktopHost, nil); err != nil {
		t.Fatalf("RequireDependency(desktop registered nil) error = %v, want nil", err)
	}
	if err := RequireDependency("runtime_reporter.orchestration_service", DependencyProfileTest, nil); err != nil {
		t.Fatalf("RequireDependency(test registered nil) error = %v, want nil", err)
	}
	if err := RequireDependency("runtime_reporter.orchestration_service", DependencyProfileProduction, struct{}{}); err != nil {
		t.Fatalf("RequireDependency(production present) error = %v", err)
	}
}

func TestRequireDependencyRejectsProductionAndUnknownAbsence(t *testing.T) {
	if err := RequireDependency("runtime_reporter.orchestration_service", DependencyProfileProduction, nil); err == nil {
		t.Fatal("RequireDependency(production registered nil) error = nil, want production failure")
	}
	if err := RequireDependency("unknown.optional", DependencyProfileDesktopHost, nil); err == nil {
		t.Fatal("RequireDependency(unknown nil) error = nil, want unknown dependency failure")
	}
	if err := RequireDependency("", DependencyProfileDesktopHost, nil); err == nil {
		t.Fatal("RequireDependency(empty name) error = nil, want dependency name failure")
	}
	if err := RequireDependency("runtime_reporter.orchestration_service", "", nil); err == nil {
		t.Fatal("RequireDependency(empty profile) error = nil, want dependency profile failure")
	}
}
```

- [ ] **Step 2: Run the focused red test**

Run:

```bash
./scripts/test_with_guard.sh ./internal/contract -run 'TestRequireDependency' -count=1
```

Expected: FAIL with `undefined: RequireDependency`.

- [ ] **Step 3: Add `RequireDependency` to the contract layer**

Add this function in `internal/contract/config.go` after `MissingDependencyModeError`:

```go
// RequireDependency validates a constructor dependency without converting registered absence into a runtime mode error.
func RequireDependency(name string, profile DependencyProfile, value any) error {
	name = strings.TrimSpace(name)
	if value != nil {
		return nil
	}
	if name == "" {
		return fmt.Errorf("dependency name is required")
	}
	if strings.TrimSpace(string(profile)) == "" {
		return fmt.Errorf("dependency profile is required for %q", name)
	}
	if !isKnownDependencyProfile(profile) {
		return fmt.Errorf("dependency profile %q is not supported for %q", profile, name)
	}
	if _, ok := lookupDependencyAbsencePolicy(name, profile); ok {
		return nil
	}
	if dependencyAbsencePolicyNameExists(name) {
		return fmt.Errorf("dependency %q is required in %s profile", name, profile)
	}
	return fmt.Errorf("unknown dependency absence policy %q in %s profile", name, profile)
}
```

Do not change `MissingDependencyModeError`. Runtime/report operations still need typed `DependencyModeError` values.

- [ ] **Step 4: Delegate app constructor checks**

Change `internal/app/dependency_contract.go` so `Require` becomes:

```go
func (c dependencyContract) Require(name string, value any) error {
	return contract.RequireDependency(name, c.profile, value)
}
```

Remove imports made unused by the change. Keep `dependencyDeferred` and `dependencyUnsupported` available for runtime/report operation boundaries.

- [ ] **Step 5: Move toolbridge constructor checks to the shared helper**

In `internal/platform/toolbridge/handler.go`, replace `toolbridgeMissingDependencyError` usage in constructor validation with this helper:

```go
func requireToolbridgeDependency(name string, profile contract.DependencyProfile, present bool) error {
	if present {
		return nil
	}
	return contract.RequireDependency(name, profile, nil)
}
```

Then update the validation loop shape to use `present` instead of `missing`:

```go
for _, dependency := range []struct {
	name    string
	present bool
}{
	{name: toolbridgeDependencyDispatcher, present: in.Dispatcher != nil && in.Emitter != nil},
	{name: toolbridgeDependencyWorkdirResolver, present: in.Resolver != nil},
	{name: toolbridgeDependencyPreferences, present: in.Preferences != nil},
	{name: toolbridgeDependencyLifecycleBackfiller, present: in.Lifecycle != nil},
	{name: toolbridgeDependencyLifecyclePolicyReader, present: in.LifecyclePolicy != nil},
	{name: toolbridgeDependencyAgentThreadLookup, present: in.BindingStore != nil},
	{name: toolbridgeDependencyThreadConfigOverride, present: in.ThreadStore != nil},
	{name: toolbridgeDependencyHostTools, present: !toolbridgeHostToolsMissing(in.HostTools)},
	{name: toolbridgeDependencySkillTools, present: in.SkillTools != nil},
} {
	if err := requireToolbridgeDependency(dependency.name, profile, dependency.present); err != nil {
		return err
	}
}
```

In `validateToolbridgeConfigDependency`, replace the nil-config branch with:

```go
if cfg == nil || strings.TrimSpace(string(cfg.Dependency.Profile)) == "" {
	return contract.RequireDependency(toolbridgeDependencyConfig, profile, nil)
}
```

Delete `toolbridgeMissingDependencyError` after no call sites remain.

- [ ] **Step 6: Update existing toolbridge constructor tests**

In `internal/platform/toolbridge/dependency_contract_test.go`, update only the allowed branches in `TestToolbridgeDesktopProfileAllowsOnlyNamedMissingDependencies` and `TestToolbridgeTestProfileAllowsOnlyTestNamedMissingDependencies`.

Replace:

```go
if !contract.IsDependencyModeError(err, dependency, contract.DependencyProfileDesktopHost, contract.ErrUnsupportedDependencyMode) {
	t.Fatalf("%s error = %v, want desktop typed unsupported", dependency, err)
}
```

with:

```go
if err != nil {
	t.Fatalf("%s error = %v, want nil for registered desktop constructor absence", dependency, err)
}
```

Replace:

```go
if !contract.IsDependencyModeError(err, dependency, contract.DependencyProfileTest, contract.ErrUnsupportedDependencyMode) {
	t.Fatalf("%s error = %v, want test typed unsupported", dependency, err)
}
```

with:

```go
if err != nil {
	t.Fatalf("%s error = %v, want nil for registered test constructor absence", dependency, err)
}
```

Do not change production missing-dependency assertions. Do not change runtime/report operation tests that intentionally assert `DependencyModeError`.

- [ ] **Step 7: Extend the existing optional-boundary guard**

Do not create `internal/archtest/dependency_profile_optional_guard_test.go`.

Add this test to `internal/archtest/dependency_optional_boundary_test.go`:

```go
func TestOptionalDependencyAbsenceUsesRequireDependency(t *testing.T) {
	t.Parallel()

	classifications := registeredOptionalDependencyClassifications()
	for key, classification := range classifications {
		if classification.category != optionalDependencyAbsence {
			continue
		}
		if err := contract.RequireDependency(classification.dependency, classification.profile, nil); err != nil {
			t.Fatalf("%s: RequireDependency(%q, %s, nil) error = %v, want nil", key, classification.dependency, classification.profile, err)
		}
		if err := contract.RequireDependency(classification.dependency, contract.DependencyProfileProduction, nil); err == nil {
			t.Fatalf("%s: RequireDependency(%q, production, nil) error = nil, want production failure", key, classification.dependency)
		}
	}
}
```

Keep the current classification key format (`relpath:kind:value`). Do not introduce `StructName.FieldName` / `RelPath#Symbol`.

- [ ] **Step 8: Run focused verification**

Run:

```bash
gofmt -w internal/contract/config.go internal/contract/dependency_policy_test.go internal/app/dependency_contract.go internal/platform/toolbridge/handler.go internal/platform/toolbridge/dependency_contract_test.go internal/archtest/dependency_optional_boundary_test.go
./scripts/test_with_guard.sh ./internal/contract ./internal/app ./internal/platform/toolbridge ./internal/archtest -run 'TestRequireDependency|TestOptionalDependency|TestToolbridge|TestDependencyAbsence' -count=1
```

Expected: PASS. Constructor absence is centralized; runtime typed-error tests still pass.

---

### Task 2: Ratchet AI Visual Review Hotspots Through Existing Metrics

**Files:**
- Modify: `internal/archtest/code_size_guard_test.go`
- Modify same-package files under:
  - `cmd/mcp-orch/orchestration/nodeexec/`
  - `internal/provider/codexapp/`
  - `internal/module/thread/`
  - `internal/platform/toolbridge/`
  - `cmd/mcp-lsp/tools/`

- [ ] **Step 1: Add the effective-line hotspot ratchet**

Add this test to `internal/archtest/code_size_guard_test.go`:

```go
func TestAIVisualBoundaryHotspotsStayReadable(t *testing.T) {
	root := repoRoot(t)
	targets := map[string]int{
		"cmd/mcp-orch/orchestration/nodeexec/executor_automation.go": 650,
		"internal/provider/codexapp/driver.go":                       650,
		"internal/module/thread/lifecycle.go":                        650,
		"internal/platform/toolbridge/handler.go":                    650,
		"cmd/mcp-lsp/tools/factory.go":                               650,
	}
	var violations []string
	for rel, limit := range targets {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			violations = append(violations, rel+": "+err.Error())
			continue
		}
		lines := archtest.CountEffectiveLines(data)
		if lines > limit {
			violations = append(violations, fmt.Sprintf("%s has %d effective lines; limit is %d", rel, lines, limit))
		}
	}
	slices.Sort(violations)
	if len(violations) > 0 {
		t.Fatalf("AI visual hotspot budget violations:\n%s", strings.Join(violations, "\n"))
	}
}
```

Add imports made necessary by the snippet. This test must use `archtest.CountEffectiveLines`; do not use `strings.Count`.

- [ ] **Step 2: Run the red guard**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestAIVisualBoundaryHotspotsStayReadable -count=1
```

Expected: FAIL until the named files are split by effective lines.

- [ ] **Step 3: Split according to the fixed package-count budget**

Before editing each package, record production Go file count:

```bash
set -euo pipefail
find internal/provider/codexapp -maxdepth 1 -name '*.go' ! -name '*_test.go' ! -name 'factory.go' -print | wc -l
find internal/module/thread -maxdepth 1 -name '*.go' ! -name '*_test.go' ! -name 'factory.go' -print | wc -l
find internal/platform/toolbridge -maxdepth 1 -name '*.go' ! -name '*_test.go' ! -name 'factory.go' -print | wc -l
find cmd/mcp-lsp/tools -maxdepth 1 -name '*.go' ! -name '*_test.go' ! -name 'factory.go' -print | wc -l
find cmd/mcp-orch/orchestration/nodeexec -maxdepth 1 -name '*.go' ! -name '*_test.go' ! -name 'factory.go' -print | wc -l
```

These counts must match the archtest rule in `internal/archtest/guardlib.go`: `_test.go` and `factory.go` do not count toward package file count. The allowed production-file budget for this task is fixed:

| Package | Current guard-equivalent count | MaxPackageFiles | Allowed new production files | Required destination policy |
| --- | ---: | ---: | ---: | --- |
| `internal/provider/codexapp` | 29 | 30 | 1 | Create exactly `internal/provider/codexapp/driver_surface.go`; do not create any other production file in this package. |
| `internal/module/thread` | 30 | 30 | 0 | Use existing `lifecycle_helpers.go`, `lifecycle_fork.go`, and `event_publish.go`; do not create a production file in this package. |
| `internal/platform/toolbridge` | 29 | 30 | 1 | Create exactly `internal/platform/toolbridge/handler_response.go`; do not create any other production file in this package. |
| `cmd/mcp-lsp/tools` | 26 | 30 | 3 | Create exactly `factory_scope.go`, `factory_bindings.go`, and `factory_results.go`; do not create any other production file in this package. |
| `cmd/mcp-orch/orchestration/nodeexec` | 14 | 30 | 3 | Create exactly `executor_automation_decode.go`, `executor_automation_prompt.go`, and `executor_automation_result.go`; do not create any other production file in this package. |

Do not add package-count freeze entries. Do not delete unrelated same-package production files to buy budget.

- [ ] **Step 4: Split the five hotspots by owner**

Move behavior-preserving helper groups as follows:

| Current file | Required owner extraction |
| --- | --- |
| `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go` | Move decode/config helpers to `executor_automation_decode.go`; move prompt/input rendering helpers to `executor_automation_prompt.go`; move result routing helpers to `executor_automation_result.go`. |
| `internal/provider/codexapp/driver.go` | Move config normalization, process launch/acquire input construction, and tool-surface wiring helpers to `driver_surface.go`. |
| `internal/module/thread/lifecycle.go` | Move status mutation and session binding helpers to existing `lifecycle_helpers.go`; move fork-specific lifecycle helpers to existing `lifecycle_fork.go`; move event projection helpers to existing `event_publish.go`. |
| `internal/platform/toolbridge/handler.go` | Move response envelope helpers and trusted scope helpers to `handler_response.go`; move host-tool dispatch routing helpers into existing `handler_host_tools.go`; move DAG launch routing helpers into existing `handler_dag_launch.go`. |
| `cmd/mcp-lsp/tools/factory.go` | Move workspace-scope helpers to `factory_scope.go`; move handler binding helpers to `factory_bindings.go`; move result formatting helpers to `factory_results.go`. |

Keep public types and constructor entrypoints in the original files. Do not change exported behavior and tool names.

- [ ] **Step 5: Run behavior-preserving verification**

Run:

```bash
set -euo pipefail
gofmt -w cmd/mcp-orch/orchestration/nodeexec internal/provider/codexapp internal/module/thread internal/platform/toolbridge cmd/mcp-lsp/tools internal/archtest/code_size_guard_test.go
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/... ./internal/provider/codexapp ./internal/module/thread ./internal/platform/toolbridge ./cmd/mcp-lsp ./internal/archtest -run 'TestAIVisualBoundaryHotspotsStayReadable|TestCodeSizeGuard|TestToolbridge|TestFactory|TestLifecycle|TestDriver|TestAutomation' -count=1
```

Expected: PASS. The five named files are each `<=650` effective lines, package production file counts match the Task 2 budget table, and existing behavior tests pass.

---

### Task 3: Remove Non-Orchestration Sidecar DB Dependency Before Allowlist Deletion

**Files:**
- Modify: `internal/mcpserver/common/tool_error_envelope.go`
- Modify: `internal/mcpserver/common/server.go`
- Modify: `internal/mcpserver/common/http_transport.go`
- Modify: `internal/mcpserver/common/server_test.go`
- Modify: `cmd/mcp-orch/runtime.go`
- Modify: `cmd/mcp-orch/runtime_stdio_test.go`
- Modify: `cmd/mcp-orch/fx.go`
- Modify: `cmd/mcp-orch/fx_test.go`
- Modify: `cmd/mcp-orch/http_runner.go`
- Modify: `cmd/mcp-orch/http_runner_test.go`
- Modify: `cmd/mcp-orch/tools/task_tools.go`
- Modify: `cmd/mcp-orch/tools/create_dag_contract_test.go`
- Modify: `cmd/mcp-orch/tools/task_create_dag_launch_validation_test.go`
- Create: `cmd/mcp-orch/tools/error_classifier_test.go`
- Modify: `internal/archtest/backend_boundary_matrix_test.go`

- [ ] **Step 1: Add the transitive DB dependency red proof**

Add this test to `internal/archtest/backend_boundary_matrix_test.go`:

```go
func TestNonOrchestrationSidecarsDoNotDependOnPlatformDB(t *testing.T) {
	root := repoRoot(t)
	for _, relPkg := range []string{"cmd/mcp-lsp", "cmd/mcp-ida"} {
		for _, dep := range goListDeps(t, root, relPkg) {
			if dep == internalPrefix("internal/platform/db") {
				t.Fatalf("%s has transitive dependency on %s", relPkg, dep)
			}
		}
	}
}
```

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestNonOrchestrationSidecarsDoNotDependOnPlatformDB -count=1
```

Expected: FAIL before the common DB import is removed.

- [x] **Step 2: Make common tool-error classification injectable**

Current implementation note: HEAD uses one classifier hook, `ToolErrorClassifier func(toolName string, err error) (ToolErrorClassification, bool)`, plus `NewToolErrorEnvelopeWithClassifier`. Do not add a varargs `NewToolErrorEnvelopeWithClassifiers` API unless a second concrete caller proves it is necessary.

In `internal/mcpserver/common/tool_error_envelope.go`, add:

```go
type ToolErrorClassifier func(toolName string, err error) (code string, retryable bool, hint string, meta map[string]any, ok bool)
```

Change envelope construction so caller-provided classifiers run before default classifiers:

```go
func NewToolErrorEnvelopeWithClassifier(toolName, languageID string, err error, extraMeta map[string]any, classifier ToolErrorClassifier) ToolErrorEnvelope {
	code, retryable, hint, codedMeta := ClassifyToolErrorWithClassifier(toolName, err, classifier)
	meta := map[string]any{"tool": strings.TrimSpace(toolName)}
	if languageID = normalizeEnvelopeLanguageID(languageID); languageID != "" {
		meta["language_id"] = languageID
	}
	for k, v := range codedMeta {
		if strings.TrimSpace(k) != "" {
			meta[k] = v
		}
	}
	for k, v := range extraMeta {
		if strings.TrimSpace(k) != "" {
			meta[k] = v
		}
	}
	return ToolErrorEnvelope{Success: false, Error: errorText(err), Code: code, Retryable: retryable, Hint: hint, Meta: meta}
}

func ClassifyToolErrorWithClassifier(toolName string, err error, classifier ToolErrorClassifier) (code string, retryable bool, hint string, meta map[string]any) {
	if err == nil {
		return "unknown", false, "next: inspect tool call arguments and retry with a concrete error", nil
	}
	if classifier != nil {
		if classification, ok := classifier(toolName, err); ok {
			return classification.Code, classification.Retryable, classification.Hint, classification.Meta
		}
	}
	return ClassifyToolError(toolName, err)
}
```

Keep `NewToolErrorEnvelope` and `NewToolErrorEnvelopeWithMeta` as wrappers that pass no custom classifiers.

- [ ] **Step 3: Remove DB ownership from common defaults**

In `internal/mcpserver/common/tool_error_envelope.go`, delete the `platformdb` import and delete the classifier that calls `platformdb.IsConflict`.

After this step, `internal/mcpserver/common` must not contain `internal/platform/db`.

- [x] **Step 4: Add classifier options to stdio and HTTP servers**

Current implementation note: HEAD uses `WithToolErrorClassifier` and `WithHTTPToolErrorClassifier` with a single classifier. Do not introduce plural classifier state.

In `internal/mcpserver/common/server.go`, add:

```go
type ServerOption func(*Server)

func WithToolErrorClassifier(classifier ToolErrorClassifier) ServerOption {
	return func(s *Server) {
		s.toolErrorClassifier = classifier
	}
}
```

Add `toolErrorClassifier ToolErrorClassifier` to `Server`, change `NewServer` to accept `opts ...ServerOption`, and apply options after constructing the server.

In the `tools/call` error path, replace:

```go
value = NewToolErrorEnvelope(params.Name, err)
```

with:

```go
value = NewToolErrorEnvelopeWithClassifier(params.Name, "", err, nil, s.toolErrorClassifier)
```

In `internal/mcpserver/common/http_transport.go`, add `toolErrorClassifier ToolErrorClassifier` to `HTTPServer` and a matching `WithHTTPToolErrorClassifier` option. In the HTTP `tools/call` error path, call `NewToolErrorEnvelopeWithClassifier` with `h.toolErrorClassifier`.

- [x] **Step 5: Move DAG conflict classification to mcp-orch**

Add or preserve this logic in the existing `cmd/mcp-orch/tools/task_tools.go`; do not create a separate classifier file:

```go
package tools

import (
	"strings"

	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func ToolErrorClassifier(toolName string, err error) (mcpcommon.ToolErrorClassification, bool) {
	if !strings.EqualFold(strings.TrimSpace(toolName), "task_create_dag") || !platformdb.IsConflict(err) {
		return mcpcommon.ToolErrorClassification{}, false
	}
	return mcpcommon.ToolErrorClassification{
		Code: "invalid_input",
		Hint: "next: choose a new dag_key or update the existing DAG with task_dag_apply_ops",
	}, true
}

var _ mcpcommon.ToolErrorClassifier = ToolErrorClassifier
```

- [ ] **Step 6: Inject the orchestration classifier in every mcp-orch tools/call envelope path**

In `cmd/mcp-orch/runtime.go`, change `newStdioServer` to:

```go
return common.NewServer(
	"mcp-orch",
	"dev",
	transport,
	registryToolProvider{registry: registry},
		common.WithToolErrorClassifier(tools.ToolErrorClassifier),
), nil
```

In `cmd/mcp-orch/fx.go`, change the bootstrap scoped `tools/call` error path inside `handleScopedToolsCallWithCaller` from:

```go
result = common.NewToolErrorEnvelope(req.Name, err)
```

to:

```go
result = common.NewToolErrorEnvelopeWithClassifier(req.Name, "", err, nil, tools.ToolErrorClassifier)
```

In `cmd/mcp-orch/http_runner.go`, add this helper:

```go
func newOrchHTTPServer(toolProvider common.ToolProvider, bearerToken string) *common.HTTPServer {
	return common.NewHTTPServer(
		httpBinaryName,
		"dev",
		toolProvider,
		common.WithBearerToken(bearerToken),
		common.WithHTTPToolErrorClassifier(tools.ToolErrorClassifier),
	)
}
```

Then change the HTTP server creation in `(*httpRunner).Run` to:

```go
srv := newOrchHTTPServer(r.tools, r.bearerToken)
```

Do not inject this classifier in `cmd/mcp-lsp` / `cmd/mcp-ida`.

- [ ] **Step 7: Move DAG conflict envelope tests off common defaults**

In `cmd/mcp-orch/tools/create_dag_contract_test.go`, replace the duplicate-DAG conflict envelope calls with the orchestration classifier:

```go
env := mcpcommon.NewToolErrorEnvelopeWithClassifier("task_create_dag", "", platformdb.ErrConflict, nil, ToolErrorClassifier)
```

and:

```go
env := mcpcommon.NewToolErrorEnvelopeWithClassifier("task_create_dag", "", err, nil, ToolErrorClassifier)
```

In `cmd/mcp-orch/tools/task_create_dag_launch_validation_test.go`, replace both `mcpcommon.NewToolErrorEnvelope("task_create_dag", err)` calls with:

```go
env := mcpcommon.NewToolErrorEnvelopeWithClassifier("task_create_dag", "", err, nil, ToolErrorClassifier)
```

In `cmd/mcp-orch/runtime_stdio_test.go`, add a real mcp-orch stdio wiring test. Add imports for `context`, `encoding/json`, `io`, `os`, `github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools`, `github.com/anthropic-ai/super-agent-v3/internal/contract`, `github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common`, and `platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"` as needed:

```go
type conflictDAGCreateRuntime struct{}

func (conflictDAGCreateRuntime) CreateDAG(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
	return contract.DAGDetail{}, platformdb.ErrConflict
}

func TestMCPOrchStdioToolsCallClassifiesTaskCreateDAGConflict(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinR.Close()
		_ = stdoutR.Close()
	})

	prevStdin := os.Stdin
	os.Stdin = stdinR
	t.Cleanup(func() { os.Stdin = prevStdin })

	prevStdout := mcpStdout.Swap(stdoutW)
	t.Cleanup(func() {
		mcpStdout.Store(prevStdout)
	})

	registry := tools.NewRegistry(tools.Dependencies{
		ToolPorts: tools.ToolPorts{DAGCreate: conflictDAGCreateRuntime{}},
	})
	server, err := newStdioServer(registry)
	if err != nil {
		t.Fatalf("newStdioServer() error = %v", err)
	}

	runErr := make(chan error, 1)
	go func() {
		runErr <- server.Run(context.Background())
		_ = stdoutW.Close()
	}()

	_, _ = io.WriteString(stdinW, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_create_dag","arguments":{"dag_key":"dup","title":"Dup","nodes":[{"node_key":"n1","title":"N1"}]},"_agentId":"agent-1"}}`+"\n")
	_ = stdinW.Close()
	raw, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("Run() error = %v; raw=%s", err, raw)
	}

	envelope := decodeMCPOrchToolErrorEnvelope(t, raw)
	if envelope.Code != "invalid_input" {
		t.Fatalf("Code = %q, want invalid_input; raw=%s", envelope.Code, raw)
	}
}
```

In `cmd/mcp-orch/http_runner_test.go`, add a real mcp-orch HTTP wiring test. Reuse `conflictDAGCreateRuntime` and `decodeMCPOrchToolErrorEnvelope` from `runtime_stdio_test.go`; add imports for `io`, `net/http`, and `strings` as needed:

```go
func TestMCPOrchHTTPToolsCallClassifiesTaskCreateDAGConflict(t *testing.T) {
	registry := tools.NewRegistry(tools.Dependencies{
		ToolPorts: tools.ToolPorts{DAGCreate: conflictDAGCreateRuntime{}},
	})
	server := newOrchHTTPServer(registryToolProvider{registry: registry}, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr, err := server.Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithCancel(context.Background())
		defer stopCancel()
		_ = server.Stop(stopCtx)
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"task_create_dag","arguments":{"dag_key":"dup","title":"Dup","nodes":[{"node_key":"n1","title":"N1"}]},"_agentId":"agent-1"}}`))
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP POST error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	envelope := decodeMCPOrchToolErrorEnvelope(t, body)
	if envelope.Code != "invalid_input" {
		t.Fatalf("Code = %q, want invalid_input; body=%s", envelope.Code, body)
	}
}
```

Add this shared decoder in `cmd/mcp-orch/runtime_stdio_test.go`:

```go
func decodeMCPOrchToolErrorEnvelope(t *testing.T, raw []byte) common.ToolErrorEnvelope {
	t.Helper()
	var response struct {
		Result struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("unmarshal response: %v; raw=%s", err, raw)
	}
	var envelope common.ToolErrorEnvelope
	if err := json.Unmarshal(response.Result.StructuredContent, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v; structured=%s raw=%s", err, response.Result.StructuredContent, raw)
	}
	return envelope
}
```

In `internal/mcpserver/common/server_test.go`, add stdio and legacy HTTP coverage for custom classifier options. This test must not import `internal/platform/db`:

```go
func TestToolsCallUsesCustomToolErrorClassifier(t *testing.T) {
	const message = "duplicate dag"
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":41,"method":"tools/call","params":{"name":"task_create_dag","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New(message)
	}}
	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider, WithToolErrorClassifier(testInvalidInputClassifier))

	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, output.Bytes())
	if envelope.Code != "invalid_input" {
		t.Fatalf("envelope code = %q, want invalid_input; output=%s", envelope.Code, output.String())
	}
}

func TestHTTPToolsCallUsesCustomToolErrorClassifier(t *testing.T) {
	const message = "duplicate dag"
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New(message)
	}}
	server := NewHTTPServer("test", "dev", provider, WithHTTPToolErrorClassifiers(testInvalidInputClassifier))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"task_create_dag","arguments":{}}}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, rec.Body.Bytes())
	if envelope.Code != "invalid_input" {
		t.Fatalf("envelope code = %q, want invalid_input; body=%s", envelope.Code, rec.Body.String())
	}
}

func testInvalidInputClassifier(toolName string, err error) (string, bool, string, map[string]any, bool) {
	if strings.TrimSpace(toolName) != "task_create_dag" || err == nil || err.Error() != "duplicate dag" {
		return "", false, "", nil, false
	}
	return "invalid_input", false, "next: choose a new dag_key", nil, true
}
```

In `cmd/mcp-orch/fx_test.go`, add `platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"` to imports and add bootstrap scoped coverage:

```go
func TestHandleScopedToolsCallWithCallerClassifiesTaskCreateDAGConflict(t *testing.T) {
	params := json.RawMessage(`{"name":"task_create_dag","arguments":{},"_agentId":"agent-1"}`)
	result, err := handleScopedToolsCallWithCaller(context.Background(), "orch", params, func(context.Context, string, json.RawMessage) (any, error) {
		return nil, platformdb.ErrConflict
	})
	if err != nil {
		t.Fatalf("handleScopedToolsCallWithCaller() error = %v", err)
	}
	envelope := decodeScopedToolEnvelope(t, result)
	if envelope.Code != "invalid_input" {
		t.Fatalf("Code = %q, want invalid_input", envelope.Code)
	}
	if envelope.Retryable {
		t.Fatal("Retryable = true, want false")
	}
}
```

Create `cmd/mcp-orch/tools/error_classifier_test.go` with this negative assertion:

```go
package tools

import (
	"testing"

	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func TestCommonDefaultDoesNotClassifyTaskCreateDAGConflict(t *testing.T) {
	env := mcpcommon.NewToolErrorEnvelope("task_create_dag", platformdb.ErrConflict)
	if env.Code == "invalid_input" {
		t.Fatalf("common default classified task_create_dag conflict as %q; want DB-aware classification only in mcp-orch", env.Code)
	}
}
```

Expected behavior after this step: common custom-classifier options are proven without importing DB, duplicate `task_create_dag` DB conflicts are proven as `invalid_input` through mcp-orch stdio, mcp-orch legacy HTTP, and bootstrap scoped `tools/call`, and common defaults do not import or recognize `platformdb.ErrConflict` / `platformdb.IsConflict`.

- [ ] **Step 8: Delete the non-orchestration DB allowances**

In `internal/archtest/backend_boundary_matrix_test.go`, remove exactly these entries from `mcpSidecarImportAllowances()`:

```go
{"internal/platform/db", "LSP sidecar reuses MCP bootstrap DB lifecycle for sidecar readiness"},
{"internal/platform/db", "IDA sidecar reuses MCP bootstrap DB lifecycle for sidecar readiness"},
```

Keep the `cmd/mcp-orch/**/*.go` DB allowance.

- [ ] **Step 9: Add fixture cases for LSP and IDA DB dependency rejection**

In `TestBackendBoundaryMatrixFixturesRejectKnownViolations`, add:

```go
{
	name:   "mcp_lsp_db_dependency_is_forbidden",
	ruleID: "mcp_sidecar_narrow_import_surface",
	deps: map[string][]string{
		"cmd/mcp-lsp": {internalPrefix("internal/platform/db")},
	},
	wantHits: []string{"cmd/mcp-lsp depends on", "internal/platform/db"},
},
{
	name:   "mcp_ida_db_dependency_is_forbidden",
	ruleID: "mcp_sidecar_narrow_import_surface",
	deps: map[string][]string{
		"cmd/mcp-ida": {internalPrefix("internal/platform/db")},
	},
	wantHits: []string{"cmd/mcp-ida depends on", "internal/platform/db"},
},
```

- [ ] **Step 10: Run boundary verification**

Run:

```bash
set -euo pipefail
gofmt -w internal/mcpserver/common/tool_error_envelope.go internal/mcpserver/common/server.go internal/mcpserver/common/http_transport.go internal/mcpserver/common/server_test.go cmd/mcp-orch/runtime.go cmd/mcp-orch/runtime_stdio_test.go cmd/mcp-orch/fx.go cmd/mcp-orch/fx_test.go cmd/mcp-orch/http_runner.go cmd/mcp-orch/http_runner_test.go cmd/mcp-orch/tools/task_tools.go cmd/mcp-orch/tools/error_classifier_test.go cmd/mcp-orch/tools/create_dag_contract_test.go cmd/mcp-orch/tools/task_create_dag_launch_validation_test.go internal/archtest/backend_boundary_matrix_test.go
./scripts/test_with_guard.sh ./internal/mcpserver/common ./cmd/mcp-orch ./cmd/mcp-lsp ./cmd/mcp-ida ./internal/archtest -run 'TestToolError|TestClassifyToolError|TestToolsCallUsesCustomToolErrorClassifier|TestHTTPToolsCallUsesCustomToolErrorClassifier|TestMCPOrchStdioToolsCallClassifiesTaskCreateDAGConflict|TestMCPOrchHTTPToolsCallClassifiesTaskCreateDAGConflict|TestHandleScopedToolsCallWithCallerClassifiesTaskCreateDAGConflict|TestCommonDefaultDoesNotClassifyTaskCreateDAGConflict|TestNonOrchestrationSidecarsDoNotDependOnPlatformDB|TestBackendBoundaryMatrix' -count=1
deps="$(go list -deps ./cmd/mcp-lsp ./cmd/mcp-ida)"
if printf '%s\n' "$deps" | rg '^github.com/anthropic-ai/super-agent-v3/internal/platform/db$'; then
  echo "non-orchestration sidecar still depends on internal/platform/db"
  exit 1
fi
```

Expected: PASS. The shell proof must print no DB package and exit 0.

---

## Final Verification

Run the full boundary proof bundle:

```bash
set -euo pipefail
git diff --check
./scripts/test_with_guard.sh ./internal/contract ./internal/app ./internal/platform/toolbridge ./internal/module/thread ./internal/provider/codexapp ./internal/mcpserver/common ./cmd/mcp-lsp ./cmd/mcp-ida ./cmd/mcp-orch ./cmd/mcp-orch/orchestration/... ./internal/archtest -count=1
deps="$(go list -deps ./cmd/mcp-lsp ./cmd/mcp-ida)"
if printf '%s\n' "$deps" | rg '^github.com/anthropic-ai/super-agent-v3/internal/platform/db$'; then
  echo "non-orchestration sidecar still depends on internal/platform/db"
  exit 1
fi
```

Expected result:

```text
git diff --check exits 0
test_with_guard exits 0
go list DB dependency proof exits 0 with no package printed
```

## Merge Gate

The work is not mergeable unless all of these are true:

- Constructor-time missing dependency enforcement uses `contract.RequireDependency` in app/toolbridge constructor validation.
- Runtime/report operation boundaries still return typed `DependencyModeError` where callers inspect deferred/unsupported dependency mode.
- No new optional dependency guard file exists; `internal/archtest/dependency_optional_boundary_test.go` remains the only optional-boundary fact source.
- The AI visual hotspot guard uses `archtest.CountEffectiveLines`, not raw newline counts.
- The five AI visual hotspot files are each `<=650` effective lines.
- Production file counts follow the Task 2 budget table exactly; saturated packages stay at the current guard-equivalent count.
- `internal/mcpserver/common` does not import `internal/platform/db`.
- `task_create_dag` duplicate-DAG DB conflicts are classified as `invalid_input` through mcp-orch stdio, legacy HTTP, and bootstrap scoped `tools/call`.
- Common default tool-error classification does not import `internal/platform/db` and does not classify `platformdb.ErrConflict` / `platformdb.IsConflict`; generic `database_schema_missing` string classification remains in common.
- `cmd/mcp-lsp` and `cmd/mcp-ida` have no direct and no transitive dependency on `internal/platform/db`.
- `mcpSidecarImportAllowances()` contains `internal/platform/db` only in the `cmd/mcp-orch/**/*.go` block.
- None of these are introduced to make tests pass: noop, fallback, compatibility branch, readiness package, allowlist broadening.
