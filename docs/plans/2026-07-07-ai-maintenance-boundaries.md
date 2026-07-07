# AI Maintenance Boundaries Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Parallel dispatch companion:** Use [2026-07-07-ai-maintenance-boundaries-parallel-agents.md](./2026-07-07-ai-maintenance-boundaries-parallel-agents.md) when converting this plan into multi-agent implementation, review, recovery, and adjudication work.

**Goal:** Make the architecture easier for AI workers to maintain by replacing ambiguous production optional/noop/fallback paths with explicit mode-aware contracts, moving frontend work behind feature-owned surfaces, and turning provider onboarding into a repeatable scaffold plus contract-test suite.

**Architecture:** Split the work into three independent lanes. Lane A adds a typed dependency profile and converts selected hidden noop paths into explicit fail-fast or typed-unsupported behavior. Lane B strengthens frontend page ownership by requiring feature services/adapters and golden DTO tests instead of direct page-to-backend coupling. Lane C adds a provider scaffold and shared contract tests that every provider adapter must satisfy before it can enter the Fx graph.

**Tech Stack:** Go 1.25.7, Uber Fx, SQLite/sqlc, MCP/toolbridge, Codex/Claude provider adapters, React/Vite/Vitest, repo-local LSP MCP tools, repo guard scripts.

**Verification Surface:** `./scripts/test_with_guard.sh ./cmd/mcp-lsp ./cmd/mcp-orch ./internal/app ./internal/module/thread ./internal/platform/config ./internal/platform/toolbridge ./internal/provider/contracttest ./internal/provider/unified ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider ./internal/archtest -count=1`, targeted MCP LSP diagnostics for every modified Go and frontend source file, `cd frontend-app && npm run lint && npm test && npm run build`, `make codemap-check`, `make project-map-check`, `make guard`, `git diff --check`.

---

## Current Boundary

- This is a planning document only. Do not change production Go or React code while landing this file.
- Before executing any lane, run `git status --short` and preserve unrelated dirty files. This repository currently often has active `cmd/mcp-lsp/**`, docs, and frontend test work in progress.
- `docs/plans/README.md` marks plans as historical trace material. Workers must revalidate source, tests, ADRs, `docs/契约/*`, and code maps before editing.
- Use LSP evidence before code edits involving source behavior: `grep` or `structure`, `inspect`, `xref`, `file(read_file)`, and `file(diagnostics)`. If diagnostics cannot be obtained after narrowing, record a blocker with the tool/action, path, error, and retries.
- Five-agent cross-adjudication on 2026-07-07 rejected the first draft as `FAIL_BLOCKING`. This revision incorporates the blocking corrections. If implementation discovers any of the same blockers still present, stop and patch this plan before editing source.

## Non-Goals

- Do not implement production or frontend code while landing or reviewing this document.
- Do not migrate `frontend-app/src/entities/client/model/useClientStore.js` in Lane B. That store boundary is a separate project unless a later plan explicitly owns it.
- Do not hand-edit generated code-map outputs. For code-map drift, update source or handwritten code-map text first, run `make codemap-check`, and only run `make codemap-refresh` when the generator reports stale generated files.
- Do not hand-edit generated project-map outputs. For project-map drift, update source or handwritten project-map inputs first, run `make project-map-check`, and only run `make project-map-refresh` when the generator reports stale generated files.
- Do not rebuild provider architecture wholesale. Lane C adds a scaffold and shared contract suite around existing `unified`, `claudecli`, and `codexapp` behavior.

## Abort, Split, And Rollback Gates

- Each lane must be implemented as an independently reviewable change. Lane A, Lane B, and Lane C can be reverted independently unless a later integration commit explicitly joins them.
- Stop immediately if a RED test fails for a reason other than the expected missing symbol or missing behavior. Patch the test or plan before writing implementation code.
- Stop immediately if an LSP diagnostic of severity `Error`, `Warning`, `Information`, or `Hint` appears in a modified source file. Fix it or record a blocker with exact file, line, rule, and why it is intentionally deferred.
- Before Lane A, re-run fresh LSP diagnostics for every source file in the lane. A previously observed Information diagnostic in `internal/platform/config/config.go` around dotenv parsing is historical evidence only: if it still appears, fix it or record an exact blocker; if the fresh LSP diagnostics call returns zero findings after narrowing, a clean diagnostics PASS is allowed. Do not copy diagnostics from old agents, memory, or earlier drafts into verification.
- If a lane touches shared files listed below, the final owner must run that lane's full focused verification before another lane edits the same file.

## Execution Order

1. Lane A first, because dependency-profile semantics affect provider and toolbridge wiring.
2. Lane B can run in parallel with Lane A after the first RED guard is written because it is frontend-only.
3. Lane C can run after Lane A defines the runtime-report and toolbridge dependency contract expected by provider adapters.
4. Final integration runs all verification commands listed in the header. Code-map generated files are updated only through `make codemap-refresh` after `make codemap-check` reports drift.

## Shared Ownership Rules

| Shared file | Sequence | Final owner | Boundary |
|---|---|---|---|
| `internal/contract/dependency.go`, `internal/contract/config.go` | Lane A only | Lane A | Adds dependency-profile config types only. Provider-specific DTOs stay in `internal/dto/provider`. |
| `internal/platform/config/config.go` | Lane A only | Lane A | Parses dependency profile and fails on invalid explicit values. |
| `internal/platform/config/config_test.go`, `cmd/mcp-lsp/runtime_test.go`, `cmd/mcp-orch/sqlite_smoke_test.go` | Lane A only | Lane A | Existing `platform/config.New()` callers must declare the intended dependency bootstrap; do not leave broad test-helper requirements outside owned scope. |
| `internal/platform/toolbridge/module.go`, `internal/platform/toolbridge/handler.go`, `internal/app/toolbridge_adapters.go` | Lane A only | Lane A | Converts production-critical toolbridge dependencies from ambiguous optional behavior into profile-aware contracts. |
| `internal/app/modules_graph_test.go` | Lane A then Lane C | Lane C | Lane A adds dependency-profile graph tests; Lane C adds provider scaffold graph tests. |
| `frontend-app/src/pages/backendApiConsumer.surface.test.js` | Lane B only | Lane B | Converts the current allowlist into a feature-boundary guard. |
| `internal/provider/unified/contract_test.go` | Lane C only | Lane C | May delegate shared provider-contract checks to a new helper package. |

## Lane A: Mode-Aware Dependency Contracts

**Goal:** Make production-critical optional dependencies explicit. A dependency may be absent only when a named profile allows it, and allowed absence must produce typed, observable behavior instead of silent success.

**Files:**
- Create: `internal/contract/dependency.go`
- Modify: `internal/contract/config.go`
- Modify: `internal/platform/config/config.go`
- Create: `internal/platform/config/dependency_profile_test.go`
- Modify: `internal/platform/config/config_test.go`
- Modify: `cmd/mcp-lsp/runtime_test.go`
- Modify: `cmd/mcp-orch/sqlite_smoke_test.go`
- Create: `internal/app/dependency_contract.go`
- Create: `internal/app/dependency_contract_test.go`
- Modify: `internal/app/runtime_reporter_adapter.go`
- Modify: `internal/app/thread_orchestration_adapter.go`
- Modify: `internal/app/thread_orchestration_adapter_test.go`
- Modify: `internal/app/modules.go`
- Modify: `internal/app/modules_graph_test.go`
- Modify: `internal/provider/claudecli/module.go`
- Modify: `internal/provider/claudecli/driver.go`
- Modify: `internal/provider/claudecli/driver_capability_test.go`
- Modify: `internal/provider/codexapp/module.go`
- Modify: `internal/provider/codexapp/support.go`
- Modify: `internal/provider/codexapp/driver_session_test.go`
- Modify: `internal/provider/codexapp/runtime_report_session_url_test.go`
- Modify: `internal/module/thread/lifecycle.go`
- Create: `internal/module/thread/bind_session_generation_status.go`
- Create: `internal/module/thread/lifecycle_bind_session_generation_test.go`
- Modify: `internal/module/thread/module.go`
- Modify: `internal/module/thread/service_constructor.go`
- Modify: `internal/platform/toolbridge/module.go`
- Modify: `internal/platform/toolbridge/handler.go`
- Modify: `internal/platform/toolbridge/diff_gen.go`
- Modify: `internal/platform/toolbridge/handler_managed_launch.go`
- Modify: `internal/app/toolbridge_adapters.go`
- Test: `internal/app/modules_graph_test.go`
- Test: `internal/archtest/fx_graph_test.go`

### Task A1: Add Typed Dependency Profile

- [ ] **Step 1: Write failing config tests**

Create `internal/platform/config/dependency_profile_test.go`:

```go
package config

import "testing"

func TestResolveDependencyProfileAllowsDesktopBootstrapDefault(t *testing.T) {
	got, err := resolveDependencyProfile("", DependencyBootstrapDesktopHost)
	if err != nil {
		t.Fatalf("resolveDependencyProfile() error = %v", err)
	}
	if got != DependencyProfileDesktopHost {
		t.Fatalf("profile = %q, want %q", got, DependencyProfileDesktopHost)
	}
}

func TestResolveDependencyProfileRequiresProductionExplicit(t *testing.T) {
	_, err := resolveDependencyProfile("", DependencyBootstrapProduction)
	if err == nil {
		t.Fatal("resolveDependencyProfile() error = nil, want missing production profile error")
	}
}

func TestParseDependencyProfileAcceptsExplicitDesktopHost(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "desktop")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "desktop_host")
	got, err := dependencyProfileFromEnv()
	if err != nil {
		t.Fatalf("dependencyProfileFromEnv() error = %v", err)
	}
	if got != DependencyProfileDesktopHost {
		t.Fatalf("profile = %q, want %q", got, DependencyProfileDesktopHost)
	}
}

func TestParseDependencyProfileRejectsDesktopProfileInProductionBootstrap(t *testing.T) {
	_, err := resolveDependencyProfile("desktop_host", DependencyBootstrapProduction)
	if err == nil {
		t.Fatal("resolveDependencyProfile() error = nil, want production desktop profile rejection")
	}
}

func TestParseDependencyProfileRejectsUnknownValue(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "maybe-production")
	_, err := dependencyProfileFromEnv()
	if err == nil {
		t.Fatal("dependencyProfileFromEnv() error = nil, want invalid profile error")
	}
}

func TestResolveDependencyProfileAllowsExplicitTestBootstrap(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	got, err := dependencyProfileFromEnv()
	if err != nil {
		t.Fatalf("dependencyProfileFromEnv() error = %v", err)
	}
	if got != DependencyProfileTest {
		t.Fatalf("profile = %q, want %q", got, DependencyProfileTest)
	}
}
```

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/config -run 'TestResolveDependencyProfile|TestParseDependencyProfile' -count=1
```

Expected: FAIL because `dependencyProfileFromEnv` and profile constants do not exist.

- [ ] **Step 2: Add config types and parser**

Create `internal/contract/dependency.go`:

```go
package contract

import (
	"errors"
	"fmt"
)

type DependencyProfile string

const (
	DependencyProfileDesktopHost DependencyProfile = "desktop_host"
	DependencyProfileProduction  DependencyProfile = "production"
	DependencyProfileTest        DependencyProfile = "test"
)

type DependencyBootstrapMode string

const (
	DependencyBootstrapDesktopHost DependencyBootstrapMode = "desktop_host"
	DependencyBootstrapProduction  DependencyBootstrapMode = "production"
	DependencyBootstrapTest        DependencyBootstrapMode = "test"
)

type DependencyConfig struct {
	Profile DependencyProfile
}

var ErrUnsupportedDependencyMode = errors.New("unsupported dependency mode")
var ErrDependencyDeferred = errors.New("dependency deferred by runtime mode")

type DependencyModeError struct {
	Err     error
	Name    string
	Profile DependencyProfile
}

func (e DependencyModeError) Error() string {
	return fmt.Sprintf("%v: %s in %s profile", e.Err, e.Name, e.Profile)
}

func (e DependencyModeError) Unwrap() error {
	return e.Err
}

func NewDependencyModeError(err error, name string, profile DependencyProfile) error {
	return DependencyModeError{Err: err, Name: name, Profile: profile}
}

func IsDependencyModeError(err error, name string, profile DependencyProfile, target error) bool {
	var modeErr DependencyModeError
	if !errors.As(err, &modeErr) {
		return false
	}
	return errors.Is(modeErr.Err, target) && modeErr.Name == name && modeErr.Profile == profile
}
```

Add `Dependency DependencyConfig` to `contract.Config`.

Add to `internal/platform/config/config.go`:

Ensure the file imports `errors`, `fmt`, `os`, `path/filepath`, and `strings` for the dependency-profile parser.

```go
const (
	DependencyProfileDesktopHost = contract.DependencyProfileDesktopHost
	DependencyProfileProduction  = contract.DependencyProfileProduction
	DependencyProfileTest        = contract.DependencyProfileTest

	DependencyBootstrapDesktopHost = contract.DependencyBootstrapDesktopHost
	DependencyBootstrapProduction  = contract.DependencyBootstrapProduction
	DependencyBootstrapTest        = contract.DependencyBootstrapTest
)

func dependencyProfileFromEnv() (contract.DependencyProfile, error) {
	bootstrap, err := dependencyBootstrapModeFromEnv()
	if err != nil {
		return "", err
	}
	return resolveDependencyProfile(os.Getenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE"), bootstrap)
}

func resolveDependencyProfile(raw string, bootstrap contract.DependencyBootstrapMode) (contract.DependencyProfile, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		switch bootstrap {
		case contract.DependencyBootstrapDesktopHost:
			return contract.DependencyProfileDesktopHost, nil
		case contract.DependencyBootstrapTest:
			return contract.DependencyProfileTest, nil
		default:
			return "", fmt.Errorf("SUPER_DOLPHIN_DEPENDENCY_PROFILE is required for %s bootstrap", bootstrap)
		}
	}
	profile := contract.DependencyProfile(raw)
	switch profile {
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileProduction, contract.DependencyProfileTest:
		if profile == contract.DependencyProfileTest && bootstrap != contract.DependencyBootstrapTest {
			return "", fmt.Errorf("test dependency profile is allowed only with test bootstrap")
		}
		if bootstrap == contract.DependencyBootstrapProduction && profile != contract.DependencyProfileProduction {
			return "", fmt.Errorf("%s dependency profile is not allowed for production bootstrap", profile)
		}
		return profile, nil
	default:
		return "", fmt.Errorf("invalid SUPER_DOLPHIN_DEPENDENCY_PROFILE %q", raw)
	}
}

func dependencyBootstrapModeFromEnv() (contract.DependencyBootstrapMode, error) {
	raw := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP"))
	packaged := strings.EqualFold(strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_RUNTIME_MODE")), "packaged")
	role := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_PROCESS_ROLE"))
	switch raw {
	case "test":
		if packaged || role == "sidecar" || !isGoTestBinary() {
			return "", errors.New("test dependency bootstrap is allowed only in Go test binaries")
		}
		return contract.DependencyBootstrapTest, nil
	case "desktop_host":
		return contract.DependencyBootstrapDesktopHost, nil
	case "production":
		return contract.DependencyBootstrapProduction, nil
	case "":
	default:
		return "", fmt.Errorf("invalid SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP %q", raw)
	}
	if packaged {
		return contract.DependencyBootstrapProduction, nil
	}
	switch role {
	case "desktop":
		return contract.DependencyBootstrapDesktopHost, nil
	case "sidecar":
		return contract.DependencyBootstrapProduction, nil
	default:
		return contract.DependencyBootstrapProduction, nil
	}
}

var isGoTestBinary = runningUnderGoTest

func runningUnderGoTest() bool {
	return strings.HasSuffix(filepath.Base(os.Args[0]), ".test")
}
```

Add an explicit parser guard:

```go
func TestParseDependencyBootstrapRejectsUnknownValue(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "desktop")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want invalid bootstrap error", err)
	}
}

func TestParseDependencyProfileRejectsTestProfileWithoutTestBootstrap(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "test")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency profile is allowed only with test bootstrap") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want profile-only test rejection", err)
	}
}

func TestParseDependencyProfileRejectsTestProfileWithExplicitDesktopBootstrap(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "desktop_host")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "test")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency profile is allowed only with test bootstrap") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want desktop bootstrap test profile rejection", err)
	}
}

func TestParseDependencyProfileRejectsTestProfileWithDesktopProcessRole(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "desktop")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "test")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency profile is allowed only with test bootstrap") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want desktop role test profile rejection", err)
	}
}

func TestParseDependencyBootstrapRejectsExplicitTestInPackagedRuntime(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency bootstrap is allowed only in Go test binaries") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want packaged test rejection", err)
	}
}

func TestParseDependencyBootstrapRejectsExplicitTestOutsideGoTestBinary(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	old := isGoTestBinary
	isGoTestBinary = func() bool { return false }
	t.Cleanup(func() { isGoTestBinary = old })
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency bootstrap is allowed only in Go test binaries") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want non-test-binary rejection", err)
	}
}

func TestParseDependencyBootstrapRejectsExplicitTestForSidecar(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	t.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "sidecar")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency bootstrap is allowed only in Go test binaries") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want sidecar test rejection", err)
	}
}
```

In `New()`, set:

```go
profile, err := dependencyProfileFromEnv()
if err != nil {
	return nil, err
}
cfg.Dependency = contract.DependencyConfig{Profile: profile}
```

Every existing Go test that calls `platform/config.New()` without a desktop or sidecar process role must set `SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP=test`. Current caller coverage includes `internal/platform/config/config_test.go`, `cmd/mcp-lsp/runtime_test.go`, and `cmd/mcp-orch/sqlite_smoke_test.go`; keep those files in Lane A owned scope if they need test bootstrap updates. Do not allow profile-only test mode: `SUPER_DOLPHIN_DEPENDENCY_PROFILE=test` without `SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP=test` must fail for production default, explicit `desktop_host`, and `SUPER_DOLPHIN_PROCESS_ROLE=desktop`. Add a focused guard test that fails when a test helper calls `New()` without declaring `SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP=test`.

- [ ] **Step 3: Run config tests**

Use LSP `file(diagnostics)` for:

- `internal/contract/dependency.go`
- `internal/contract/config.go`
- `internal/platform/config/config.go`

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/config ./cmd/mcp-lsp ./cmd/mcp-orch -run 'TestResolveDependencyProfile|TestParseDependencyProfile|TestParseDependencyBootstrap|TestConfig|TestNew_|TestNewManager|TestSQLiteMCPOrchConfig' -count=1
```

Expected: PASS.

### Task A2: Add App Dependency Contract

- [ ] **Step 1: Write failing app tests**

Create `internal/app/dependency_contract_test.go`:

```go
package app

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestDependencyContractRequiresProductionRuntimeReporter(t *testing.T) {
	policy := newDependencyContract(contract.DependencyProfileProduction)
	err := policy.Require("runtime_reporter.orchestration_service", nil)
	if err == nil {
		t.Fatal("Require() error = nil, want missing dependency")
	}
}

func TestDependencyContractAllowsDesktopRuntimeReporterNoop(t *testing.T) {
	policy := newDependencyContract(contract.DependencyProfileDesktopHost)
	if err := policy.Require("runtime_reporter.orchestration_service", nil); err != nil {
		t.Fatalf("desktop runtime reporter dependency error = %v", err)
	}
}

func TestDependencyContractRejectsUnknownOptionalDependency(t *testing.T) {
	policy := newDependencyContract(contract.DependencyProfileDesktopHost)
	err := policy.Require("unknown.optional", nil)
	if err == nil {
		t.Fatal("Require() error = nil, want unknown optional dependency failure")
	}
}

func TestDependencyContractRejectsEmptyProfile(t *testing.T) {
	policy := newDependencyContract("")
	err := policy.Require("runtime_reporter.orchestration_service", nil)
	if err == nil {
		t.Fatal("Require() error = nil, want missing dependency profile failure")
	}
}

func TestDependencyContractTypedUnsupported(t *testing.T) {
	err := dependencyUnsupported("thread.bind_session_generation", contract.DependencyProfileDesktopHost)
	if !errors.Is(err, contract.ErrUnsupportedDependencyMode) {
		t.Fatalf("error = %v, want ErrUnsupportedDependencyMode", err)
	}
}
```

Run:

```bash
./scripts/test_with_guard.sh ./internal/app -run 'TestDependencyContract' -count=1
```

Expected: FAIL because the dependency contract and `contract.ErrUnsupportedDependencyMode` do not exist.

- [ ] **Step 2: Add typed error and policy**

Create `internal/app/dependency_contract.go`:

```go
package app

import (
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type dependencyContract struct {
	profile contract.DependencyProfile
}

func newDependencyContract(profile contract.DependencyProfile) dependencyContract {
	return dependencyContract{profile: profile}
}

func (c dependencyContract) Require(name string, value any) error {
	name = strings.TrimSpace(name)
	if value != nil {
		return nil
	}
	if strings.TrimSpace(string(c.profile)) == "" {
		return fmt.Errorf("app dependency profile is required before resolving %q", name)
	}
	if c.allowsMissing(name) {
		return nil
	}
	return fmt.Errorf("app dependency %q is required in %s profile", name, c.profile)
}

func (c dependencyContract) allowsMissing(name string) bool {
	switch c.profile {
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileTest:
		switch name {
		case "runtime_reporter.orchestration_service":
			return true
		default:
			return false
		}
	case contract.DependencyProfileProduction:
		return false
	default:
		return false
	}
}

func dependencyUnsupported(name string, profile contract.DependencyProfile) error {
	return contract.NewDependencyModeError(contract.ErrUnsupportedDependencyMode, name, profile)
}

func dependencyDeferred(name string, profile contract.DependencyProfile) error {
	return contract.NewDependencyModeError(contract.ErrDependencyDeferred, name, profile)
}
```

- [ ] **Step 3: Run app policy tests**

Use LSP `file(diagnostics)` for:

- `internal/contract/dependency.go`
- `internal/app/dependency_contract.go`
- `internal/app/dependency_contract_test.go`

Run:

```bash
./scripts/test_with_guard.sh ./internal/app -run 'TestDependencyContract' -count=1
```

Expected: PASS.

### Task A3: Convert RuntimeReporter Noop To Mode-Aware Behavior

- [ ] **Step 1: Write failing runtime reporter tests**

Add to `internal/app/dependency_contract_test.go`:

```go
func TestNewRuntimeReporterFailsInProductionWithoutOrchestrationService(t *testing.T) {
	_, err := newRuntimeReporter(runtimeReporterParams{
		Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction},
	})
	if err == nil {
		t.Fatal("newRuntimeReporter() error = nil, want missing orchestration service")
	}
}

func TestNewRuntimeReporterAllowsDesktopExternalOrchestration(t *testing.T) {
	reporter, err := newRuntimeReporter(runtimeReporterParams{
		Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileDesktopHost},
	})
	if err != nil {
		t.Fatalf("newRuntimeReporter() error = %v", err)
	}
	if _, ok := reporter.(desktopExternalRuntimeReporter); !ok {
		t.Fatalf("reporter = %T, want desktopExternalRuntimeReporter", reporter)
	}
	err = reporter.ReportRuntime(context.Background(), contract.RuntimeReport{AgentID: "agent-1", Provider: "codex"})
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("ReportRuntime() error = %v, want ErrDependencyDeferred", err)
	}
}
```

Run:

```bash
./scripts/test_with_guard.sh ./internal/app -run 'TestNewRuntimeReporter' -count=1
```

Expected: FAIL because `newRuntimeReporter` currently returns only `contract.RuntimeReporter` and silently constructs `noopRuntimeReporter`.

- [ ] **Step 2: Change constructor signature**

Change `runtimeReporterParams`:

```go
type runtimeReporterParams struct {
	fx.In

	Service    contract.OrchestrationService `optional:"true"`
	Logger     *slog.Logger                  `optional:"true"`
	Dependency contract.DependencyConfig
}
```

Change constructor:

```go
func newRuntimeReporter(p runtimeReporterParams) (contract.RuntimeReporter, error) {
	if p.Service != nil {
		return orchestrationRuntimeReporter{svc: p.Service}, nil
	}
	policy := newDependencyContract(p.Dependency.Profile)
	if err := policy.Require("runtime_reporter.orchestration_service", p.Service); err != nil {
		return nil, err
	}
	return desktopExternalRuntimeReporter{logger: p.Logger, profile: p.Dependency.Profile}, nil
}
```

Rename `noopRuntimeReporter` to `desktopExternalRuntimeReporter` and keep the method explicit:

```go
type desktopExternalRuntimeReporter struct {
	logger  *slog.Logger
	profile contract.DependencyProfile
}

func (r desktopExternalRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	if r.logger != nil {
		r.logger.Debug("runtime report deferred to external orchestration", "agent_id", report.AgentID, "provider", report.Provider)
	}
	return dependencyDeferred("runtime_reporter.orchestration_service", r.profile)
}
```

Any caller that accepts this deferred result must test `errors.Is(err, contract.ErrDependencyDeferred)`, record a structured deferred status, and continue only when `DependencyProfileDesktopHost` or `DependencyProfileTest` is active. Production callers must treat the same error as a failure.

- [ ] **Step 3: Make provider runtime-report callers mode-aware**

Modify:

- `internal/provider/claudecli/module.go`
- `internal/provider/claudecli/driver.go`
- `internal/provider/claudecli/driver_capability_test.go`
- `internal/provider/codexapp/module.go`
- `internal/provider/codexapp/support.go`
- `internal/provider/codexapp/driver_session_test.go`
- `internal/provider/codexapp/runtime_report_session_url_test.go`

Add `contract.DependencyConfig` to the real Claude and Codex Fx factory parameter structs, not only driver-private helpers. Current source anchors are `internal/provider/claudecli/module.go` `driverFactoryParams` and `internal/provider/codexapp/module.go` `DriverFactoryParams`; both must pass the profile into the driver path used by production `group:"drivers"` registration. Replace warn-and-swallow runtime report handling with a helper whose behavior is tested:

| Profile | `contract.ErrDependencyDeferred` | generic reporter error |
|---|---|---|
| `production` | return error and block start/resume path that needs the report | return error and block |
| `desktop_host` | record structured deferred status and continue | return error |
| `test` | record structured deferred status and continue | return error |

Required tests:

```go
func TestDriverReportRuntimeProductionFailsOnDeferredReporter(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileProduction)}
	driver := newDriverWithRuntimeReporterForTest(reporter, contract.DependencyProfileProduction)
	err := driver.reportRuntimeForTest(context.Background(), "agent-1", "http://127.0.0.1:12345")
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("reportRuntimeForTest() error = %v, want ErrDependencyDeferred", err)
	}
}

func TestDriverReportRuntimeDesktopRecordsDeferredStatus(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileDesktopHost)}
	driver := newDriverWithRuntimeReporterForTest(reporter, contract.DependencyProfileDesktopHost)
	if err := driver.reportRuntimeForTest(context.Background(), "agent-1", "http://127.0.0.1:12345"); err != nil {
		t.Fatalf("reportRuntimeForTest() error = %v", err)
	}
	if !driver.runtimeReportDeferredForTest("agent-1") {
		t.Fatal("runtime report deferred status was not recorded")
	}
}
```

These helper tests are not sufficient on their own. Add RED/GREEN tests on the real provider start/resume paths as well:

- `TestClaudeStartSessionFailsWhenRuntimeReporterReturnsDeferredInProduction`
- `TestClaudeResumeSessionFailsWhenRuntimeReporterReturnsDeferredInProduction`
- `TestCodexStartSessionFailsWhenRuntimeReporterReturnsDeferredInProduction`
- `TestCodexResumeSessionFailsWhenRuntimeReporterReturnsDeferredInProduction`
- matching generic reporter error tests for Claude and Codex start/resume in every profile

The production tests must call the same `StartSession` / `ResumeSession` surfaces used by the registered `group:"drivers"` factory, not only `reportRuntimeForTest`. `reportRuntime` must return an error, and the caller must check that error before returning a session. A provider that logs or records a warning and then returns a usable session while the reporter returned `contract.ErrDependencyDeferred` or a generic error is still failing this task.

Codex must keep the existing session URL port assertion; Claude must keep the no-port stdio report assertion. Both providers must have a sibling generic reporter error test that fails in every profile.

Add constructor/graph RED tests using the real factory parameter surface:

```go
func TestClaudeDriverFactoryRequiresDependencyProfile(t *testing.T) {
	params := completeClaudeDriverFactoryParamsForTest()
	params.Dependency = contract.DependencyConfig{}
	if _, err := newDriverFactoryForTest(params); err == nil {
		t.Fatal("NewDriverFactory() error = nil, want missing dependency profile")
	}
}

func TestCodexDriverFactoryRequiresDependencyProfile(t *testing.T) {
	params := completeCodexDriverFactoryParamsForTest()
	params.Dependency = contract.DependencyConfig{}
	if _, err := provideDriverFactory(params); err == nil {
		t.Fatal("provideDriverFactory() error = nil, want missing dependency profile")
	}
}
```

If the production constructor currently returns only `contract.DriverFactory`, introduce a small validating constructor or make the provider-specific `provide*DriverFactory` return `(factory, error)` so Fx can fail at graph construction. Tests must fail against `internal/provider/claudecli/module.go` and `internal/provider/codexapp/module.go` before driver helper changes, proving the real provider factory is no longer profile-blind.

- [ ] **Step 4: Wire dependency config into app graph**

`config.Module` already provides `*contract.Config` through the type alias. Add a provider in `internal/app/modules.go`:

```go
func provideDependencyConfig(cfg *contract.Config) (contract.DependencyConfig, error) {
	if cfg == nil {
		return contract.DependencyConfig{}, errors.New("app dependency config is required")
	}
	if strings.TrimSpace(string(cfg.Dependency.Profile)) == "" {
		return contract.DependencyConfig{}, errors.New("app dependency profile is required")
	}
	return cfg.Dependency, nil
}
```

Add `provideDependencyConfig` to the `fx.Provide(...)` block.

- [ ] **Step 5: Run graph and app/provider tests**

Use LSP `file(diagnostics)` for:

- `internal/app/runtime_reporter_adapter.go`
- `internal/app/modules.go`
- `internal/app/dependency_contract.go`
- `internal/app/dependency_contract_test.go`
- `internal/provider/claudecli/module.go`
- `internal/provider/claudecli/driver.go`
- `internal/provider/claudecli/driver_capability_test.go`
- `internal/provider/codexapp/module.go`
- `internal/provider/codexapp/support.go`
- `internal/provider/codexapp/driver_session_test.go`
- `internal/provider/codexapp/runtime_report_session_url_test.go`

Run:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/provider/claudecli ./internal/provider/codexapp ./internal/archtest -run 'TestNewRuntimeReporter|TestDependencyContract|ReportRuntime|GenericReporterError|StartSessionFailsWhenRuntimeReporter|ResumeSessionFailsWhenRuntimeReporter|TestFxValidateApp|TestAppModuleGraphIsClosed' -count=1
```

Before accepting this step, list the matching provider tests and fail if any mandatory start/resume name is absent:

```bash
go test ./internal/provider/claudecli ./internal/provider/codexapp -list 'StartSessionFailsWhenRuntimeReporter|ResumeSessionFailsWhenRuntimeReporter'
```

The list must include the deferred-production and generic-error variants for Claude and Codex start and resume. A green `go test -run` with zero matching tests is not evidence.

Expected: PASS.

### Task A4: Replace BindSessionGeneration Silent Success

- [ ] **Step 1: Write failing tests**

Add to `internal/app/thread_orchestration_adapter_test.go`:

```go
func TestBindSessionGenerationReturnsTypedUnsupportedForDesktopExternalMode(t *testing.T) {
	facade := &mcpOrchOrchestrationFacade{
		dependency: contract.DependencyConfig{Profile: contract.DependencyProfileDesktopHost},
	}
	err := facade.BindSessionGeneration(context.Background(), "agent-1", 7)
	if !contract.IsDependencyModeError(err, "thread.bind_session_generation", contract.DependencyProfileDesktopHost, contract.ErrUnsupportedDependencyMode) {
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
```

Create `internal/module/thread/lifecycle_bind_session_generation_test.go`:

```go
func TestThreadLifecycleSkipsOnlyTypedBindSessionGenerationUnsupported(t *testing.T) {
	svc := lifecycleBindServiceForTest(contract.DependencyProfileDesktopHost, contract.NewDependencyModeError(
		contract.ErrUnsupportedDependencyMode,
		"thread.bind_session_generation",
		contract.DependencyProfileDesktopHost,
	))
	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err != nil {
		t.Fatalf("bindSessionGeneration() error = %v", err)
	}
	svc.bindSessionGenerationRecorder.(*bindGenerationStatusRecorder).require(t, "agent-1", contract.DependencyProfileDesktopHost, "unsupported")
}

func TestThreadLifecycleSkipsOnlyTypedBindSessionGenerationUnsupportedOnResumePath(t *testing.T) {
	svc := lifecycleBindServiceForTest(contract.DependencyProfileTest, contract.NewDependencyModeError(
		contract.ErrUnsupportedDependencyMode,
		"thread.bind_session_generation",
		contract.DependencyProfileTest,
	))
	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err != nil {
		t.Fatalf("bindSessionGeneration() error = %v", err)
	}
	svc.bindSessionGenerationRecorder.(*bindGenerationStatusRecorder).require(t, "agent-1", contract.DependencyProfileTest, "unsupported")
}

func TestThreadLifecycleFailsProductionMissingBindSessionGeneration(t *testing.T) {
	svc := lifecycleBindServiceForTest(contract.DependencyProfileProduction, errors.New("thread.bind_session_generation is required in production profile"))
	err := svc.bindSessionGeneration(context.Background(), "agent-1")
	if err == nil {
		t.Fatal("bindSessionGeneration() error = nil, want production bind failure")
	}
}

func TestThreadLifecycleFailsGenericBindSessionGenerationError(t *testing.T) {
	svc := lifecycleBindServiceForTest(contract.DependencyProfileDesktopHost, errors.New("store down"))
	err := svc.bindSessionGeneration(context.Background(), "agent-1")
	if err == nil || !strings.Contains(err.Error(), "store down") {
		t.Fatalf("bindSessionGeneration() error = %v, want store down", err)
	}
}

func TestThreadLifecycleProductionRequiresOrchestrationAndSessionGenerationProvider(t *testing.T) {
	svc := &service{cfg: &contract.Config{Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction}}}
	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err == nil {
		t.Fatal("bindSessionGeneration() error = nil, want missing orchestration/session generation failure")
	}
}

func TestThreadLifecycleProductionRequiresSessionGenerationProvider(t *testing.T) {
	svc := &service{
		cfg:           &contract.Config{Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction}},
		sessions:      bindGenerationSessionProviderWithoutGeneration{},
		orchestration: bindGenerationOrchestration{},
	}
	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err == nil {
		t.Fatal("bindSessionGeneration() error = nil, want missing session generation provider failure")
	}
}

func TestThreadLifecycleFailsEmptyDependencyProfile(t *testing.T) {
	svc := &service{
		cfg:           &contract.Config{Dependency: contract.DependencyConfig{}},
		sessions:      bindGenerationSessionProvider{generation: 7},
		orchestration: bindGenerationOrchestration{},
	}
	err := svc.bindSessionGeneration(context.Background(), "agent-1")
	if err == nil || !strings.Contains(err.Error(), "dependency profile is required") {
		t.Fatalf("bindSessionGeneration() error = %v, want missing dependency profile failure", err)
	}
}

func TestThreadLifecycleDesktopMissingSessionGenerationProviderRecordsTypedUnsupported(t *testing.T) {
	recorder := &bindGenerationStatusRecorder{}
	svc := &service{
		cfg:                           &contract.Config{Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileDesktopHost}},
		sessions:                      bindGenerationSessionProviderWithoutGeneration{},
		orchestration:                 bindGenerationOrchestration{},
		bindSessionGenerationRecorder: recorder,
	}
	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err != nil {
		t.Fatalf("bindSessionGeneration() error = %v, want nil after recorded typed unsupported", err)
	}
	recorder.require(t, "agent-1", contract.DependencyProfileDesktopHost, "unsupported")
}

func lifecycleBindServiceForTest(profile contract.DependencyProfile, bindErr error) *service {
	return &service{
		cfg:                           &contract.Config{Dependency: contract.DependencyConfig{Profile: profile}},
		sessions:                      bindGenerationSessionProvider{generation: 7},
		orchestration:                 bindGenerationOrchestration{err: bindErr},
		bindSessionGenerationRecorder: &bindGenerationStatusRecorder{},
	}
}

type bindGenerationStatusRecorder struct{ records []bindGenerationStatusRecord }

func (r *bindGenerationStatusRecorder) RecordBindSessionGenerationSkipped(_ context.Context, record bindGenerationStatusRecord) error {
	r.records = append(r.records, record)
	return nil
}

func (r *bindGenerationStatusRecorder) require(t *testing.T, agentID string, profile contract.DependencyProfile, status string) {
	t.Helper()
	if len(r.records) != 1 {
		t.Fatalf("records = %v, want one skipped bind record", r.records)
	}
	got := r.records[0]
	if got.AgentID != agentID || got.Dependency != "thread.bind_session_generation" || got.Profile != profile || got.Status != status {
		t.Fatalf("record = %#v, want agent/profile/status dependency record", got)
	}
}

type bindGenerationSessionProvider struct{ generation uint64 }

func (p bindGenerationSessionProvider) GetSession(string) (contract.Session, error) { return nil, nil }
func (p bindGenerationSessionProvider) RemoveSession(string)                        {}
func (p bindGenerationSessionProvider) SessionGeneration(string) uint64              { return p.generation }

type bindGenerationSessionProviderWithoutGeneration struct{}

func (bindGenerationSessionProviderWithoutGeneration) GetSession(string) (contract.Session, error) { return nil, nil }
func (bindGenerationSessionProviderWithoutGeneration) RemoveSession(string)                        {}

type bindGenerationOrchestration struct{ err error }

func (o bindGenerationOrchestration) LaunchAgent(context.Context, LaunchAgentRequest) error { return nil }
func (o bindGenerationOrchestration) StopAgent(context.Context, string) error               { return nil }
func (o bindGenerationOrchestration) Recover(context.Context, string) error                 { return nil }
func (o bindGenerationOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return o.err
}
```

Run:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/module/thread -run 'BindSessionGeneration|Lifecycle.*Unsupported|ProductionRequiresOrchestration|ProductionRequiresSessionGenerationProvider|FailsEmptyDependencyProfile|DesktopMissingSessionGenerationProviderRecordsTypedUnsupported' -count=1
```

Expected: FAIL because `BindSessionGeneration` currently returns `nil`, `internal/module/thread/module.go` still marks orchestration/session-related parameters optional, and `bindSessionGeneration` silently returns nil when orchestration or session generation is absent.

- [ ] **Step 2: Implement typed unsupported**

Change `internal/app/thread_orchestration_adapter.go` so the real Fx path injects dependency profile into the facade:

```go
type mcpOrchOrchestrationFacade struct {
	tools      dagToolCaller
	dependency contract.DependencyConfig
	// existing fields...
}

func newMCPOrchOrchestrationFacade(ref *toolbridgeHandlerRef, dependency contract.DependencyConfig) (*mcpOrchOrchestrationFacade, error) {
	if dependency.Profile == "" {
		return nil, errors.New("app: dependency profile is required for mcp-orch orchestration facade")
	}
	return &mcpOrchOrchestrationFacade{tools: ref, dependency: dependency}, nil
}
```

Update `internal/app/modules.go` so Fx provides `contract.DependencyConfig` to `newMCPOrchOrchestrationFacade`; do not create a local default in the constructor. Add graph tests in `internal/app/modules_graph_test.go` proving the app graph resolves an `mcpOrchOrchestrationFacade` with the configured profile, desktop/test bind returns typed unsupported, production without a binding port fails, and empty profile fails at construction. Name the focused graph test `TestMCPOrchOrchestrationFacadeDependencyConfig`.

Then change `BindSessionGeneration`:

```go
func (f *mcpOrchOrchestrationFacade) BindSessionGeneration(_ context.Context, _ string, _ uint64) error {
	switch f.dependency.Profile {
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileTest:
		return dependencyUnsupported("thread.bind_session_generation", f.dependency.Profile)
	default:
		return newDependencyContract(f.dependency.Profile).Require("thread.bind_session_generation", nil)
	}
}
```

Create `internal/module/thread/bind_session_generation_status.go` and wire it through `internal/module/thread/module.go` as a required Fx dependency for the lifecycle service. The first implementation may be a structured `slog` recorder, but it must be a real production provider, not a test stub and not a noop:

```go
type slogBindSessionGenerationStatusRecorder struct {
	logger *slog.Logger
}

func newBindSessionGenerationStatusRecorder(logger *slog.Logger) (bindSessionGenerationStatusRecorder, error) {
	if logger == nil {
		return nil, errors.New("thread bind-session-generation status logger is required")
	}
	return slogBindSessionGenerationStatusRecorder{logger: logger}, nil
}

func (r slogBindSessionGenerationStatusRecorder) RecordBindSessionGenerationSkipped(_ context.Context, record bindGenerationStatusRecord) error {
	if strings.TrimSpace(record.AgentID) == "" || record.Dependency != "thread.bind_session_generation" || record.Profile == "" || record.Status == "" {
		return errors.New("thread bind-session-generation skipped status record is incomplete")
	}
	r.logger.Warn("thread bind-session-generation skipped",
		"agent_id", record.AgentID,
		"dependency", record.Dependency,
		"profile", record.Profile,
		"status", record.Status,
		"reason", record.Reason,
	)
	return nil
}
```

Add a graph/constructor test that removes this recorder from the thread module and proves construction fails. Add start/resume/fork path tests with the real recorder wired into the service; test-local recorders may still be used for unit tests, but they must not be the only evidence that production lifecycle code records the skipped status.

Change `internal/module/thread/module.go` and the thread lifecycle call site so production profile requires orchestration and a session-generation-capable provider. It must reject an explicitly present empty dependency profile before checking dependency availability, and it skips only the exact desktop/test typed unsupported result after recording a concrete skipped/deferred status:

```go
profile, err := threadDependencyProfile(s.cfg)
if err != nil {
	return err
}
generationProvider, ok := s.sessions.(sessionGenerationProvider)
if s.orchestration == nil || s.sessions == nil || !ok {
	return s.handleBindSessionGenerationError(ctx, agentID, missingBindSessionGenerationDependency(profile), profile)
}

generation := generationProvider.SessionGeneration(agentID)
if generation == 0 {
	return errors.New("session generation is not available")
}
err = s.orchestration.BindSessionGeneration(ctx, strings.TrimSpace(agentID), generation)
return s.handleBindSessionGenerationError(ctx, agentID, err, profile)
```

`missingBindSessionGenerationDependency` must make allowed absence typed and observable instead of silent:

```go
type bindSessionGenerationStatusRecorder interface {
	RecordBindSessionGenerationSkipped(context.Context, bindGenerationStatusRecord) error
}

type bindGenerationStatusRecord struct {
	AgentID    string
	Dependency string
	Profile    contract.DependencyProfile
	Status     string
	Reason     string
}

func missingBindSessionGenerationDependency(profile contract.DependencyProfile) error {
	switch profile {
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileTest:
		return contract.NewDependencyModeError(
			contract.ErrUnsupportedDependencyMode,
			"thread.bind_session_generation",
			profile,
		)
	case contract.DependencyProfileProduction:
		return errors.New("thread.bind_session_generation requires orchestration and session generation provider in production profile")
	default:
		return fmt.Errorf("thread dependency profile %q is not supported", profile)
	}
}

func (s *service) handleBindSessionGenerationError(ctx context.Context, agentID string, err error, profile contract.DependencyProfile) error {
	if err == nil {
		return nil
	}
	if contract.IsDependencyModeError(err, "thread.bind_session_generation", contract.DependencyProfileDesktopHost, contract.ErrUnsupportedDependencyMode) ||
		contract.IsDependencyModeError(err, "thread.bind_session_generation", contract.DependencyProfileTest, contract.ErrUnsupportedDependencyMode) {
		if s.bindSessionGenerationRecorder == nil {
			return errors.New("thread.bind_session_generation skipped status recorder is required")
		}
		if recordErr := s.bindSessionGenerationRecorder.RecordBindSessionGenerationSkipped(ctx, bindGenerationStatusRecord{
			AgentID:    strings.TrimSpace(agentID),
			Dependency: "thread.bind_session_generation",
			Profile:    profile,
			Status:     "unsupported",
			Reason:     err.Error(),
		}); recordErr != nil {
			return recordErr
		}
		return nil
	}
	return err
}
```

`threadDependencyProfile` may default a nil config to production, but it must fail-fast when a config exists and `cfg.Dependency.Profile` is empty:

```go
func threadDependencyProfile(cfg *contract.Config) (contract.DependencyProfile, error) {
	if cfg == nil {
		return contract.DependencyProfileProduction, nil
	}
	if strings.TrimSpace(string(cfg.Dependency.Profile)) == "" {
		return "", errors.New("thread dependency profile is required")
	}
	return cfg.Dependency.Profile, nil
}
```

Do not skip generic errors.

`bindSessionGeneration` itself must never return nil only because `orchestration`, `sessions`, or `SessionGeneration` capability is missing. Desktop/test allowed absence must first become `contract.ErrUnsupportedDependencyMode` and record an observable skipped/deferred status; only the exact typed unsupported result may be converted to nil for lifecycle continuity. Add start/resume/fork path assertions that the status record includes `agent_id`, dependency `thread.bind_session_generation`, dependency profile, and status `unsupported` whenever lifecycle continuity relies on this conversion.

- [ ] **Step 3: Run tests and LSP diagnostics**

Use LSP `file(diagnostics)` for:

- `internal/app/thread_orchestration_adapter.go`
- `internal/app/thread_orchestration_adapter_test.go`
- `internal/module/thread/lifecycle.go`
- `internal/module/thread/bind_session_generation_status.go`
- `internal/module/thread/module.go`
- `internal/module/thread/service_constructor.go`
- `internal/module/thread/lifecycle_bind_session_generation_test.go`

Then run:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/module/thread -run 'BindSessionGeneration|Lifecycle.*Unsupported|ProductionRequiresOrchestration|ProductionRequiresSessionGenerationProvider|FailsEmptyDependencyProfile|DesktopMissingSessionGenerationProviderRecordsTypedUnsupported|MCPOrchOrchestrationFacadeDependencyConfig' -count=1
```

Expected: PASS.

### Task A5: Add Toolbridge Dependency Contract

- [ ] **Step 1: Write failing toolbridge graph tests**

Add production-profile cases to `internal/app/modules_graph_test.go` and focused constructor cases under `internal/platform/toolbridge`:

```go
func TestToolbridgeProductionProfileRequiresCriticalDependencies(t *testing.T) {
	for _, tc := range []struct {
		name       string
		omit       string
		wantErr    string
	}{
		{name: "dispatcher", omit: "toolbridge.dispatcher", wantErr: "toolbridge.dispatcher"},
		{name: "resolver", omit: "toolbridge.workdir_resolver", wantErr: "toolbridge.workdir_resolver"},
		{name: "preferences", omit: "toolbridge.preferences", wantErr: "toolbridge.preferences"},
		{name: "config", omit: "toolbridge.config", wantErr: "toolbridge.config"},
		{name: "lifecycle_backfiller", omit: "toolbridge.lifecycle_backfiller", wantErr: "toolbridge.lifecycle_backfiller"},
		{name: "lifecycle_policy_reader", omit: "toolbridge.lifecycle_policy_reader", wantErr: "toolbridge.lifecycle_policy_reader"},
		{name: "agent_thread_lookup", omit: "toolbridge.agent_thread_lookup", wantErr: "toolbridge.agent_thread_lookup"},
		{name: "thread_config_override_store", omit: "toolbridge.thread_config_override_store", wantErr: "toolbridge.thread_config_override_store"},
		{name: "host_tools", omit: "toolbridge.host_tools", wantErr: "toolbridge.host_tools"},
		{name: "skill_tools", omit: "toolbridge.skill_tools", wantErr: "toolbridge.skill_tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolbridgeDependencies(toolbridgeDependencyFixture{
				profile: contract.DependencyProfileProduction,
				omit:    tc.omit,
			}.handlerIn())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateToolbridgeDependencies() error = %v, want %s", err, tc.wantErr)
			}
		})
	}
}

func TestToolbridgeDesktopProfileAllowsOnlyNamedMissingDependencies(t *testing.T) {
	allowed := map[string]bool{
		"toolbridge.agent_thread_lookup":          true,
		"toolbridge.thread_config_override_store": true,
	}
	for _, dependency := range allToolbridgeDependencyNamesForTest() {
		err := validateToolbridgeDependencies(toolbridgeDependencyFixture{
			profile: contract.DependencyProfileDesktopHost,
			omit:    dependency,
		}.handlerIn())
		if allowed[dependency] {
			if !contract.IsDependencyModeError(err, dependency, contract.DependencyProfileDesktopHost, contract.ErrUnsupportedDependencyMode) {
				t.Fatalf("%s error = %v, want desktop typed unsupported", dependency, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), dependency) {
			t.Fatalf("%s error = %v, want required dependency failure", dependency, err)
		}
	}
}

func TestToolbridgeTestProfileAllowsOnlyTestNamedMissingDependencies(t *testing.T) {
	allowed := map[string]bool{
		"toolbridge.lifecycle_backfiller": true,
		"toolbridge.skill_tools":          true,
	}
	for _, dependency := range allToolbridgeDependencyNamesForTest() {
		err := validateToolbridgeDependencies(toolbridgeDependencyFixture{
			profile: contract.DependencyProfileTest,
			omit:    dependency,
		}.handlerIn())
		if allowed[dependency] {
			if !contract.IsDependencyModeError(err, dependency, contract.DependencyProfileTest, contract.ErrUnsupportedDependencyMode) {
				t.Fatalf("%s error = %v, want test typed unsupported", dependency, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), dependency) {
			t.Fatalf("%s error = %v, want required dependency failure", dependency, err)
		}
	}
}
```

Start with this explicit matrix:

| Dependency | Production | Desktop host | Test | Reason |
|---|---|---|---|---|
| `toolbridge.dispatcher` | required | required | required | Diff/tool events must not disappear silently. |
| `toolbridge.workdir_resolver` | required | required | required | Missing resolver silently skips diff snapshots in `diff_gen.go`. |
| `toolbridge.preferences` | required | required | required | Missing preferences falls back to default launch config. |
| `toolbridge.config` | required | required | required | Missing config changes persistent-subagent fallback behavior. |
| `toolbridge.lifecycle_backfiller` | required | required | typed unsupported allowed | MCP tool lifecycle discovery is production state. |
| `toolbridge.lifecycle_policy_reader` | required | required | required | Tool lifecycle policy is a production gate. |
| `toolbridge.agent_thread_lookup` | required | typed unsupported allowed | required | Desktop external orchestration may own binding; production/test must prove behavior. |
| `toolbridge.thread_config_override_store` | required | typed unsupported allowed | required | Desktop external orchestration may own config; production/test must prove behavior. |
| `toolbridge.host_tools` | required | required | required | Empty host tool registry is a real missing capability. |
| `toolbridge.skill_tools` | required | required | typed unsupported allowed | Skill tool exposure is a provider/runtime contract. |

Run:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/platform/toolbridge -run 'Toolbridge.*Dependenc|ProductionProfileRequiresCriticalDependencies' -count=1
```

Expected: FAIL because `internal/platform/toolbridge/module.go` currently declares these dependencies `optional:"true"` and `NewHandler` accepts nil values.

- [ ] **Step 2: Implement toolbridge dependency contract**

Modify:

- `internal/platform/toolbridge/module.go`
- `internal/platform/toolbridge/handler.go`
- `internal/platform/toolbridge/diff_gen.go`
- `internal/platform/toolbridge/handler_managed_launch.go`
- `internal/app/toolbridge_adapters.go`
- `internal/app/modules_graph_test.go`

Rules:

- inject `contract.DependencyConfig` into the toolbridge module,
- convert `NewHandler` or the module provider to return `(*Handler, error)`,
- validate every matrix entry before constructing `Handler`,
- cover the current nil branches in `diff_gen.go`, `handler_managed_launch.go`, and `handler.go` with tests that execute constructors and the relevant methods,
- remove test-only silent empty results such as adapter methods that return empty binding/config when stores are nil,
- for allowed absence, return `contract.NewDependencyModeError(...)` with the exact dependency name and profile; never allow a test-only absence in `desktop_host`,
- production profile must never convert a missing dependency into an empty registry, empty binding, nil dispatcher, or no-op store.

- [ ] **Step 3: Run toolbridge LSP diagnostics and tests**

Use LSP `file(diagnostics)` for every modified file in `internal/platform/toolbridge/**`, `internal/app/toolbridge_adapters.go`, and `internal/app/modules_graph_test.go`, then run:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/platform/toolbridge ./internal/archtest -run 'Toolbridge.*Dependenc|TestToolbridgeCodexProductionBindingRequiresCriticalDependencies|TestFxValidateApp|TestAppModuleGraphIsClosed' -count=1
```

Expected: PASS.

## Lane B: Frontend Feature-Owned Surfaces

**Goal:** Make frontend changes safer for AI workers by forcing page features through local services, adapters, and contract tests. Pages should render and orchestrate user interactions; backend DTO shaping belongs in feature services or shared API facades.

**Files:**
- Modify: `frontend-app/src/pages/backendApiConsumer.surface.test.js`
- Create: `frontend-app/src/pages/pageSurfaceManifest.js`
- Create: `frontend-app/src/pages/pageSurfaceManifest.test.js`
- Create: `frontend-app/src/pages/files/services/filesPageService.js`
- Create: `frontend-app/src/pages/files/services/filesPageService.test.js`
- Create: `frontend-app/src/pages/memory/services/memoryPageService.js`
- Create: `frontend-app/src/pages/memory/services/memoryPageService.test.js`
- Create: `frontend-app/src/pages/observability/services/observabilityPageService.js`
- Create: `frontend-app/src/pages/observability/services/observabilityPageService.test.js`
- Create: `frontend-app/src/pages/prompts/services/promptPageService.js`
- Create: `frontend-app/src/pages/prompts/services/promptPageService.test.js`
- Modify: `frontend-app/src/pages/files/FilesPage.jsx`
- Modify: `frontend-app/src/pages/memory/MemoryPage.jsx`
- Modify: `frontend-app/src/pages/observability/ObservabilityPage.jsx`
- Modify: `frontend-app/src/pages/prompts/PromptPage.jsx`
- Modify: `frontend-app/src/features/prompts/PromptPageView.jsx`
- Modify: `frontend-app/src/pages/shared/pageShared.js`
- Modify: `frontend-app/src/App.jsx`
- Modify: `frontend-app/package.json`
- Modify: `frontend-app/package-lock.json`
- Create: `frontend-app/src/pages/importSurfaceGuard.test-helper.js`
- Modify: `frontend-app/src/services/modules/fileService.js`
- Create/modify: `frontend-app/src/services/modules/fileService.test.js`
- Modify: `frontend-app/src/adapters/fileAdapter.js`
- Create/modify: `frontend-app/src/adapters/fileAdapter.test.js`
- Create: `frontend-app/src/pages/shared/sharedSurfaceBoundary.test.js`
- Modify: `frontend-app/src/pages/shared/pageComponents.test.jsx`
- Test: `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`

### Task B1: Replace Static Backend-Consumer Allowlist With Feature Boundary Manifest

- [ ] **Step 1: Write failing manifest test**

Create `frontend-app/src/pages/pageSurfaceManifest.js`:

```js
export const pageSurfaceManifest = {
  chat: {
    entry: 'pages/chat/ChatPage.jsx',
    servicePrefix: 'pages/chat/services/',
    adapterPrefix: 'pages/chat/adapters/',
  },
  files: {
    entry: 'pages/files/FilesPage.jsx',
    servicePrefix: 'pages/files/services/',
    adapterPrefix: 'pages/files/adapters/',
  },
  memory: {
    entry: 'pages/memory/MemoryPage.jsx',
    servicePrefix: 'pages/memory/services/',
    adapterPrefix: 'pages/memory/adapters/',
  },
  observability: {
    entry: 'pages/observability/ObservabilityPage.jsx',
    servicePrefix: 'pages/observability/services/',
    adapterPrefix: 'pages/observability/adapters/',
  },
  prompts: {
    entry: 'pages/prompts/PromptPage.jsx',
    servicePrefix: 'pages/prompts/services/',
    adapterPrefix: 'pages/prompts/adapters/',
  },
  settings: {
    entry: 'pages/settings/SettingsPage.jsx',
    servicePrefix: 'pages/settings/services/',
    adapterPrefix: 'pages/settings/adapters/',
  },
  skills: {
    entry: 'pages/skills/SkillsPage.jsx',
    servicePrefix: 'pages/skills/services/',
    adapterPrefix: 'pages/skills/adapters/',
  },
  workflows: {
    entry: 'pages/workflows/WorkflowPage.jsx',
    servicePrefix: 'pages/workflows/services/',
    adapterPrefix: 'pages/workflows/adapters/',
  },
};
```

Create `frontend-app/src/pages/importSurfaceGuard.test-helper.js` and import it from both `frontend-app/src/pages/pageSurfaceManifest.test.js` and `frontend-app/src/pages/backendApiConsumer.surface.test.js`. The helper must be the only import-specifier collector used by page-surface boundary tests; do not keep a second regex-only implementation in the recursive backend consumer guard.

```js
import { parse } from '@babel/parser';

export const NON_LITERAL_DYNAMIC_IMPORT = '__non_literal_dynamic_import__';
export const COMPUTED_VITEST_MODULE_MOCK = '__computed_vitest_module_mock__';

function literalString(node) {
  if (!node) return '';
  if (node.type === 'StringLiteral') return node.value;
  if (node.type === 'Literal' && typeof node.value === 'string') return node.value;
  return '';
}

function memberPropertyName(node) {
  if (!node) return '';
  if (!node.computed && node.property?.type === 'Identifier') return node.property.name;
  return literalString(node.property);
}

function vitestMockMember(node) {
  if (
    node?.type !== 'MemberExpression' ||
    node.object?.type !== 'Identifier' ||
    node.object.name !== 'vi'
  ) {
    return { known: false, unknownComputed: false };
  }
  const name = memberPropertyName(node);
  return {
    known: ['mock', 'doMock', 'unstable_mockModule'].includes(name),
    unknownComputed: node.computed && !name,
  };
}

function unwrapVitestCallApply(node) {
  if (
    node?.type === 'MemberExpression' &&
    ['call', 'apply'].includes(memberPropertyName(node)) &&
    node.object?.type === 'MemberExpression'
  ) {
    return node.object;
  }
  return null;
}

function reflectApplyTarget(node) {
  if (
    node?.type === 'CallExpression' &&
    node.callee?.type === 'MemberExpression' &&
    node.callee.object?.type === 'Identifier' &&
    node.callee.object.name === 'Reflect' &&
    memberPropertyName(node.callee) === 'apply'
  ) {
    return node.arguments?.[0];
  }
  return null;
}

function callApplySpecifier(callee, args) {
  if (
    callee?.type !== 'MemberExpression' ||
    !['call', 'apply'].includes(memberPropertyName(callee))
  ) {
    return '';
  }
  if (memberPropertyName(callee) === 'call') return literalString(args?.[1]);
  const applyArgs = args?.[1];
  if (applyArgs?.type !== 'ArrayExpression') return COMPUTED_VITEST_MODULE_MOCK;
  return literalString(applyArgs.elements?.[0]) || COMPUTED_VITEST_MODULE_MOCK;
}

function collectImportSpecifiers(node, specifiers = []) {
  if (!node || typeof node !== 'object') return specifiers;
  switch (node.type) {
    case 'ImportDeclaration':
    case 'ExportNamedDeclaration':
    case 'ExportAllDeclaration':
      if (literalString(node.source)) specifiers.push(literalString(node.source));
      break;
    case 'ImportExpression':
      specifiers.push(literalString(node.source) || NON_LITERAL_DYNAMIC_IMPORT);
      break;
    case 'CallExpression': {
      const callee = node.callee;
      const isRequire = callee?.type === 'Identifier' && callee.name === 'require';
      const directVitest = vitestMockMember(callee);
      const callApplyTarget = unwrapVitestCallApply(callee);
      const callApplyVitest = vitestMockMember(callApplyTarget);
      const reflectTarget = reflectApplyTarget(node);
      const reflectVitest = vitestMockMember(reflectTarget);
      const directArg = literalString(node.arguments?.[0]);
      const callApplyArg = callApplySpecifier(callee, node.arguments);
      const reflectApplyArg = callApplySpecifier({ type: 'MemberExpression', property: { type: 'Identifier', name: 'apply' } }, [null, node.arguments?.[2]]);
      if (isRequire && directArg) {
        specifiers.push(directArg);
      } else if (directVitest.known && directArg) {
        specifiers.push(directArg);
      } else if (callApplyVitest.known && callApplyArg && callApplyArg !== COMPUTED_VITEST_MODULE_MOCK) {
        specifiers.push(callApplyArg);
      } else if (reflectVitest.known && reflectApplyArg && reflectApplyArg !== COMPUTED_VITEST_MODULE_MOCK) {
        specifiers.push(reflectApplyArg);
      } else if (
        directVitest.unknownComputed ||
        callApplyVitest.unknownComputed ||
        reflectVitest.unknownComputed ||
        (callApplyVitest.known && callApplyArg === COMPUTED_VITEST_MODULE_MOCK) ||
        (reflectVitest.known && reflectApplyArg === COMPUTED_VITEST_MODULE_MOCK)
      ) {
        specifiers.push(COMPUTED_VITEST_MODULE_MOCK);
      }
      break;
    }
  }
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) {
      for (const child of value) collectImportSpecifiers(child, specifiers);
    } else if (value && typeof value === 'object' && typeof value.type === 'string') {
      collectImportSpecifiers(value, specifiers);
    }
  }
  return specifiers;
}

export function importSpecifiers(source) {
  const ast = parse(source, {
    sourceType: 'module',
    createImportExpressions: true,
    plugins: ['jsx', 'typescript', 'dynamicImport', 'importAttributes'],
  });
  return collectImportSpecifiers(ast);
}
```

Create `frontend-app/src/pages/pageSurfaceManifest.test.js`:

```js
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { COMPUTED_VITEST_MODULE_MOCK, importSpecifiers, NON_LITERAL_DYNAMIC_IMPORT } from './importSurfaceGuard.test-helper.js';
import { pageSurfaceManifest } from './pageSurfaceManifest.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function read(relPath) {
  return fs.readFileSync(path.join(sourceRoot, relPath), 'utf8');
}

function resolveEntryImport(entry, specifier) {
  if (!specifier.startsWith('.')) return '';
  const resolved = path.normalize(path.join(path.dirname(entry), specifier));
  return resolved.split(path.sep).join('/');
}

describe('page surface manifest', () => {
  it('declares a feature service boundary for every page entry', () => {
    const missing = [];
    for (const [feature, surface] of Object.entries(pageSurfaceManifest)) {
      expect(surface.servicePrefix).toBe(`pages/${feature}/services/`);
      expect(surface.adapterPrefix).toBe(`pages/${feature}/adapters/`);
      const entrySource = read(surface.entry);
      const imports = importSpecifiers(entrySource).map((specifier) => resolveEntryImport(surface.entry, specifier));
      if (!imports.some((resolved) => resolved.startsWith(surface.servicePrefix))) {
        missing.push(`${feature}:${surface.entry} does not import a service under ${surface.servicePrefix}`);
      }
    }
    expect(missing).toEqual([]);
  });

  it('prevents page entries from importing shared module services directly', () => {
    const violations = [];
    for (const surface of Object.values(pageSurfaceManifest)) {
      const entrySource = read(surface.entry);
      for (const specifier of importSpecifiers(entrySource)) {
        if (specifier.includes('/services/modules/')) {
          violations.push(`${surface.entry} imports ${specifier}`);
        }
      }
    }
    expect(violations).toEqual([]);
  });

  it('detects non-static backend imports before they can bypass the surface guard', () => {
    expect(importSpecifiers(`
      await import('../../shared/api/backendApi.js');
      export { callAPI } from '../../shared/api/backendApi.js';
      export * from '../../services/modules/fileService.js';
      const api = require('../../shared/api/backendApi.js');
      vi.mock('../../shared/api/backendApi.js', () => ({}));
      vi.doMock('../../shared/api/backendApi.js', () => ({}));
      vi.unstable_mockModule('../../shared/api/backendApi.js', () => ({}));
      vi['mock']('../../shared/api/backendApi.js', () => ({}));
      vi['doMock']('../../shared/api/backendApi.js', () => ({}));
	      vi[mockName]('../../shared/api/backendApi.js', () => ({}));
	      vi['doMock'].call(vi, '../../shared/api/backendApi.js', () => ({}));
      vi[mockName].call(vi, '../../shared/api/backendApi.js', () => ({}));
      vi['doMock'].apply(vi, ['../../shared/api/backendApi.js', () => ({})]);
      vi['doMock'].apply(vi, [backendApiPath, () => ({})]);
      vi[mockName].apply(vi, [backendApiPath, () => ({})]);
      Reflect.apply(vi.doMock, vi, ['../../shared/api/backendApi.js', () => ({})]);
      Reflect.apply(vi[mockName], vi, [backendApiPath, () => ({})]);
      handler.apply(thisArg, argsArray);
      await import('../../shared/api/' + 'backendApi.js');
    `)).toEqual([
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../services/modules/fileService.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
	      COMPUTED_VITEST_MODULE_MOCK,
      '../../shared/api/backendApi.js',
      COMPUTED_VITEST_MODULE_MOCK,
      '../../shared/api/backendApi.js',
      COMPUTED_VITEST_MODULE_MOCK,
      COMPUTED_VITEST_MODULE_MOCK,
      '../../shared/api/backendApi.js',
      COMPUTED_VITEST_MODULE_MOCK,
      NON_LITERAL_DYNAMIC_IMPORT,
	    ]);
  });
});
```

Add `@babel/parser` as an explicit frontend dev dependency if it is not already declared in `frontend-app/package.json`; do not rely on transitive parser copies from ESLint/Vite packages.

Create `frontend-app/src/pages/shared/sharedSurfaceBoundary.test.js` to cover shared entry points outside `pages/**`:

```js
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

function read(relPath) {
  return fs.readFileSync(path.join(sourceRoot, relPath), 'utf8');
}

describe('shared page surface boundary', () => {
  it('keeps App memory badge and pageShared behind page-owned memory services', () => {
    const checked = ['App.jsx', 'pages/shared/pageShared.js'];
    const violations = [];
    for (const relPath of checked) {
      const source = read(relPath);
      if (source.includes('services/modules/memoryService.js')) violations.push(`${relPath} imports memoryService`);
    }
    expect(violations).toEqual([]);
    expect(read('App.jsx')).not.toMatch(/\bfetchMemoryDashboard\b/);
    expect(read('App.jsx')).toMatch(/memory(?:Page|Badge)Service/);
    expect(read('pages/shared/pageShared.js')).toMatch(/memory(?:Page|Badge)Service/);
  });

  it('keeps prompt feature view behind the prompt page service', () => {
    const source = read('features/prompts/PromptPageView.jsx');
    expect(source).not.toContain('shared/api/backendApi.js');
    expect(source).toMatch(/promptPageService/);
  });
});
```

Run:

```bash
cd frontend-app
npx vitest run src/pages/pageSurfaceManifest.test.js src/pages/shared/sharedSurfaceBoundary.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL for pages that still import backend facades directly or have no local service boundary.

- [ ] **Step 2: Update backend consumer guard to use the manifest**

Modify `frontend-app/src/pages/backendApiConsumer.surface.test.js` so `isBackendApiServiceConsumer(relativePath)` accepts any `servicePrefix` declared in `pageSurfaceManifest` and so the recursive scanner imports `importSpecifiers`, `NON_LITERAL_DYNAMIC_IMPORT`, and `COMPUTED_VITEST_MODULE_MOCK` from `frontend-app/src/pages/importSurfaceGuard.test-helper.js`. Remove hardcoded migrated page names once the manifest covers the same entries.

The expected invariant:

```js
function isFeatureServiceConsumer(relativePath) {
  return Object.values(pageSurfaceManifest).some((surface) => relativePath.startsWith(surface.servicePrefix));
}
```

Page entries and page-owned modules under `frontend-app/src/pages/**` must not import raw `callAPI`, raw `callBackend`, or direct `shared/api/backendApi.js` unless they are inside a feature service prefix or the shared API package itself. This includes `ImportDeclaration`, `ExportNamedDeclaration`, `ExportAllDeclaration`, literal `import()`, `require()`, `vi.mock()`, `vi.doMock()`, `vi.unstable_mockModule()`, and computed literal forms such as `vi['mock']()` / `vi['doMock']()`. Any `NON_LITERAL_DYNAMIC_IMPORT` or `COMPUTED_VITEST_MODULE_MOCK` in guarded page/feature surfaces is a violation unless the file is in an explicit non-page test allowlist with a comment naming why dynamic import or computed mocking is safe.

They also must not import `frontend-app/src/services/modules/*Service.js` directly. The only allowed module-service consumers inside `frontend-app/src/pages/**` are paths matching a manifest `servicePrefix`. This prevents a page from adding a local service while still keeping the old direct module import.

`frontend-app/src/features/prompts/PromptPageView.jsx` is part of the prompts feature surface even though it lives under `features/**`, not `pages/**`. Add it to the same guard: it must import `promptPageService` from `frontend-app/src/pages/prompts/services/promptPageService.js` and must not import `frontend-app/src/shared/api/backendApi.js` directly. Prompt tests may mock `promptPageService`; they must not mock raw backend API as the page-facing seam.

Add RED cases directly to `frontend-app/src/pages/backendApiConsumer.surface.test.js` proving the recursive guard fails for `await import('../../shared/api/backendApi.js')`, `await import('../../shared/api/' + 'backendApi.js')`, `export { callAPI } from '../../shared/api/backendApi.js'`, `export * from '../../shared/api/backendApi.js'`, `require('../../shared/api/backendApi.js')`, `vi.mock('../../shared/api/backendApi.js')`, `vi.doMock('../../shared/api/backendApi.js')`, `vi.unstable_mockModule('../../shared/api/backendApi.js')`, `vi['mock']('../../shared/api/backendApi.js')`, `vi['doMock']('../../shared/api/backendApi.js')`, unknown `vi[mockName]`, `vi['doMock'].call(vi, '../../shared/api/backendApi.js')`, unknown `vi[mockName].call(vi, '../../shared/api/backendApi.js')`, `vi['doMock'].apply(vi, ['../../shared/api/backendApi.js'])`, known nonliteral `vi['doMock'].apply(vi, [backendApiPath])`, unknown/nonliteral `vi[mockName].apply(vi, [backendApiPath])`, `Reflect.apply(vi.doMock, vi, ['../../shared/api/backendApi.js'])`, and `Reflect.apply(vi[mockName], vi, [backendApiPath])` in page-owned or prompt feature files. Add a pass case proving ordinary non-Vitest `.apply`, such as `handler.apply(thisArg, argsArray)`, does not emit `COMPUTED_VITEST_MODULE_MOCK`.

Do not make Lane B fail on `frontend-app/src/entities/client/model/useClientStore.js`; that store currently imports backend facades directly and needs a separate store-boundary plan. If `backendApiConsumer.surface.test.js` still scans `entities/**`, preserve the existing entity allowlist or split a new store migration task before tightening that surface.

- [ ] **Step 3: Run guard**

Run:

```bash
cd frontend-app
npx vitest run src/pages/backendApiConsumer.surface.test.js src/pages/pageSurfaceManifest.test.js src/pages/shared/sharedSurfaceBoundary.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL until Files, Memory, Observability, Prompts, and shared memory badge paths have service boundaries and no page entry imports module services directly.

### Task B2: Move Page Backend Calls Behind Feature Services

- [ ] **Step 1: Add Files service tests**

Create `frontend-app/src/pages/files/services/filesPageService.test.js`:

```js
import { describe, expect, it, vi } from 'vitest';
import { createFilesPageService } from './filesPageService.js';

describe('filesPageService', () => {
  it('reads shared file detail through the existing file service module', async () => {
    const api = { readSharedFile: vi.fn().mockResolvedValue({ path: 'notes/a.md', content: 'hello' }) };
    const service = createFilesPageService(api);
    await expect(service.readSharedFile('notes/a.md')).resolves.toEqual({ path: 'notes/a.md', content: 'hello' });
    expect(api.readSharedFile).toHaveBeenCalledWith({ path: 'notes/a.md' }, undefined);
  });
});
```

Extend the same test file with this required matrix. Each row must assert the exact API method invoked, exact payload forwarding, and that fail-fast cases reject before the API method is called:

| Method | Required positive case | Required fail-fast / fallback case |
|---|---|---|
| `listSharedFilesDashboard()` | calls `api.listSharedFilesDashboard()` with no args | malformed non-object dashboard response rejects |
| `readSharedFile(path, fallbackFile)` | `('notes/a.md', { size: 10 })` forwards `[{ path: 'notes/a.md' }, { size: 10 }]` | blank or non-string path throws; malformed backend response throws; omitted `fallbackFile` stays `undefined`, never `{}` |
| `openSharedFile(path)` | `'notes/a.md'` forwards `{ path: 'notes/a.md' }` | blank or non-string path throws before API call |
| `deleteSharedFile(path)` | `'notes/a.md'` forwards `{ path: 'notes/a.md' }` | blank or non-string path throws before API call |
| `saveTextFile(params)` | `{ defaultPath: '/tmp', defaultFilename: 'notes.md', content: 'hello' }` forwards the native-save DTO with normalized `defaultFilename` | missing, blank, or non-string `defaultFilename` throws; present non-string `defaultPath` throws; missing or non-string `content` throws before API call |

Create `frontend-app/src/pages/memory/services/memoryPageService.test.js`.
The test file must cover every row in the table below with an injected API object, assert the exact API method invoked, assert the exact payload or argument list forwarded, and assert each fail-fast case rejects before any API call.

| Method | Required positive case | Required fail-fast case |
|---|---|---|
| `fetchMemoryDashboard(cwd, options)` | `('/repo', { signal })` forwards both args unchanged | blank `cwd` throws before API call |
| `getMemoryEntry(params)` | `{ cwd: '/repo', target: 'team', path: 'memory.md' }` | missing `target` or `path` throws |
| `upsertMemoryEntry(params)` | forwards exact payload | missing `cwd`, `target`, or `path` throws |
| `deleteMemoryEntry(params)` | forwards exact payload | missing `cwd`, `target`, or `path` throws |
| `setMemoryAutoDreamIntent(params)` | forwards exact payload | missing `cwd` throws |
| `mergeMemoryEntries(params)` | forwards exact payload | missing source/target identity throws |
| `ignoreMemorySimilarity(params)` | forwards exact payload | missing similarity identity throws |
| `startConsolidateMemorySimilarities(params)` | forwards exact payload | missing `cwd` throws |
| `getMemoryConsolidationStatus(params)` | `{ cwd: '/repo', jobId: 'job-1' }` | missing `jobId` throws |

Create `frontend-app/src/pages/observability/services/observabilityPageService.test.js`.
The test file must cover every row in the table below with an injected API object, assert the exact API method invoked, assert the exact payload forwarded, and assert each fail-fast or normalization case before the page component consumes it.

| Method | Required positive case | Required fail-fast / normalization case |
|---|---|---|
| `listObservabilityRecent(params)` | `{ limit: 25, status: 'error', traceId: 'trace-1', includeTail: true }` | `limit: 0` throws; blank limit is omitted; positive integer strings normalize; non-numeric strings throw |
| `getObservabilityTrace(params)` | `{ traceId: 'trace-1', limit: 25 }` | blank `traceId` throws before API call |
| `copyTextToClipboard(text)` | `'trace-1'` | blank text throws before API call |

Do not let page tests mock `callAPI` directly.

Add a page-level regression proving `frontend-app/src/pages/observability/ObservabilityPage.jsx` no longer performs page-local numeric parsing with a default `50` fallback. The page must pass the raw `filters.limit` value into `observabilityPageService`, and `observabilityPageService` must reject non-numeric or non-positive limits before any API call. This closes the current page-level fallback in `ObservabilityPage.jsx`, where `queryLimit` is derived in the component and silently defaults to `50`.

Also create `frontend-app/src/pages/prompts/services/promptPageService.test.js`. It must inject the current prompt backend API facade used by `frontend-app/src/features/prompts/PromptPageView.jsx` and assert prompt list/detail/update request shapes. It must include a negative test proving prompt page code cannot import raw backend API from `features/prompts`.

Create direct fallback-removal tests for the shared file module and adapter:

```js
// frontend-app/src/services/modules/fileService.test.js
it('does not synthesize an empty fallback for readSharedFile', async () => {
  vi.resetModules();
  const readSharedFileBackend = vi.fn().mockResolvedValue({ file: { path: 'notes/a.md', content: 'hello' } });
  const adaptSharedFileDetail = vi.fn((response, fallbackFile) => ({ path: response.file.path, fallbackFile }));
  vi.doMock('../../shared/api/backendApi.js', () => ({
    deleteSharedFile: vi.fn(),
    listSharedFiles: vi.fn(),
    openSharedFile: vi.fn(),
    readSharedFile: readSharedFileBackend,
    saveTextFile: vi.fn(),
  }));
  vi.doMock('../../adapters/fileAdapter.js', () => ({
    adaptSharedFileDetail,
    adaptSharedFilesDashboard: vi.fn(),
  }));
  const { readSharedFile } = await import('./fileService.js');
  await readSharedFile({ path: 'notes/a.md' });
  expect(adaptSharedFileDetail).toHaveBeenCalledWith(expect.anything(), undefined);
});
```

`frontend-app/src/services/modules/fileService.test.js` must use `vi.resetModules()` plus `vi.doMock` or an explicitly exported `createFileService` dependency-injection factory; do not reference undefined test-only factory helpers. It must also prove malformed/empty backend detail rejects instead of flowing into `adaptSharedFileDetail(response || {}, fallbackFile)`.

`frontend-app/src/adapters/fileAdapter.test.js` must prove `adaptSharedFileDetail(undefined)`, `adaptSharedFileDetail({})`, `adaptSharedFileDetail({ file: null })`, and a response without a path all throw. It may accept a valid `{ file: {...} }` or current backend shape, but it must not recover missing detail through `{}` or caller fallback.

Run:

```bash
cd frontend-app
npx vitest run src/pages/files/services src/pages/memory/services src/pages/observability/services src/pages/prompts/services src/services/modules/fileService.test.js src/adapters/fileAdapter.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the service files do not exist.

- [ ] **Step 2: Create services**

Create `frontend-app/src/pages/files/services/filesPageService.js`:

```js
import * as fileService from '../../../services/modules/fileService.js';

function normalizeRequiredPath(path) {
  if (typeof path !== 'string') throw new Error('file path is required');
  const normalized = path.trim();
  if (!normalized) throw new Error('file path is required');
  return normalized;
}

function normalizeRequiredFilename(filename) {
  if (typeof filename !== 'string') throw new Error('default filename is required');
  const normalized = filename.trim();
  if (!normalized) throw new Error('default filename is required');
  return normalized;
}

export function createFilesPageService(api = fileService) {
  return {
    listSharedFilesDashboard() {
      return api.listSharedFilesDashboard();
    },
    readSharedFile(path, fallbackFile) {
      const normalized = normalizeRequiredPath(path);
      return api.readSharedFile({ path: normalized }, fallbackFile);
    },
    openSharedFile(path) {
      const normalized = normalizeRequiredPath(path);
      return api.openSharedFile({ path: normalized });
    },
    deleteSharedFile(path) {
      const normalized = normalizeRequiredPath(path);
      return api.deleteSharedFile({ path: normalized });
    },
    saveTextFile(params) {
      if (!params || typeof params !== 'object' || Array.isArray(params)) throw new Error('file save params are required');
      const defaultFilename = normalizeRequiredFilename(params.defaultFilename);
      if ('defaultPath' in params && typeof params.defaultPath !== 'string') throw new Error('default path must be a string');
      if (typeof params.content !== 'string') throw new Error('file content is required');
      return api.saveTextFile({ ...params, defaultFilename, content: params.content });
    },
  };
}

export const filesPageService = createFilesPageService();
```

Create `frontend-app/src/pages/memory/services/memoryPageService.js` as a thin page-owned facade over `frontend-app/src/services/modules/memoryService.js`. It must export `createMemoryPageService(api = memoryService)` and `memoryPageService`, with methods named exactly like the injected module functions: `fetchMemoryDashboard`, `getMemoryEntry`, `upsertMemoryEntry`, `deleteMemoryEntry`, `setMemoryAutoDreamIntent`, `mergeMemoryEntries`, `ignoreMemorySimilarity`, `startConsolidateMemorySimilarities`, and `getMemoryConsolidationStatus`.

Create `frontend-app/src/pages/observability/services/observabilityPageService.js` as a thin page-owned facade over `frontend-app/src/services/modules/observabilityService.js`. It must export `createObservabilityPageService(api = observabilityService)` and `observabilityPageService`, with methods `listObservabilityRecent`, `getObservabilityTrace`, and `copyTextToClipboard`.

Create `frontend-app/src/pages/prompts/services/promptPageService.js` as a page-owned facade over the prompt backend API currently consumed from `frontend-app/src/features/prompts/PromptPageView.jsx`.

The page-owned services validate required user inputs before calling the module service: file read/open/delete methods require `path` and must not default `fallbackFile` to `{}`, file save uses the current native-save DTO (`defaultPath`, `defaultFilename`, `content`) and must not require a fixed `path`, memory dashboard requires `cwd`, memory detail requires `target` and `path`, observability trace requires `traceId`, observability recent throws for `limit <= 0`, omits blank limit, and accepts only positive integer strings, and prompt service methods validate prompt keys/ids before API calls. If a legacy display fallback is still required, pass it explicitly from the page and add a negative test proving missing or malformed backend responses are not silently converted into `{}` by the new page service.

Also modify `frontend-app/src/services/modules/fileService.js` and `frontend-app/src/adapters/fileAdapter.js` so `readSharedFile(params, fallbackFile)` no longer defaults `fallbackFile` to `{}`, `adaptSharedFileDetail(response, fallbackFile)` no longer calls `adaptSharedFile(response || {}, 0, fallbackFile)`, and malformed backend responses fail in the direct module/adapter tests.

- [ ] **Step 3: Update pages**

Modify:

- `frontend-app/src/pages/files/FilesPage.jsx`
- `frontend-app/src/pages/memory/MemoryPage.jsx`
- `frontend-app/src/pages/observability/ObservabilityPage.jsx`
- `frontend-app/src/pages/prompts/PromptPage.jsx`
- `frontend-app/src/features/prompts/PromptPageView.jsx`
- `frontend-app/src/pages/shared/pageShared.js`
- `frontend-app/src/App.jsx`

Pages must import only their local service:

```js
import { filesPageService } from './services/filesPageService.js';
```

They must not import from `../../shared/api/backendApi.js` directly.
They also must not import from `../../services/modules/*Service.js` directly. `pageShared.js` and the `App.jsx` memory badge refresh path must consume `memoryPageService` or a clearly named `pages/memory/services/memoryBadgeService.js`, not `services/modules/memoryService.js`.

- [ ] **Step 4: Run focused frontend tests**

Use LSP `file(diagnostics)` for every modified frontend source and test file in Lane B. If diagnostics for a large frontend file exceed tool budget after narrowing, record the file, tool/action, narrowing attempts, and exact blocker instead of reporting PASS.

Run:

```bash
	cd frontend-app
	npx vitest run \
	  src/shared/api/backendApi.contractMatrix.test.js \
	  src/pages/files/FilesPage.test.jsx \
	  src/pages/files/services/filesPageService.test.js \
  src/pages/memory/MemoryPage.test.jsx \
  src/pages/memory/services/memoryPageService.test.js \
  src/pages/observability/ObservabilityPage.test.jsx \
  src/pages/observability/services/observabilityPageService.test.js \
  src/pages/prompts/services/promptPageService.test.js \
  src/pages/shared/pageShared.test.js \
  src/pages/shared/sharedSurfaceBoundary.test.js \
  src/App.test.jsx \
  src/services/modules/fileService.test.js \
  src/adapters/fileAdapter.test.js \
  src/pages/backendApiConsumer.surface.test.js \
  src/pages/pageSurfaceManifest.test.js \
  --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

### Task B3: Add DTO Golden Tests For Page Services

- [ ] **Step 1: Add golden request cases**

For each new service test, add one request-shape golden case:

```js
it('keeps readSharedFile request shape stable', async () => {
  const calls = [];
  const api = {
    readSharedFile: vi.fn((payload, fallbackFile) => {
      calls.push([payload, fallbackFile]);
      return Promise.resolve({ path: payload.path, content: '' });
    }),
  };
  const service = createFilesPageService(api);
  await service.readSharedFile('src/App.jsx', { size: 10 });
  expect(calls).toEqual([[{ path: 'src/App.jsx' }, { size: 10 }]]);
});
```

Memory golden cases must include `fetchMemoryDashboard('/repo', { signal })`, `getMemoryEntry({ cwd: '/repo', target: 'team', path: 'memory.md' })`, and `getMemoryConsolidationStatus({ cwd: '/repo', jobId: 'job-1' })`.

Observability golden cases must include `listObservabilityRecent({ limit: 25, status: 'error', traceId: 'trace-1', includeTail: true })`, `limit: 0` and non-numeric string fail-fast cases, blank limit omission, positive integer string normalization, `getObservabilityTrace({ traceId: 'trace-1', limit: 25 })`, and `copyTextToClipboard('trace-1')`.

Prompt golden cases must include the list/detail/update request shapes currently issued by `PromptPageView.jsx`, with negative tests for blank prompt id/key. The prompt feature must not import raw backend API after migration.

Also extend `frontend-app/src/shared/api/backendApi.contractMatrix.test.js` so the migrated page-owned service methods remain represented in the backend API facade contract matrix. The test must assert the registry entries for shared files, memory, observability, and prompt routes point at the new page/service or module facade names, include the new page service golden test files in their `tests` field, and keep known raw-literal RPC exceptions explicit in policy fields instead of relying on implicit defaults. Do not list this file as owned unless these assertions are added and included in the focused Vitest command above.

- [ ] **Step 2: Extend shared page primitive test**

Modify `frontend-app/src/pages/shared/pageComponents.test.jsx` to assert `RetryableSyncError` keeps retry failure text in `role="alert"`:

```js
await screen.findByText('重试同步失败：backend offline');
expect(screen.getByRole('alert')).toHaveTextContent('重试同步失败：backend offline');
```

This locks the shared page error surface that feature services use when sync calls fail.

- [ ] **Step 3: Run full frontend verification**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all commands exit 0.

## Lane C: Provider Scaffold And Contract Suite

**Goal:** Make a new provider adapter impossible to merge unless it proves the same lifecycle, prompt, approval, interrupt, resume, toolbridge, and runtime-report behavior as existing providers.

**Files:**
- Create: `internal/provider/contracttest/suite.go`
- Create: `internal/provider/contracttest/suite_test.go`
- Create/modify: `internal/provider/**/testdata/event_snapshots/*.json`
- Create/modify: `internal/provider/**/testdata/prompt_snapshots/*.json`
- Create: `internal/provider/provider_contract_manifest_test.go`
- Create: `internal/provider/unified/provider_contract_test.go`
- Modify: `internal/provider/unified/contract_test.go`
- Create: `internal/provider/codexapp/provider_contract_test.go`
- Create: `internal/provider/claudecli/provider_contract_test.go`
- Create: `internal/provider/_template/README.md`
- Create: `internal/provider/_template/module.go.txt`
- Create: `internal/provider/_template/provider_contract_test.go.txt`
- Create: `internal/provider/provider_template_compile_test.go`
- Modify: `internal/app/modules_graph_test.go`
- Modify: `docs/doc/codemap/09-provider.md`

### Task C1: Add Shared Provider Contract Test Harness

- [ ] **Step 1: Write harness self-test**

Create `internal/provider/contracttest/suite_test.go`:

```go
package contracttest

import (
	"strings"
	"testing"
)

func TestSuiteRejectsEmptyProviderName(t *testing.T) {
	result := ValidateSpec(Spec{})
	if result == nil {
		t.Fatal("ValidateSpec() error = nil, want empty provider name error")
	}
}

func TestSuiteRejectsMissingRequiredCases(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	delete(spec.RequiredCases, CaseRuntimeReport)
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("ValidateSpec() error = nil, want missing required case error")
	}
}

func TestSuiteRejectsRequiredCaseWithoutEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseRuntimeReport] = Case{Name: "runtime report", Run: func(*testing.T, *CaseEvidence) {}}
	if err := RunSpecForTest(t, spec); err == nil {
		t.Fatal("RunSpecForTest() error = nil, want missing evidence error")
	}
}

func TestSuiteRejectsRequiredCaseWithoutRecordedAssertion(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseRuntimeReport] = Case{Name: "runtime report", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		// Intentionally perform work but do not record through an evidence helper.
	}}
	if err := RunSpecForTest(t, spec); err == nil {
		t.Fatal("RunSpecForTest() error = nil, want missing recorded assertion")
	}
}

func TestSuiteRejectsTautologicalRequiredCaseEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseRuntimeReport] = Case{Name: "runtime report", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.AssertEqual(t, EvidenceKey("supplemental.runtime_report_shape"), true, true)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "tautological") {
		t.Fatalf("RunSpecForTest() error = %v, want tautological evidence failure", err)
	}
}

func TestSuiteRejectsGenericReservedEvidenceKey(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseRuntimeReport] = Case{Name: "runtime report", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.AssertNoError(t, EvidenceRuntimeReportPayload, nil)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "typed evidence helper") {
		t.Fatalf("RunSpecForTest() error = %v, want reserved evidence helper failure", err)
	}
}

func TestSuiteRejectsPromptParitySelfEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	fields := PromptParityFields{
		BaseInstructions:      "base",
		DeveloperInstructions: "developer",
		PrefixHash:            "hash-contract",
		Boundary:              "boundary",
		SectionSnapshot:       "section",
	}
	evidence := NewProviderPromptEvidence("capture-1", fields)
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordPromptParity(t, evidence, evidence)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "expected_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want prompt parity self-evidence failure", err)
	}
}

func TestSuiteRejectsPromptParityCopiedExpectedEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	fields := PromptParityFields{
		BaseInstructions:      "base",
		DeveloperInstructions: "developer",
		PrefixHash:            "hash-contract",
		Boundary:              "boundary",
		SectionSnapshot:       "section",
	}
	got := NewProviderPromptEvidence("capture-1", fields)
	want := got
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordPromptParity(t, got, want)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "independent expected_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want copied expected evidence failure", err)
	}
}

func TestSuiteRejectsPromptParityExpectedFieldsCopiedFromProvider(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	fields := PromptParityFields{
		BaseInstructions:      "base",
		DeveloperInstructions: "developer",
		PrefixHash:            "hash-contract",
		Boundary:              "boundary",
		SectionSnapshot:       "section",
	}
	got := NewProviderPromptEvidence("capture-1", fields)
	want := expectedPromptEvidenceForTest("snapshot-1", fields)
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordPromptParity(t, got, want)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "independent expected_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want copied expected fields failure", err)
	}
}

func TestSuiteRejectsPromptParitySharedEvidenceID(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	fields := PromptParityFields{
		BaseInstructions:      "base",
		DeveloperInstructions: "developer",
		PrefixHash:            "hash-contract",
		Boundary:              "boundary",
		SectionSnapshot:       "section",
	}
	got := NewProviderPromptEvidence("same-origin", fields)
	want := NewExpectedPromptEvidence(ExpectedPromptSnapshot{
		snapshotID:          "same-origin",
		fields:              fields,
		loadedFromSnapshot:  true,
	})
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordPromptParity(t, got, want)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "independent expected_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want shared evidence id failure", err)
	}
}

func TestSuiteRejectsPromptParityDirectFieldEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.AssertEqual(t, EvidencePromptBaseInstructions, "base", "base")
		e.AssertEqual(t, EvidencePromptDeveloperInstructions, "developer", "developer")
		e.AssertEqual(t, EvidencePromptPrefixHash, "hash-contract", "hash-contract")
		e.AssertEqual(t, EvidencePromptBoundary, "boundary", "boundary")
		e.AssertEqual(t, EvidencePromptSectionSnapshot, "section", "section")
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "typed evidence helper") {
		t.Fatalf("RunSpecForTest() error = %v, want direct prompt evidence failure", err)
	}
}

func TestSuiteRejectsResumeWithoutIdentityEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	delete(spec.RequiredCases, CaseResume)
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("ValidateSpec() error = nil, want missing resume identity case")
	}
	spec = CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseResume] = Case{Name: "resume identity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		// Simulate a provider that can resume but never proves ProviderThreadID was used.
	}}
	if err := RunSpecForTest(t, spec); err == nil {
		t.Fatal("RunSpecForTest() error = nil, want missing resume identity evidence")
	}
}

func TestSuiteRunsCompleteProvider(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	Run(t, spec)
}
```

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/contracttest -count=1
```

Expected: FAIL because `contracttest` does not exist.

The contracttest self-test file must also include prompt snapshot loader negatives:

| Test | Setup | Expected |
|---|---|---|
| `TestLoadExpectedPromptSnapshotRejectsMissingSnapshot` | no `testdata/prompt_snapshots/<id>.json` file | fail with missing snapshot |
| `TestLoadExpectedPromptSnapshotRejectsEmptySnapshot` | tracked empty snapshot fixture | fail with empty snapshot |
| `TestLoadExpectedPromptSnapshotRejectsUntrackedSnapshot` | create a snapshot file during the test that is not returned by `git ls-files --error-unmatch` | fail with tracked golden data error |
| `TestLoadExpectedPromptSnapshotRejectsDirtyTrackedSnapshot` | modify a tracked or newly indexed snapshot fixture during the test before loading it | fail with unstaged working-tree changes |
| `TestLoadExpectedPromptSnapshotRejectsGeneratedMarker` | tracked fixture containing `GENERATED_DURING_TEST` | fail with generated snapshot error |

The same self-test file must include event and outcome evidence negatives:

| Test | Setup | Expected |
|---|---|---|
| `TestRecordEventTranslationRejectsCopiedExpectedEvent` | pass provider-captured event evidence as both observed and expected, or build expected evidence without `LoadExpectedEventSnapshot` | fail with independent snapshot error |
| `TestRecordEventTranslationRejectsSameCaptureAndSnapshotID` | use the same id for captured event and expected event snapshot | fail with distinct id error |
| `TestRecordEventTranslationRejectsCopiedSnapshotLiteral` | build observed event evidence from copied snapshot JSON or an equivalent literal without invoking the provider translator | fail because observed evidence must come from the contracttest translator capture API |
| `TestRecordEventTranslationRejectsMissingGoldenEvent` | no `testdata/event_snapshots/<id>.json` file | fail with missing snapshot |
| `TestLoadExpectedEventSnapshotExposesNoDecoderCallback` | compile the old `LoadExpectedEventSnapshot(t, id, decoder)` pattern in an external provider-style package | fail because expected event decoding is contract-owned and has no caller-supplied decoder |
| `TestLoadExpectedEventSnapshotRejectsDirtyTrackedSnapshot` | modify a tracked or newly indexed event snapshot fixture during the test before loading it | fail with unstaged working-tree changes |
| `TestRecordOutcomeRejectsFreeFormApproval` | record approval with only a nonblank final state, or blank `ObservedActionID` plus nil `Unsupported` | fail with typed outcome evidence error |
| `TestRecordOutcomeRejectsBooleanUnsupported` | attempt to satisfy an outcome using only a boolean unsupported marker | fail because unsupported outcomes require a typed dependency-mode error |
| `TestRecordOutcomeRejectsSyntheticUnsupportedWithoutObservedProviderResult` | construct `contract.NewDependencyModeError(...)` inline in a contract case instead of capturing the provider operation's returned error | fail with synthetic unsupported evidence error |
| `TestRecordOutcomeRejectsWrongUnsupportedDependencyForCase` | capture an unsupported error for one dependency but record it under a different outcome's expected dependency name | fail with key-specific dependency error |
| `TestRecordOutcomeRejectsToolbridgeWithoutDependencyProfile` | record toolbridge outcome without dependency name or profile | fail with dependency/profile evidence error |
| `TestRecordOutcomeRejectsCustomAsSyntheticUnsupported` | provider operation returns a custom error with `As(any) bool` that pretends to be `contract.DependencyModeError` without a real unwrap chain | fail with concrete dependency-mode error requirement |

- [ ] **Step 2: Implement harness**

Create `internal/provider/contracttest/suite.go` with:

```go
package contracttest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type Spec struct {
	Name          string
	Start         func(context.Context, dto.StartSessionRequest) (contract.Session, error)
	Resume        func(context.Context, dto.ResumeSessionRequest) (contract.Session, error)
	EventCases    []Case
	RequiredCases map[CaseKey]Case
}

type CaseKey string

const (
	CasePromptParity  CaseKey = "prompt_parity"
	CaseApproval      CaseKey = "approval"
	CaseInterrupt     CaseKey = "interrupt"
	CaseForceComplete CaseKey = "force_complete"
	CaseResume        CaseKey = "resume"
	CaseToolbridge    CaseKey = "toolbridge"
	CaseRuntimeReport CaseKey = "runtime_report"
)

type Case struct {
	Name string
	Run  func(*testing.T, *CaseEvidence)
}

type EvidenceKey string

const (
	EvidenceEventTranslated             EvidenceKey = "event.translated"
	EvidencePromptBaseInstructions      EvidenceKey = "prompt.base_instructions"
	EvidencePromptDeveloperInstructions EvidenceKey = "prompt.developer_instructions"
	EvidencePromptPrefixHash            EvidenceKey = "prompt.prefix_hash"
	EvidencePromptBoundary              EvidenceKey = "prompt.boundary"
	EvidencePromptSectionSnapshot       EvidenceKey = "prompt.section_snapshot"
	EvidenceApprovalOutcome             EvidenceKey = "approval.outcome"
	EvidenceInterruptOutcome            EvidenceKey = "interrupt.outcome"
	EvidenceForceCompleteOutcome        EvidenceKey = "force_complete.outcome"
	EvidenceResumeIdentity              EvidenceKey = "resume.identity"
	EvidenceToolbridgeDependency        EvidenceKey = "toolbridge.dependency"
	EvidenceRuntimeReportPayload        EvidenceKey = "runtime_report.payload"
)

var requiredEvidenceByCase = map[CaseKey][]EvidenceKey{
	CasePromptParity: {
		EvidencePromptBaseInstructions,
		EvidencePromptDeveloperInstructions,
		EvidencePromptPrefixHash,
		EvidencePromptBoundary,
		EvidencePromptSectionSnapshot,
	},
	CaseApproval:      {EvidenceApprovalOutcome},
	CaseInterrupt:     {EvidenceInterruptOutcome},
	CaseForceComplete: {EvidenceForceCompleteOutcome},
	CaseResume:        {EvidenceResumeIdentity},
	CaseToolbridge:    {EvidenceToolbridgeDependency},
	CaseRuntimeReport: {EvidenceRuntimeReportPayload},
}

var reservedEvidenceKeys = map[EvidenceKey]bool{
	EvidenceEventTranslated:             true,
	EvidencePromptBaseInstructions:      true,
	EvidencePromptDeveloperInstructions: true,
	EvidencePromptPrefixHash:            true,
	EvidencePromptBoundary:              true,
	EvidencePromptSectionSnapshot:       true,
	EvidenceApprovalOutcome:             true,
	EvidenceInterruptOutcome:            true,
	EvidenceForceCompleteOutcome:        true,
	EvidenceResumeIdentity:              true,
	EvidenceToolbridgeDependency:        true,
	EvidenceRuntimeReportPayload:        true,
}

type promptEvidenceOrigin string

const (
	promptOriginProviderRequest   promptEvidenceOrigin = "provider_request"
	promptOriginExpectedSnapshot  promptEvidenceOrigin = "expected_snapshot"
)

type PromptParityFields struct {
	BaseInstructions      string
	DeveloperInstructions string
	PrefixHash            string
	Boundary              string
	SectionSnapshot       string
}

type PromptParityEvidence struct {
	origin             promptEvidenceOrigin
	evidenceID         string
	fields             PromptParityFields
	loadedFromSnapshot bool
}

type eventEvidenceOrigin string

const (
	eventOriginProviderTranslator eventEvidenceOrigin = "provider_translator"
	eventOriginExpectedSnapshot   eventEvidenceOrigin = "expected_event_snapshot"
)

type EventTranslationEvidence struct {
	origin             eventEvidenceOrigin
	evidenceID         string
	canonicalJSON      []byte
	loadedFromSnapshot bool
}

type ExpectedPromptSnapshot struct {
	snapshotID         string
	fields             PromptParityFields
	loadedFromSnapshot bool
}

type ExpectedEventSnapshot struct {
	snapshotID         string
	canonicalJSON      []byte
	loadedFromSnapshot bool
}

func NewProviderPromptEvidence(captureID string, fields PromptParityFields) PromptParityEvidence {
	return PromptParityEvidence{origin: promptOriginProviderRequest, evidenceID: strings.TrimSpace(captureID), fields: fields}
}

func CaptureProviderEventTranslation(t testing.TB, captureID string, raw dto.RawProviderEvent, translate func(dto.RawProviderEvent, func(any))) EventTranslationEvidence {
	t.Helper()
	if translate == nil {
		t.Fatal("event translator capture requires a translator function")
	}
	var captured []any
	translate(raw, func(event any) {
		captured = append(captured, event)
	})
	if len(captured) != 1 {
		t.Fatalf("event translator capture produced %d events, want exactly 1", len(captured))
	}
	return newProviderEventEvidence(t, captureID, captured[0])
}

func newProviderEventEvidence(t testing.TB, captureID string, event any) EventTranslationEvidence {
	t.Helper()
	return EventTranslationEvidence{
		origin:        eventOriginProviderTranslator,
		evidenceID:    strings.TrimSpace(captureID),
		canonicalJSON: canonicalEventJSON(t, event),
	}
}

func LoadExpectedPromptSnapshot(t testing.TB, snapshotID string) ExpectedPromptSnapshot {
	t.Helper()
	fields := loadExpectedPromptSnapshotFields(t, snapshotID)
	return ExpectedPromptSnapshot{snapshotID: strings.TrimSpace(snapshotID), fields: fields, loadedFromSnapshot: true}
}

func LoadExpectedEventSnapshot(t testing.TB, snapshotID string) ExpectedEventSnapshot {
	t.Helper()
	canonical := loadExpectedEventSnapshot(t, snapshotID)
	return ExpectedEventSnapshot{snapshotID: strings.TrimSpace(snapshotID), canonicalJSON: canonical, loadedFromSnapshot: true}
}

func NewExpectedEventEvidence(snapshot ExpectedEventSnapshot) EventTranslationEvidence {
	return EventTranslationEvidence{
		origin:             eventOriginExpectedSnapshot,
		evidenceID:         strings.TrimSpace(snapshot.snapshotID),
		canonicalJSON:      snapshot.canonicalJSON,
		loadedFromSnapshot: snapshot.loadedFromSnapshot,
	}
}

func loadExpectedEventSnapshot(t testing.TB, snapshotID string) []byte {
	t.Helper()
	clean := filepath.Clean(strings.TrimSpace(snapshotID))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		t.Fatalf("event snapshot id %q is invalid", snapshotID)
	}
	path := filepath.Join("testdata", "event_snapshots", clean+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("event snapshot %s is required: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("event snapshot %s is empty", path)
	}
	repoPathRaw, err := exec.Command("git", "ls-files", "--cached", "--full-name", "--error-unmatch", path).Output()
	if err != nil {
		t.Fatalf("event snapshot %s must be tracked golden data", path)
	}
	repoPath := strings.TrimSpace(string(repoPathRaw))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read event snapshot %s: %v", path, err)
	}
	if strings.Contains(string(raw), "GENERATED_DURING_TEST") {
		t.Fatalf("event snapshot %s must be checked-in golden data, not generated during the test", path)
	}
	indexRaw, err := exec.Command("git", "show", ":"+filepath.ToSlash(repoPath)).Output()
	if err != nil {
		t.Fatalf("event snapshot %s must exist in the git index: %v", path, err)
	}
	if !bytes.Equal(raw, indexRaw) {
		t.Fatalf("event snapshot %s has unstaged working-tree changes", path)
	}
	return canonicalEventJSONBytes(t, raw)
}

func canonicalEventJSON(t testing.TB, event any) []byte {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal provider event evidence: %v", err)
	}
	return canonicalEventJSONBytes(t, raw)
}

func canonicalEventJSONBytes(t testing.TB, raw []byte) []byte {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode event evidence JSON: %v", err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("canonicalize event evidence JSON: %v", err)
	}
	if len(canonical) == 0 || bytes.Equal(canonical, []byte("null")) {
		t.Fatal("event evidence must not be empty or null")
	}
	return canonical
}

func loadExpectedPromptSnapshotFields(t testing.TB, snapshotID string) PromptParityFields {
	t.Helper()
	clean := filepath.Clean(strings.TrimSpace(snapshotID))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		t.Fatalf("prompt snapshot id %q is invalid", snapshotID)
	}
	path := filepath.Join("testdata", "prompt_snapshots", clean+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("prompt snapshot %s is required: %v", path, err)
	}
	repoPathRaw, err := exec.Command("git", "ls-files", "--cached", "--full-name", "--error-unmatch", path).Output()
	if err != nil {
		t.Fatalf("prompt snapshot %s must be tracked golden data", path)
	}
	repoPath := strings.TrimSpace(string(repoPathRaw))
	if info.Size() == 0 {
		t.Fatalf("prompt snapshot %s is empty", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prompt snapshot %s: %v", path, err)
	}
	if strings.Contains(string(raw), "GENERATED_DURING_TEST") {
		t.Fatalf("prompt snapshot %s must be checked-in golden data, not generated during the test", path)
	}
	indexRaw, err := exec.Command("git", "show", ":"+filepath.ToSlash(repoPath)).Output()
	if err != nil {
		t.Fatalf("prompt snapshot %s must exist in the git index: %v", path, err)
	}
	if !bytes.Equal(raw, indexRaw) {
		t.Fatalf("prompt snapshot %s has unstaged working-tree changes", path)
	}
	var fields PromptParityFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode prompt snapshot %s: %v", path, err)
	}
	if fields.BaseInstructions == "" || fields.DeveloperInstructions == "" || fields.PrefixHash == "" || fields.Boundary == "" || fields.SectionSnapshot == "" {
		t.Fatalf("prompt snapshot %s is missing required fields", path)
	}
	return fields
}

func NewExpectedPromptEvidence(snapshot ExpectedPromptSnapshot) PromptParityEvidence {
	return PromptParityEvidence{
		origin:             promptOriginExpectedSnapshot,
		evidenceID:         strings.TrimSpace(snapshot.snapshotID),
		fields:             snapshot.fields,
		loadedFromSnapshot: snapshot.loadedFromSnapshot,
	}
}

func expectedPromptEvidenceForTest(snapshotID string, fields PromptParityFields) PromptParityEvidence {
	return PromptParityEvidence{origin: promptOriginExpectedSnapshot, evidenceID: strings.TrimSpace(snapshotID), fields: fields}
}

type ResumeIdentityEvidence struct {
	PublicThreadID   string
	ProviderThreadID string
	ResumedThreadID  string
}

type RuntimeReportEvidence struct {
	AgentID        string
	Provider       string
	SessionURLPort string
	StdioMode      string
	DeferredReason string
}

type OutcomeEvidence struct {
	ObservedActionID       string
	StateBefore            string
	StateAfter             string
	ExpectedDependencyName string
	DependencyName         string
	Profile                contract.DependencyProfile
	Unsupported            *UnsupportedOutcomeEvidence
}

type UnsupportedOutcomeEvidence struct {
	err            error
	dependencyName string
	profile        contract.DependencyProfile
	operationID    string
}

func CaptureUnsupportedOutcome(t testing.TB, operationID, dependencyName string, profile contract.DependencyProfile, run func() error) *UnsupportedOutcomeEvidence {
	t.Helper()
	if strings.TrimSpace(operationID) == "" {
		t.Fatal("unsupported outcome operation id is required")
	}
	if run == nil {
		t.Fatal("unsupported outcome capture requires a provider operation")
	}
	err := run()
	unsupported := &UnsupportedOutcomeEvidence{
		err:            err,
		dependencyName: strings.TrimSpace(dependencyName),
		profile:        profile,
		operationID:    strings.TrimSpace(operationID),
	}
	if validateUnsupportedOutcome(unsupported) != nil {
		t.Fatalf("unsupported outcome %s did not return the required dependency-mode error: %v", operationID, err)
	}
	return unsupported
}

type CaseEvidence struct {
	assertions map[EvidenceKey]string
	invalid    []string
}

func NewEvidence() *CaseEvidence {
	return &CaseEvidence{assertions: map[EvidenceKey]string{}}
}

func (e *CaseEvidence) AssertEqual(t *testing.T, key EvidenceKey, got, want any) {
	t.Helper()
	if key == "" {
		t.Fatal("provider contract evidence key is required")
	}
	if reservedEvidenceKeys[key] {
		e.invalid = append(e.invalid, fmt.Sprintf("%s must be recorded through a typed evidence helper", key))
		return
	}
	if isTautologicalEvidence(got, want) {
		e.invalid = append(e.invalid, fmt.Sprintf("%s used tautological evidence", key))
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s got %#v, want %#v", key, got, want)
	}
	e.assertions[key] = fmt.Sprintf("%#v", got)
}

func (e *CaseEvidence) AssertNoError(t *testing.T, key EvidenceKey, err error) {
	t.Helper()
	if key == "" {
		t.Fatal("provider contract evidence key is required")
	}
	if reservedEvidenceKeys[key] {
		e.invalid = append(e.invalid, fmt.Sprintf("%s must be recorded through a typed evidence helper", key))
		return
	}
	if err != nil {
		t.Fatalf("%s error = %v", key, err)
	}
	e.assertions[key] = "ok"
}

func (e *CaseEvidence) RecordEventTranslation(t *testing.T, name string, got, want EventTranslationEvidence) {
	t.Helper()
	if strings.TrimSpace(name) == "" {
		t.Fatal("event translation evidence name is required")
	}
	if got.origin != eventOriginProviderTranslator || want.origin != eventOriginExpectedSnapshot {
		e.invalid = append(e.invalid, "event translation evidence must compare provider_translator to expected_event_snapshot")
		return
	}
	if !want.loadedFromSnapshot {
		e.invalid = append(e.invalid, "event translation expectation must be loaded from an independent checked-in snapshot")
		return
	}
	if got.evidenceID == "" || want.evidenceID == "" || got.evidenceID == want.evidenceID {
		e.invalid = append(e.invalid, "event translation evidence must use distinct capture and expected snapshot ids")
		return
	}
	if len(got.canonicalJSON) == 0 || len(want.canonicalJSON) == 0 {
		e.invalid = append(e.invalid, "event translation evidence must include observed and expected events")
		return
	}
	if !bytes.Equal(got.canonicalJSON, want.canonicalJSON) {
		t.Fatalf("event translation %s got %s, want %s", name, got.canonicalJSON, want.canonicalJSON)
	}
	e.assertions[EvidenceEventTranslated] = fmt.Sprintf("%s/%s/%s", name, got.evidenceID, want.evidenceID)
}

func (e *CaseEvidence) RecordPromptParity(t *testing.T, got, want PromptParityEvidence) {
	t.Helper()
	if got.origin != promptOriginProviderRequest || want.origin != promptOriginExpectedSnapshot {
		e.invalid = append(e.invalid, "prompt parity evidence must compare provider_request to expected_snapshot")
		return
	}
	if !want.loadedFromSnapshot {
		e.invalid = append(e.invalid, "prompt parity evidence must compare captured provider_request to an independent expected_snapshot")
		return
	}
	if got.evidenceID == "" || want.evidenceID == "" || got.evidenceID == want.evidenceID {
		e.invalid = append(e.invalid, "prompt parity evidence must compare captured provider_request to an independent expected_snapshot")
		return
	}
	if got.fields.BaseInstructions == "" || got.fields.DeveloperInstructions == "" || got.fields.PrefixHash == "" || got.fields.Boundary == "" || got.fields.SectionSnapshot == "" {
		t.Fatal("prompt parity evidence must contain all captured provider prompt fields")
	}
	if want.fields.BaseInstructions == "" || want.fields.DeveloperInstructions == "" || want.fields.PrefixHash == "" || want.fields.Boundary == "" || want.fields.SectionSnapshot == "" {
		t.Fatal("prompt parity expectation must contain all expected prompt fields")
	}
	e.recordPromptField(t, EvidencePromptBaseInstructions, got.fields.BaseInstructions, want.fields.BaseInstructions)
	e.recordPromptField(t, EvidencePromptDeveloperInstructions, got.fields.DeveloperInstructions, want.fields.DeveloperInstructions)
	e.recordPromptField(t, EvidencePromptPrefixHash, got.fields.PrefixHash, want.fields.PrefixHash)
	e.recordPromptField(t, EvidencePromptBoundary, got.fields.Boundary, want.fields.Boundary)
	e.recordPromptField(t, EvidencePromptSectionSnapshot, got.fields.SectionSnapshot, want.fields.SectionSnapshot)
}

func (e *CaseEvidence) recordPromptField(t *testing.T, key EvidenceKey, got, want string) {
	t.Helper()
	if strings.TrimSpace(got) == "" || strings.TrimSpace(want) == "" {
		e.invalid = append(e.invalid, fmt.Sprintf("%s prompt evidence is blank", key))
		return
	}
	if got != want {
		t.Fatalf("%s got %q, want %q", key, got, want)
	}
	e.assertions[key] = got
}

func (e *CaseEvidence) RecordResumeIdentity(t *testing.T, identity ResumeIdentityEvidence) {
	t.Helper()
	if strings.TrimSpace(identity.PublicThreadID) == "" || strings.TrimSpace(identity.ProviderThreadID) == "" || strings.TrimSpace(identity.ResumedThreadID) == "" {
		t.Fatal("resume identity evidence requires public, provider, and resumed thread ids")
	}
	if identity.ResumedThreadID != identity.ProviderThreadID {
		t.Fatalf("resume used thread id %q, want provider thread id %q", identity.ResumedThreadID, identity.ProviderThreadID)
	}
	if identity.ResumedThreadID == identity.PublicThreadID {
		t.Fatalf("resume reinvented public thread id %q instead of provider thread id", identity.PublicThreadID)
	}
	e.assertions[EvidenceResumeIdentity] = fmt.Sprintf("%s/%s/%s", identity.PublicThreadID, identity.ProviderThreadID, identity.ResumedThreadID)
}

func (e *CaseEvidence) RecordRuntimeReport(t *testing.T, report RuntimeReportEvidence) {
	t.Helper()
	if strings.TrimSpace(report.AgentID) == "" || strings.TrimSpace(report.Provider) == "" {
		t.Fatal("runtime report evidence requires agent id and provider")
	}
	if strings.TrimSpace(report.SessionURLPort) == "" && strings.TrimSpace(report.StdioMode) == "" && strings.TrimSpace(report.DeferredReason) == "" {
		t.Fatal("runtime report evidence requires session URL port, stdio mode, or deferred reason")
	}
	e.assertions[EvidenceRuntimeReportPayload] = fmt.Sprintf("%s/%s/%s/%s/%s", report.AgentID, report.Provider, report.SessionURLPort, report.StdioMode, report.DeferredReason)
}

func (e *CaseEvidence) RecordOutcome(t *testing.T, key EvidenceKey, outcome OutcomeEvidence) {
	t.Helper()
	switch key {
	case EvidenceApprovalOutcome, EvidenceInterruptOutcome, EvidenceForceCompleteOutcome, EvidenceToolbridgeDependency:
	default:
		t.Fatalf("unsupported outcome evidence key %s", key)
	}
	if strings.TrimSpace(outcome.StateAfter) == "" {
		e.invalid = append(e.invalid, fmt.Sprintf("%s outcome evidence must include the observed final state", key))
		return
	}
	if strings.TrimSpace(outcome.ObservedActionID) == "" {
		if err := validateUnsupportedOutcome(outcome.Unsupported); err != nil {
			e.invalid = append(e.invalid, fmt.Sprintf("%s outcome evidence must include an observed action id or typed unsupported result: %v", key, err))
			return
		}
	}
	if key == EvidenceToolbridgeDependency {
		if outcome.Unsupported != nil {
			outcome.DependencyName = outcome.Unsupported.dependencyName
			outcome.Profile = outcome.Unsupported.profile
		}
		if strings.TrimSpace(outcome.DependencyName) == "" || outcome.Profile == "" {
			e.invalid = append(e.invalid, "toolbridge dependency evidence must include dependency name and profile")
			return
		}
	}
	if outcome.Unsupported != nil {
		if strings.TrimSpace(outcome.ExpectedDependencyName) == "" {
			e.invalid = append(e.invalid, fmt.Sprintf("%s unsupported outcome must declare the expected dependency name", key))
			return
		}
		if outcome.ExpectedDependencyName != outcome.Unsupported.dependencyName {
			e.invalid = append(e.invalid, fmt.Sprintf("%s unsupported dependency = %s, want %s", key, outcome.Unsupported.dependencyName, outcome.ExpectedDependencyName))
			return
		}
	}
	e.assertions[key] = fmt.Sprintf("%s/%s/%s/%s/%s/%t", outcome.ObservedActionID, outcome.StateBefore, outcome.StateAfter, outcome.DependencyName, outcome.Profile, outcome.Unsupported != nil)
}

func validateUnsupportedOutcome(unsupported *UnsupportedOutcomeEvidence) error {
	if unsupported == nil {
		return errors.New("typed unsupported evidence is required")
	}
	if strings.TrimSpace(unsupported.operationID) == "" {
		return errors.New("observed provider operation id is required")
	}
	if strings.TrimSpace(unsupported.dependencyName) == "" || unsupported.profile == "" {
		return errors.New("dependency name and profile are required")
	}
	modeErr, ok := concreteDependencyModeError(unsupported.err)
	if !ok {
		return fmt.Errorf("unsupported outcome error = %v, want concrete dependency mode error in unwrap chain", unsupported.err)
	}
	if modeErr.Name != unsupported.dependencyName || modeErr.Profile != unsupported.profile {
		return fmt.Errorf("unsupported outcome dependency = %s/%s, want %s/%s", modeErr.Name, modeErr.Profile, unsupported.dependencyName, unsupported.profile)
	}
	if !errors.Is(modeErr.Err, contract.ErrUnsupportedDependencyMode) && !errors.Is(modeErr.Err, contract.ErrDependencyDeferred) {
		return fmt.Errorf("unsupported outcome error = %v, want dependency mode error", unsupported.err)
	}
	return nil
}

func concreteDependencyModeError(err error) (contract.DependencyModeError, bool) {
	for err != nil {
		switch typed := err.(type) {
		case contract.DependencyModeError:
			return typed, true
		case *contract.DependencyModeError:
			if typed != nil {
				return *typed, true
			}
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return contract.DependencyModeError{}, false
		}
		err = unwrapped.Unwrap()
	}
	return contract.DependencyModeError{}, false
}

func (e *CaseEvidence) Validate(caseName string, required []EvidenceKey) error {
	if e == nil {
		return fmt.Errorf("provider contract case %s returned no evidence", caseName)
	}
	if len(e.invalid) > 0 {
		return fmt.Errorf("provider contract case %s returned invalid evidence: %s", caseName, strings.Join(e.invalid, ", "))
	}
	if len(e.assertions) == 0 {
		return fmt.Errorf("provider contract case %s returned no evidence", caseName)
	}
	for _, key := range required {
		if _, ok := e.assertions[key]; !ok {
			return fmt.Errorf("provider contract case %s missing evidence key %s", caseName, key)
		}
	}
	return nil
}

func isTautologicalEvidence(got, want any) bool {
	if got == nil || want == nil {
		return false
	}
	if reflect.TypeOf(got) != reflect.TypeOf(want) {
		return false
	}
	switch got.(type) {
	case bool:
		return true
	case string:
		return strings.TrimSpace(got.(string)) == "" && strings.TrimSpace(want.(string)) == ""
	}
	return false
}

func ValidateSpec(spec Spec) error {
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("provider contract spec name is required")
	}
	if spec.Start == nil {
		return errors.New("provider contract Start function is required")
	}
	if spec.Resume == nil {
		return errors.New("provider contract Resume function is required")
	}
	if len(spec.EventCases) == 0 {
		return errors.New("provider contract event cases are required")
	}
	for _, c := range spec.EventCases {
		if strings.TrimSpace(c.Name) == "" || c.Run == nil {
			return errors.New("provider contract event case is incomplete")
		}
	}
	for _, key := range []CaseKey{CasePromptParity, CaseApproval, CaseInterrupt, CaseForceComplete, CaseResume, CaseToolbridge, CaseRuntimeReport} {
		c, ok := spec.RequiredCases[key]
		if !ok || strings.TrimSpace(c.Name) == "" || c.Run == nil {
			return fmt.Errorf("provider contract case %s is required", key)
		}
	}
	return nil
}

func Run(t *testing.T, spec Spec) {
	t.Helper()
	if err := RunSpecForTest(t, spec); err != nil {
		t.Fatal(err)
	}
}

func RunSpecForTest(t *testing.T, spec Spec) error {
	t.Helper()
	if err := ValidateSpec(spec); err != nil {
		return err
	}
	t.Run("start carries prompt snapshot", func(t *testing.T) {
		t.Helper()
		session, err := spec.Start(context.Background(), dto.StartSessionRequest{
			ThreadID: "thread-contract",
			StartAssembly: dto.StartAssembly{
				DisplayName:           "contract",
				BaseInstructions:      "base",
				DeveloperInstructions: "developer",
				PrefixShape:           dto.PrefixShape{Hash: "hash-contract"},
			},
		})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if session == nil || strings.TrimSpace(session.ThreadID()) == "" {
			t.Fatalf("Start() session = %T thread=%q", session, sessionThreadID(session))
		}
		assertSessionContract(t, session)
	})
	t.Run("resume returns usable session", func(t *testing.T) {
		t.Helper()
		session, err := spec.Resume(context.Background(), dto.ResumeSessionRequest{
			ThreadID:         "thread-contract",
			ProviderThreadID: "provider-thread-contract",
		})
		if err != nil {
			t.Fatalf("Resume() error = %v", err)
		}
		if session == nil || strings.TrimSpace(session.ThreadID()) == "" {
			t.Fatalf("Resume() session = %T thread=%q", session, sessionThreadID(session))
		}
		assertSessionContract(t, session)
	})
	for _, c := range spec.EventCases {
		if err := runCase(t, "event/"+c.Name, c, []EvidenceKey{EvidenceEventTranslated}); err != nil {
			return err
		}
	}
	for _, key := range []CaseKey{CasePromptParity, CaseApproval, CaseInterrupt, CaseForceComplete, CaseResume, CaseToolbridge, CaseRuntimeReport} {
		c := spec.RequiredCases[key]
		if err := runCase(t, string(key)+"/"+c.Name, c, requiredEvidenceByCase[key]); err != nil {
			return err
		}
	}
	return nil
}

func runCase(t *testing.T, name string, c Case, required []EvidenceKey) error {
	t.Helper()
	evidence := NewEvidence()
	passed := t.Run(name, func(t *testing.T) {
		t.Helper()
		c.Run(t, evidence)
	})
	if !passed {
		return fmt.Errorf("provider contract case %s failed", name)
	}
	return evidence.Validate(name, required)
}

func sessionThreadID(session contract.Session) string {
	if session == nil {
		return ""
	}
	return session.ThreadID()
}

func assertSessionContract(t *testing.T, session contract.Session) {
	t.Helper()
	if strings.TrimSpace(session.ThreadID()) == "" {
		t.Fatal("session ThreadID is empty")
	}
	if strings.TrimSpace(session.RolloutPath()) == "" {
		t.Fatal("session RolloutPath is empty")
	}
	assertStartTurnContract(t, session)
	caps := session.Capabilities()
	assertThreadListContract(t, session, caps)
	if _, err := session.ReadHistory(context.Background(), session.ThreadID(), 10); err != nil {
		t.Fatalf("ReadHistory() error = %v", err)
	}
	if err := session.Configure(context.Background(), dto.ThreadConfigPatch{}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if err := session.Interrupt(context.Background(), dto.InterruptRequest{}); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	if err := session.ForceComplete(context.Background(), dto.ForceCompleteRequest{}); err != nil {
		t.Fatalf("ForceComplete() error = %v", err)
	}
	assertThreadForkContract(t, session, caps)
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := session.ForceStop(); err != nil {
		t.Fatalf("ForceStop() error = %v", err)
	}
}

func assertStartTurnContract(t *testing.T, session contract.Session) {
	t.Helper()
	handle, err := session.StartTurn(context.Background(), dto.TurnRequest{
		LocalID:  "turn-contract",
		ThreadID: session.ThreadID(),
		Inputs: []dto.InputItem{{Type: "text", Content: "contract turn"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if handle == nil || strings.TrimSpace(handle.LocalID()) == "" || strings.TrimSpace(handle.ProviderID()) == "" {
		t.Fatalf("StartTurn() handle = %#v, want local and provider ids", handle)
	}
	select {
	case <-handle.Done():
	default:
		t.Fatal("StartTurn() handle Done channel is not closed for deterministic fixture")
	}
	if err := handle.Err(); err != nil {
		t.Fatalf("StartTurn() handle Err() = %v", err)
	}
}

func assertThreadListContract(t *testing.T, session contract.Session, caps dto.CapabilitySet) {
	t.Helper()
	_, err := session.ListThreads(context.Background())
	if caps.Has(dto.CapThreadList) {
		if err != nil {
			t.Fatalf("ListThreads() error = %v", err)
		}
		return
	}
	var capErr *contract.CapabilityError
	if !errors.As(err, &capErr) || capErr.Capability != dto.CapThreadList {
		t.Fatalf("ListThreads() error = %v, want thread list CapabilityError", err)
	}
}

func assertThreadForkContract(t *testing.T, session contract.Session, caps dto.CapabilitySet) {
	t.Helper()
	_, err := session.ForkThread(context.Background(), dto.ForkRequest{ThreadID: session.ThreadID()})
	if caps.Has(dto.CapThreadFork) {
		if err != nil {
			t.Fatalf("ForkThread() error = %v", err)
		}
		return
	}
	var capErr *contract.CapabilityError
	if !errors.As(err, &capErr) || capErr.Capability != dto.CapThreadFork {
		t.Fatalf("ForkThread() error = %v, want thread fork CapabilityError", err)
	}
}
```

Also provide `NewFixtureSession` and `CompleteFixtureSpec` inside the same package or a test-only helper file. `CompleteFixtureSpec` must include required cases for prompt parity, approval, interrupt, force-complete, resume identity, toolbridge, and runtime report; the resume fixture must call `RecordResumeIdentity` with distinct public and provider thread ids. The fixture must implement every current `contract.Session` method with deterministic in-memory behavior: `ThreadID`, `RolloutPath`, `Capabilities`, `StartTurn`, `Interrupt`, `ForceComplete`, `ListThreads`, `ForkThread`, `ReadHistory`, `Configure`, `Close`, and `ForceStop`. If the fixture does not declare `dto.CapThreadList` or `dto.CapThreadFork`, `ListThreads` or `ForkThread` must return `*contract.CapabilityError` for the matching capability rather than a nil/empty success.

- [ ] **Step 3: Run harness tests**

Use LSP `file(diagnostics)` for:

- `internal/provider/contracttest/suite.go`
- `internal/provider/contracttest/suite_test.go`

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/contracttest -count=1
```

Expected: PASS.

### Task C2: Require Contract Tests For Provider Packages

- [ ] **Step 1: Add provider manifest guard**

Create `internal/provider/provider_contract_manifest_test.go`:

```go
package provider_test

import (
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestProviderPackagesHaveContractTests(t *testing.T) {
	root := "."
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "contracttest" || name == "shared" || name == "toolfilter" || name == "e2e" || name == "e2efixture" || name == "_template" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, "module.go")); err != nil {
			continue
		}
		packageDir := filepath.Join(root, name)
		testPath := filepath.Join(root, name, "provider_contract_test.go")
		if _, err := os.Stat(testPath); err != nil {
			missing = append(missing, name)
			continue
		}
			packageFiles := providerPackageFiles(t, packageDir)
			if !providerContractTestCallsSharedSuite(t, packageFiles) {
				missing = append(missing, name+": provider_contract_test.go does not call contracttest.Run with a complete spec")
			}
	}
	if len(missing) > 0 {
		t.Fatalf("provider packages missing provider_contract_test.go: %v", missing)
	}
}

type typedProviderPackage struct {
	typeInfo *types.Info
	pkgPath  string
}

var typedProviderPackages = map[string]typedProviderPackage{}

func providerPackageFiles(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	files := map[string]*ast.File{}
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedFiles,
		Dir:   dir,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		t.Fatalf("load provider package %s: %v", dir, err)
	}
	pkg := selectProviderContractPackage(t, pkgs)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolve provider dir %s: %v", dir, err)
	}
	typedProviderPackages[absDir] = typedProviderPackage{typeInfo: pkg.TypesInfo, pkgPath: pkg.Types.Path()}
	for _, file := range pkg.Syntax {
		path := pkg.Fset.Position(file.Pos()).Filename
		absPath, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("resolve provider file %s: %v", path, err)
		}
		if filepath.Dir(absPath) != absDir {
			continue
		}
		files[absPath] = file
	}
	return files
}

func selectProviderContractPackage(t *testing.T, pkgs []*packages.Package) *packages.Package {
	t.Helper()
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			if strings.HasSuffix(pkg.Fset.Position(file.Pos()).Filename, "provider_contract_test.go") {
				return pkg
			}
		}
	}
	t.Fatalf("loaded packages do not contain provider_contract_test.go")
	return nil
}

func providerContractTestCallsSharedSuite(t *testing.T, files map[string]*ast.File) bool {
	t.Helper()
	validRuns := 0
	invalidRun := false
	typeInfo, providerPkgPath := loadTypedProviderPackage(t, files)
	roots := map[*types.Func]bool{}
	for path, file := range files {
		if !isContractOwnedProviderTest(path) {
			continue
		}
		aliases := contracttestAliases(file)
		if len(aliases) == 0 {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if ok && selector.Sel.Name == "Run" {
					if ident, ok := selector.X.(*ast.Ident); ok && aliases[ident.Name] {
						specFuncs := providerContractRunSpecFuncs(call, typeInfo)
						if !providerContractRunUsesCompleteSpec(specFuncs) || !providerContractRunSpecsArePackageLocal(specFuncs, providerPkgPath) {
							invalidRun = true
							return false
						}
						validRuns++
						for _, fn := range specFuncs {
							roots[fn] = true
						}
					}
				}
				return true
			})
	}
	return validRuns > 0 && !invalidRun && providerContractTestRejectsShallowCases(t, files, roots, typeInfo)
}

func isContractOwnedProviderTest(path string) bool {
	base := filepath.Base(path)
	return base == "provider_contract_test.go" ||
		base == "provider_contract_helpers_test.go" ||
		strings.HasSuffix(base, "_contract_test.go")
}

func contracttestAliases(file *ast.File) map[string]bool {
	contracttestAliases := map[string]bool{}
	for _, imported := range file.Imports {
		if strings.Trim(imported.Path.Value, `"`) != "github.com/anthropic-ai/super-agent-v3/internal/provider/contracttest" {
			continue
		}
		alias := "contracttest"
		if imported.Name != nil {
			alias = imported.Name.Name
		}
		contracttestAliases[alias] = true
	}
	return contracttestAliases
}

func providerContractRunUsesCompleteSpec(specFuncs []*types.Func) bool {
	for _, fn := range specFuncs {
		name := fn.Name()
		if strings.HasSuffix(name, "ContractSpec") || strings.HasPrefix(name, "Complete") || strings.Contains(strings.ToLower(name), "contractspec") {
			return true
		}
	}
	return false
}

func providerContractRunSpecsArePackageLocal(specFuncs []*types.Func, providerPkgPath string) bool {
	if len(specFuncs) == 0 {
		return false
	}
	for _, fn := range specFuncs {
		if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != providerPkgPath {
			return false
		}
	}
	return true
}

func providerContractRunSpecFuncs(call *ast.CallExpr, typeInfo *types.Info) []*types.Func {
	if len(call.Args) < 2 {
		return nil
	}
	switch arg := call.Args[1].(type) {
	case *ast.CallExpr:
		if ident, ok := arg.Fun.(*ast.Ident); ok {
			if fn, ok := typeInfo.Uses[ident].(*types.Func); ok {
				return []*types.Func{fn}
			}
		}
	case *ast.Ident:
		if fn, ok := typeInfo.Uses[arg].(*types.Func); ok {
			return []*types.Func{fn}
		}
	}
	return nil
}

func providerContractHelpersStayTypedAndSafe(t *testing.T, files map[string]*ast.File, roots map[*types.Func]bool) bool {
	t.Helper()
	// Build a package-local function index for helpers referenced from provider
	// contract specs or Case.Run closures. Contract roots must live in
	// contract-owned files, but reachable package-local helpers may be existing
	// provider request-builder/session fake helpers when they are type-resolved
	// and contain no forbidden constructs.
	// Known e2e/smoke/platform helpers stay legal only when they are not reachable
	// from the provider contract suite.
	// The production implementation must resolve callees with go/packages + go/types:
	// selector calls are traversed only when the selected object belongs to the
	// provider package, and external selectors are allowed only after their import
	// path is known. Do not queue selectors by bare method name.
	typeInfo, providerPkgPath := loadTypedProviderPackage(t, files)
	return contractReachableHelpersAreOwned(files, roots, typeInfo, providerPkgPath)
}

func providerContractTestRejectsShallowCases(t *testing.T, files map[string]*ast.File, roots map[*types.Func]bool, typeInfo *types.Info) bool {
	t.Helper()
	ok := true
	for path, file := range files {
			contractOwned := isContractOwnedProviderTest(path)
			aliases := contracttestAliases(file)
			if aliases["."] {
				ok = false
				return false
			}
			if packageLevelDependencyModeConstructs(file, typeInfo) {
				return false
			}
			ast.Inspect(file, func(node ast.Node) bool {
			if call, isCall := node.(*ast.CallExpr); isCall && isContracttestCompleteFixtureCall(call, aliases) {
				ok = false
				return false
			}
			if lit, isLiteral := node.(*ast.CompositeLit); isLiteral && isCaseEvidenceComposite(lit) {
				ok = false
				return false
			}
			if selector, isSelector := node.(*ast.SelectorExpr); isSelector {
				if isContracttestSelector(selector, aliases, "CompleteFixtureSpec") {
					ok = false
					return false
				}
				if contractOwned && isTestingSkipSelector(selector) {
					ok = false
					return false
				}
			}
			fn, isFunc := node.(*ast.FuncLit)
			if !contractOwned || !isFunc {
				return true
			}
			if fn.Body == nil || len(fn.Body.List) == 0 {
				ok = false
				return false
			}
			for _, stmt := range fn.Body.List {
				ret, isReturn := stmt.(*ast.ReturnStmt)
				if !isReturn {
					continue
				}
				for _, result := range ret.Results {
					lit, isLiteral := result.(*ast.CompositeLit)
					if !isLiteral {
						continue
					}
					if len(lit.Elts) == 0 {
						ok = false
						return false
					}
				}
			}
			return true
		})
		if !ok {
			return false
		}
	}
	return ok && providerContractHelpersStayTypedAndSafe(t, files, roots)
}

func packageLevelDependencyModeConstructs(file *ast.File, typeInfo *types.Info) bool {
	if file == nil {
		return false
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			switch typed := spec.(type) {
			case *ast.TypeSpec:
				if resolvesToDependencyModeError(typeInfo, typed.Type) {
					return true
				}
			case *ast.ValueSpec:
				if resolvesToDependencyModeError(typeInfo, typed.Type) {
					return true
				}
				for _, value := range typed.Values {
					if resolvesToDependencyModeError(typeInfo, value) || reachableForbiddenNode(value, typeInfo) {
						return true
					}
				}
			}
		}
	}
	return false
}

func isContracttestSelector(selector *ast.SelectorExpr, aliases map[string]bool, name string) bool {
	if selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && aliases[ident.Name]
}

func isContracttestCompleteFixtureCall(call *ast.CallExpr, aliases map[string]bool) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return isContracttestSelector(fun, aliases, "CompleteFixtureSpec")
	case *ast.Ident:
		return aliases["."] && fun.Name == "CompleteFixtureSpec"
	default:
		return false
	}
}

func isTestingSkipSelector(selector *ast.SelectorExpr) bool {
	switch selector.Sel.Name {
	case "Skip", "Skipf", "SkipNow":
		return true
	default:
		return false
	}
}

func loadTypedProviderPackage(t *testing.T, files map[string]*ast.File) (*types.Info, string) {
	t.Helper()
	dir := providerDirFromFiles(files)
	typed, ok := typedProviderPackages[dir]
	if !ok || typed.typeInfo == nil || typed.pkgPath == "" {
		t.Fatalf("typed provider package metadata for %s is missing", dir)
	}
	return typed.typeInfo, typed.pkgPath
}

func providerDirFromFiles(files map[string]*ast.File) string {
	for path := range files {
		return filepath.Dir(path)
	}
	return "."
}

func contractReachableHelpersAreOwned(files map[string]*ast.File, roots map[*types.Func]bool, typeInfo *types.Info, providerPkgPath string) bool {
	type fnDecl struct {
		path string // retained for diagnostics; provider-surface helpers are allowed when type-resolved and safe.
		decl *ast.FuncDecl
	}
		decls := map[*types.Func]fnDecl{}
		queue := []*types.Func{}
	for path, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
				obj, _ := typeInfo.Defs[fn.Name].(*types.Func)
				if obj == nil {
					continue
				}
				decls[obj] = fnDecl{path: path, decl: fn}
			}
		}
		for fn := range roots {
			if fn != nil {
				queue = append(queue, fn)
			}
			}
			seen := map[*types.Func]bool{}
			allowedTranslatorCallbacks := map[*types.Func]bool{}
			blocked := map[string]bool{}
			for len(queue) > 0 {
			fn := queue[0]
			queue = queue[1:]
			if seen[fn] {
				continue
			}
			seen[fn] = true
			current, ok := decls[fn]
		if !ok || current.decl.Body == nil {
			continue
		}
		if shadowsAllowedContractNames(current.decl.Body) {
			return false
		}
			ast.Inspect(current.decl.Body, func(node ast.Node) bool {
					if reachableForbiddenNode(node, typeInfo) {
						blocked["__forbidden_reachable_construct__"] = true
						return false
					}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
				switch fun := call.Fun.(type) {
					case *ast.Ident:
						obj := typeInfo.Uses[fun]
						if target, ok := obj.(*types.Func); ok {
							if _, exists := decls[target]; exists {
								queue = append(queue, target)
								return true
							}
						}
						if callback, ok := obj.(*types.Var); ok && allowedTranslatorCallbacks[fn] && isAllowedTranslatorEmitCallback(current.decl, callback, typeInfo) {
							return true
						}
						if _, ok := obj.(*types.Builtin); ok {
							return true
						}
					if obj == nil {
						blocked["__unsafe_func_var_call__"] = true
						return false
					}
					blocked["__unsafe_func_var_call__"] = true
					return false
					case *ast.SelectorExpr:
						if captured, kind, isCapture := contracttestCaptureFunctionArgument(fun, call, typeInfo, providerPkgPath); isCapture {
							if captured == nil {
								blocked["__untraversed_capture_function_arg__"] = true
								return false
							}
							queue = append(queue, captured)
							if kind == "event_translator" {
								allowedTranslatorCallbacks[captured] = true
							}
							return true
						}
						resolved := resolveSelectorWithTypes(fun, typeInfo, providerPkgPath)
						if resolved.PackageLocalFunc != nil {
						queue = append(queue, resolved.PackageLocalFunc)
						return true
					}
					if !resolved.ExternalSafe {
						blocked["__unsafe_selector_call__"] = true
						return false
					}
				}
			return true
		})
				if blocked["__forbidden_reachable_construct__"] || blocked["__unsafe_func_var_call__"] || blocked["__unsafe_selector_call__"] || blocked["__untraversed_capture_function_arg__"] {
					return false
				}
	}
	return true
}

func reachableForbiddenNode(node ast.Node, typeInfo *types.Info) bool {
	if ident, ok := node.(*ast.Ident); ok && resolvesToDependencyModeError(typeInfo, ident) {
		return true
	}
	if lit, ok := node.(*ast.CompositeLit); ok && isCaseEvidenceComposite(lit) {
		return true
	}
	if lit, ok := node.(*ast.CompositeLit); ok {
		if resolvesToDependencyModeError(typeInfo, lit.Type) || resolvesToDependencyModeError(typeInfo, lit) {
			return true
		}
	}
	if valueSpec, ok := node.(*ast.ValueSpec); ok {
		if resolvesToDependencyModeError(typeInfo, valueSpec.Type) {
			return true
		}
	}
	if typeSpec, ok := node.(*ast.TypeSpec); ok {
		if resolvesToDependencyModeError(typeInfo, typeSpec.Type) {
			return true
		}
	}
	if selector, ok := node.(*ast.SelectorExpr); ok {
		if isTestingSkipSelector(selector) || selector.Sel.Name == "CompleteFixtureSpec" || selector.Sel.Name == "NewDependencyModeError" {
			return true
		}
	}
	if call, ok := node.(*ast.CallExpr); ok {
		if ident, ok := call.Fun.(*ast.Ident); ok && (ident.Name == "CompleteFixtureSpec" || ident.Name == "NewDependencyModeError") {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "new" && len(call.Args) == 1 && resolvesToDependencyModeError(typeInfo, call.Args[0]) {
			return true
		}
		if resolvesToDependencyModeError(typeInfo, call.Fun) || resolvesToDependencyModeError(typeInfo, call) {
			return true
		}
	}
	return false
}

func contracttestCaptureFunctionArgument(selector *ast.SelectorExpr, call *ast.CallExpr, typeInfo *types.Info, providerPkgPath string) (*types.Func, string, bool) {
	if selector == nil || call == nil || typeInfo == nil {
		return nil, "", false
	}
	fn, _ := typeInfo.Uses[selector.Sel].(*types.Func)
	if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/anthropic-ai/super-agent-v3/internal/provider/contracttest" {
		return nil, "", false
	}
	var arg ast.Expr
	kind := ""
	switch fn.Name() {
	case "CaptureProviderEventTranslation":
		if len(call.Args) < 4 {
			return nil, "event_translator", true
		}
		arg = call.Args[3]
		kind = "event_translator"
	case "CaptureUnsupportedOutcome":
		if len(call.Args) < 5 {
			return nil, "unsupported_operation", true
		}
		arg = call.Args[4]
		kind = "unsupported_operation"
	default:
		return nil, "", false
	}
	ident, ok := arg.(*ast.Ident)
	if !ok {
		return nil, kind, true
	}
	target, _ := typeInfo.Uses[ident].(*types.Func)
	if target == nil || target.Pkg() == nil || target.Pkg().Path() != providerPkgPath {
		return nil, kind, true
	}
	return target, kind, true
}

func isAllowedTranslatorEmitCallback(decl *ast.FuncDecl, callback *types.Var, typeInfo *types.Info) bool {
	if decl == nil || decl.Type == nil || decl.Type.Params == nil || callback == nil {
		return false
	}
	if callback.Name() != "emit" {
		return false
	}
	if decl.Type.Params.NumFields() != 2 {
		return false
	}
	firstParam := decl.Type.Params.List[0]
	if !strings.Contains(types.ExprString(firstParam.Type), "RawProviderEvent") {
		return false
	}
	sig, ok := callback.Type().Underlying().(*types.Signature)
	if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 0 {
		return false
	}
	for _, field := range decl.Type.Params.List {
		for _, name := range field.Names {
			if typeInfo.Defs[name] == callback {
				return true
			}
		}
	}
	return false
}

func resolvesToDependencyModeError(typeInfo *types.Info, expr ast.Expr) bool {
	if typeInfo == nil || expr == nil {
		return false
	}
	typ := typeInfo.TypeOf(expr)
	for typ != nil {
		typ = types.Unalias(typ)
		named, ok := typ.(*types.Named)
		if !ok {
			ptr, isPtr := typ.(*types.Pointer)
			if !isPtr {
				return false
			}
			typ = ptr.Elem()
			continue
		}
		obj := named.Obj()
		return obj != nil && obj.Name() == "DependencyModeError" && obj.Pkg() != nil && obj.Pkg().Path() == "github.com/anthropic-ai/super-agent-v3/internal/contract"
	}
	return false
}

func shadowsAllowedContractNames(body *ast.BlockStmt) bool {
	shadowed := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.AssignStmt:
			if stmt.Tok.String() != ":=" {
				return true
			}
			for _, lhs := range stmt.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && isAllowedContractName(ident.Name) {
					shadowed = true
					return false
				}
			}
		case *ast.ValueSpec:
			for _, name := range stmt.Names {
				if isAllowedContractName(name.Name) {
					shadowed = true
					return false
				}
			}
		}
		return true
	})
	return shadowed
}

func isAllowedContractName(name string) bool {
	switch name {
	case "append", "len", "make", "new", "contracttest", "contract", "dto", "errors", "strings", "t", "e":
		return true
	default:
		return false
	}
}

type selectorResolution struct {
	PackageLocalFunc *types.Func
	ExternalSafe     bool
}

func resolveSelectorWithTypes(selector *ast.SelectorExpr, typeInfo *types.Info, providerPkgPath string) selectorResolution {
	if typeInfo == nil {
		return selectorResolution{}
	}
	obj := typeInfo.Uses[selector.Sel]
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return selectorResolution{}
	}
	if fn.Pkg().Path() == providerPkgPath {
		return selectorResolution{PackageLocalFunc: fn}
	}
	if isApprovedExternalContractFunction(fn) {
		return selectorResolution{ExternalSafe: true}
	}
	return selectorResolution{}
}

func isApprovedExternalContractFunction(fn *types.Func) bool {
	pkgPath := fn.Pkg().Path()
	switch pkgPath {
	case "encoding/json", "context", "errors", "strings", "testing",
		"github.com/anthropic-ai/super-agent-v3/internal/contract",
		"github.com/anthropic-ai/super-agent-v3/internal/dto/provider",
		"github.com/anthropic-ai/super-agent-v3/internal/provider/contracttest",
		"github.com/anthropic-ai/super-agent-v3/internal/platform/pkglogger":
		return true
	default:
		return false
	}
}

func isCaseEvidenceComposite(lit *ast.CompositeLit) bool {
	switch typ := lit.Type.(type) {
	case *ast.Ident:
		return typ.Name == "CaseEvidence"
	case *ast.SelectorExpr:
		return typ.Sel.Name == "CaseEvidence"
	}
	return false
}
```

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider -run TestProviderPackagesHaveContractTests -count=1
```

Expected: FAIL until `unified`, `claudecli`, and `codexapp` have provider contract test files using the shared suite and no shallow contract cases. The manifest guard must fail fixtures that try to fabricate `contracttest.CaseEvidence{...}` or local `CaseEvidence{...}` anywhere in the provider package, dot-import `contracttest`, or call `contracttest.CompleteFixtureSpec` / bare `CompleteFixtureSpec` from any provider `.go` file, including non-test helpers such as `contract_spec.go`, to fake completeness. Skip calls are forbidden in contract-owned files (`provider_contract_test.go`, `provider_contract_helpers_test.go`, and `*_contract_test.go`) and in any package-local helper reachable from the provider contract suite. Existing e2e/smoke/platform skips remain legal only when they are not reachable from the contract suite. Add self-test fixtures proving: a contract-owned helper with `t.SkipNow` fails; `provider_contract_test.go` calling a non-contract-owned `helpers_test.go` function with `t.SkipNow` fails; an unrelated `pool_smoketest_test.go` with a platform skip is allowed; dot import of `contracttest` fails; and `CompleteCodexContractSpec() { return contracttest.CompleteFixtureSpec("codex") }` fails both in `_test.go` and non-test `.go` files.

Create explicit manifest-guard self-tests using temporary provider packages:

| Test fixture | Files written | Expected |
|---|---|---|
| `rejects_contract_owned_skip` | `provider_contract_test.go` calls `t.SkipNow` inside a contract case | fail |
| `rejects_reachable_non_owned_skip_helper` | `provider_contract_test.go` calls `skipHelper(t)` from `helpers_test.go`; helper calls `t.SkipNow` | fail |
| `rejects_reachable_method_helper` | `provider_contract_test.go` calls `suite.skip(t)`; method body calls `t.SkipNow` | fail |
| `rejects_reachable_exported_method_helper` | `provider_contract_test.go` calls `suite.Bypass(t)`; method body calls `t.SkipNow` | fail |
| `rejects_reachable_func_var_helper` | `provider_contract_test.go` calls `runCase(t)` where `runCase` is a function variable pointing to a skip helper | fail |
| `rejects_shadowed_builtin_func_var` | reachable helper uses `append := skipHelper; append(t)` | fail |
| `rejects_shadowed_builtin_param_func_var` | reachable helper takes `append func(*testing.T)` and calls `append(t)` | fail |
| `rejects_shadowed_allowed_selector` | reachable helper uses `strings := helper{}; strings.bypass(t)` | fail |
| `rejects_reachable_dependency_mode_error_var` | reachable helper declares `var e contract.DependencyModeError`, fills fields, and returns it from the provider operation captured by `CaptureUnsupportedOutcome` | fail |
| `rejects_reachable_dependency_mode_error_new` | reachable helper returns `new(contract.DependencyModeError)` or a pointer to it | fail |
| `rejects_reachable_dependency_mode_error_alias` | reachable helper declares `type local = contract.DependencyModeError` and returns `local{...}` | fail |
| `rejects_reachable_dependency_mode_error_defined_conversion` | reachable helper declares `type local contract.DependencyModeError` and returns `contract.DependencyModeError(local{...})` | fail |
| `rejects_package_level_dependency_mode_error_value` | package-level `var synthetic error = contract.DependencyModeError{...}` is returned by a captured provider operation | fail |
| `rejects_package_level_dependency_mode_error_pointer` | package-level `var synthetic = new(contract.DependencyModeError)` is returned by a captured provider operation | fail |
| `rejects_fake_event_translator_literal` | `CaptureProviderEventTranslation` receives package-local fake translator that emits copied snapshot JSON/literal instead of real translator output | fail |
| `rejects_untraversed_capture_function_args` | `CaptureProviderEventTranslation` or `CaptureUnsupportedOutcome` receives a func var, external function, or package-local function that is not queued for reachability traversal | fail |
| `rejects_external_complete_contract_spec_root` | `provider_contract_test.go` passes a dot-imported or external `Complete...ContractSpec` function to `contracttest.Run` | fail |
| `allows_real_provider_surface_helpers` | contract-owned spec calls existing provider request-builder/session fake helpers, including chained exported selectors such as `s.runtime.Start()`, that contain no forbidden constructs | pass |
| `allows_real_provider_event_translator_callback` | provider contract uses `contracttest.CaptureProviderEventTranslation` with a real package-local translator that calls its `emit` callback exactly once | pass |
| `allows_unreachable_smoke_skip` | `provider_contract_test.go` has complete evidence; `pool_smoketest_test.go` has platform `t.Skip` but is not reachable | pass |
| `rejects_dot_import_contracttest` | non-test `contract_spec.go` imports `. "internal/provider/contracttest"` | fail |
| `rejects_complete_fixture_in_test_file` | `provider_contract_test.go` returns `contracttest.CompleteFixtureSpec("codex")` | fail |
| `rejects_complete_fixture_in_non_test_file` | `contract_spec.go` returns `contracttest.CompleteFixtureSpec("codex")` and contract test calls it | fail |

- [ ] **Step 2: Add provider-specific contract tests**

Create `internal/provider/unified/provider_contract_test.go`, `internal/provider/codexapp/provider_contract_test.go`, and `internal/provider/claudecli/provider_contract_test.go`. Each file must call `contracttest.Run` with a provider-specific fixture that uses the package's existing fake transport/session helpers. If a real package cannot cheaply start without external binaries, the fixture must exercise the adapter's session object and request builders directly, not the external CLI or app-server.

Each provider must expose a `CompleteProviderContractSpec()` or `<Provider>ContractSpec()` helper and pass that helper directly to `contracttest.Run(t, ...)`. Required cases must contain real assertions through the `*contracttest.CaseEvidence` typed helpers: `RecordEventTranslation`, `RecordPromptParity`, `RecordOutcome`, `RecordResumeIdentity`, and `RecordRuntimeReport`. `RecordEventTranslation` must compare `contracttest.CaptureProviderEventTranslation(t, captureID, rawEvent, translator)` from the real translator function against `contracttest.NewExpectedEventEvidence(contracttest.LoadExpectedEventSnapshot(t, snapshotID))` from an independent `testdata/event_snapshots` golden snapshot; `captureID` and `snapshotID` must both be non-empty and different. The manifest guard must type-resolve the `translator` argument, require it to be a package-local `*types.Func`, add it to the reachability queue, and allow its `emit` callback only for the exact `func(dto.RawProviderEvent, func(any))` translator signature. The expected event snapshot API must not accept a provider-supplied decoder callback, expose the decoded expected event to provider packages, or allow provider packages to construct observed event evidence from copied snapshot JSON/literals; comparison happens on contract-owned canonical JSON bytes captured only by invoking the queued translator. `RecordPromptParity` must compare `contracttest.NewProviderPromptEvidence(captureID, capturedFields)` from the real provider request/config path against `contracttest.NewExpectedPromptEvidence(contracttest.LoadExpectedPromptSnapshot(t, snapshotID))` from an independent `testdata/prompt_snapshots` golden snapshot; `captureID` and `snapshotID` must both be non-empty and different. `RecordOutcome` must receive `contracttest.OutcomeEvidence` with either a real observed action id and final state, or an `UnsupportedOutcomeEvidence` returned by `contracttest.CaptureUnsupportedOutcome(t, operationID, dependencyName, profile, providerOperation)`; the manifest guard must type-resolve the `providerOperation` argument, require it to be a package-local `*types.Func`, and add it to the reachability queue. The captured unsupported evidence must carry a concrete `contract.DependencyModeError` found by manual unwrap, not a custom `As` implementation, and `OutcomeEvidence.ExpectedDependencyName` must match it. Arbitrary nonblank strings, boolean unsupported flags, inline `contract.NewDependencyModeError(...)`, custom `As` errors, or final-state-only records must not satisfy approval, interrupt, force-complete, or toolbridge evidence. The expected side must not accept exported `PromptParityFields` or raw event structs directly. The authoritative origin fields are unexported inside `contracttest`; `Source` / `Provenance` string labels must not be exported or trusted. Passing the same struct as both sides, copying provider evidence as expected evidence, copying captured fields/events into expected evidence with a different id, copying and changing both old `Source` and `Provenance` labels, or calling `contracttest.CompleteFixtureSpec` from a provider package is self-evidence and must fail. Generic `AssertEqual` / `AssertNoError` may record supplemental evidence only; they must not satisfy reserved required evidence keys. Provider contract-owned files and contract-reachable helpers must not construct `contracttest.CaseEvidence` directly, must not record arbitrary string evidence, must not call `t.Skip`, must not construct `contract.DependencyModeError` or call `contract.NewDependencyModeError`, must not return before an evidence helper, and must not only assert that a callback was invoked. A required case that records only tautological evidence such as `true == true`, an empty string equality, a copied event expected value, a boolean unsupported flag, or a free-form outcome detail must fail in `contracttest` self-tests.

The provider-specific `contracttest.Spec` must map each behavior to existing provider surfaces:

| Behavior | Unified provider | Codex app provider | Claude CLI provider |
|---|---|---|---|
| Event translation | Reuse `internal/provider/unified/event_map_test.go` to prove common dispatch publishes sanitized raw events and typed translated events without unsupported-event success masking. | Reuse cases from `internal/provider/codexapp/event_map_test.go`. | Reuse cases from `internal/provider/claudecli/event_map_test.go`. |
| Prompt parity | Assert unified routing/delegation preserves the selected driver's captured provider request and does not rewrite `PrefixShape.Hash`, `Boundary`, or `SectionSnapshot`. | Prove prompt snapshot, `PrefixShape.Hash`, `Boundary`, and `SectionSnapshot` survive start/resume request building. | Same parity proof against Claude request/config builders. |
| Approval | Prove approval behavior is delegated to the resolved driver and missing-driver approval paths fail with unknown-provider errors rather than no-op callbacks. | Test approval bridge callback behavior through `session_approval*_test.go` fixtures. | Test permission-mode/config policy and assert there is no outbound approval callback bridge. |
| Interrupt / force-complete | Assert unified session manager forwards interrupt and force-complete to the active provider session and records typed failures when the session/driver is missing. | Reuse deterministic session interrupt and force-complete fixtures. | Reuse deterministic session interrupt and force-complete fixtures. |
| Resume | Reuse `internal/provider/unified/session_resolver_identity_test.go` to assert `ProviderThreadID` is used and public `ThreadID` is not reinvented. | Assert `ProviderThreadID` is used and public `ThreadID` is not reinvented. | Same assertion through resume snapshot fixtures. |
| Toolbridge / proxy | Assert native-tool aggregation comes from registered driver factories and missing Codex/Claude factories do not silently fall back to an empty driver set. | Require configured toolbridge/proxy settings or `contract.NewDependencyModeError` with exact dependency/profile. | Require explicit provider-native tool governance or typed unsupported for missing bridge. |
| Runtime report | Assert unified reports the selected provider/session runtime payload and preserves provider-specific deferred or stdio/session-url fields. | Assert session URL port fields are reported through `runtime_report_session_url_test.go` fixtures. | Assert stdio runtime report fields and warn/failure semantics are explicit. |

Existing coverage to reuse or wrap includes `internal/provider/unified/event_map_test.go`, `internal/provider/unified/session_resolver_identity_test.go`, `internal/provider/claudecli/driver_resume_snapshot_test.go`, `internal/provider/codexapp/driver_session_test.go`, `internal/provider/codexapp/session_approval*_test.go`, `internal/provider/claudecli/transport_config*_test.go`, `internal/provider/codexapp/driver_toolbridge_test.go`, `internal/provider/codexapp/runtime_report_session_url_test.go`, and provider e2e MCP tests.

The prompt parity case must capture the actual provider request/config generated by Start and Resume and assert `BaseInstructions`, `DeveloperInstructions`, `PrefixShape.Hash`, `Boundary`, and `SectionSnapshot`. Checking only that `StartSession` returns a non-empty thread id is not sufficient.

- [ ] **Step 3: Run provider contract tests**

Use LSP `file(diagnostics)` for every modified provider contract test and provider helper file in Lane C. If a provider file has existing diagnostics, either fix them in the same lane or record them as blockers with exact file and line.

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/contracttest ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider -run 'ProviderContract|TestProviderPackagesHaveContractTests' -count=1
```

Expected: PASS.

### Task C3: Add Provider Scaffold Template

- [ ] **Step 1: Create template files**

Create `internal/provider/_template/README.md`:

```markdown
# Provider Adapter Template

Copy this directory to `internal/provider/<name>` and replace `template` with the provider name. A provider is mergeable only after its package has `module.go`, a driver factory registered into `group:"drivers"`, event translators, prompt snapshot request builders, and `provider_contract_test.go` passing the shared contract suite.
```

Create `internal/provider/_template/module.go.txt`:

```go
package template

import (
	"errors"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var Module = fx.Module("provider.template",
	fx.Provide(
		fx.Annotate(NewDriverFactory, fx.ResultTags(`group:"drivers"`)),
	),
	fx.Invoke(RegisterEventTranslators),
)

type driverFactoryParams struct {
	fx.In

	Reporter        contract.RuntimeReporter
	ToolbridgeProxy TemplateToolbridgeProxy
	Approvals       TemplateApprovalBridge `optional:"true"`
	Mirror          TemplateProviderMirror
	Recovery        TemplateSessionRecovery
	Tracer          TemplateTracer `optional:"true"`
	Dependency      contract.DependencyConfig
}

func NewDriverFactory(p driverFactoryParams) (contract.DriverFactory, error) {
	if err := ValidateProviderDependencies(p); err != nil {
		return contract.DriverFactory{}, err
	}
	return contract.DriverFactory{
		Name: "template",
		Create: func() contract.Driver {
			return NewDriver(DriverConfig{
				Reporter:        p.Reporter,
				ToolbridgeProxy: p.ToolbridgeProxy,
				Approvals:       p.Approvals,
				Mirror:          p.Mirror,
				Recovery:        p.Recovery,
				Tracer:          p.Tracer,
				Dependency:      p.Dependency,
			})
		},
		NativeTools: templateNativeTools(),
	}, nil
}

func ValidateProviderDependencies(p driverFactoryParams) error {
	if p.Dependency.Profile == "" {
		return errors.New("provider template dependency profile required")
	}
	if p.Dependency.Profile != contract.DependencyProfileProduction {
		return nil
	}
	required := []struct {
		name    string
		missing bool
	}{
		{name: "provider.template.runtime_reporter", missing: p.Reporter == nil},
		{name: "provider.template.toolbridge_proxy", missing: p.ToolbridgeProxy == nil},
		{name: "provider.template.mirror", missing: p.Mirror == nil},
		{name: "provider.template.session_recovery", missing: p.Recovery == nil},
	}
	for _, dep := range required {
		if dep.missing {
			return contract.NewDependencyModeError(contract.ErrUnsupportedDependencyMode, dep.name, p.Dependency.Profile)
		}
	}
	return nil
}

func templateNativeTools() []contract.NativeToolDescriptor {
	return []contract.NativeToolDescriptor{
		{ID: "template.read", Label: "provider native read", Description: "provider-native file read bypasses project file tools", DefaultDisabled: true, Provider: "template", FilterMode: contract.NativeToolFilterModeHard},
	}
}
```

`ValidateProviderDependencies` must stay in the rendered template, not only prose, and `NewDriverFactory` must call it directly before returning a `contract.DriverFactory`. Required provider dependency slots in the template must be interface or pointer types so the generated validation can nil-check them. It must fail-fast when required `driverFactoryParams` fields are nil in `production`, while non-production profiles may return typed unsupported behavior through their contract cases. If a provider does not support an approval bridge, it must provide an explicit approval-policy case in `provider_contract_test.go` rather than leaving `Approvals` nil silently.

Create `internal/provider/_template/provider_contract_test.go.txt` with a real call to `contracttest.Run` and a clear instruction to replace only the fixture constructor, not the contract assertions. The template must include provider slots for event translation, prompt parity, approval bridge or approval policy, interrupt, force-complete, resume identity, toolbridge/proxy, and runtime report hooks.

Create `internal/provider/provider_template_compile_test.go`. The test must render `_template/*.go.txt` into a temporary provider package, add only minimal generated stub definitions for `TemplateToolbridgeProxy`, `TemplateApprovalBridge`, `TemplateProviderMirror`, `TemplateSessionRecovery`, `TemplateTracer`, `NewDriver`, `DriverConfig`, and `RegisterEventTranslators`, then run `gofmt`, MCP LSP `file(diagnostics)` on the rendered `.go` files, and `go test` on the temporary package. Do not stub `ValidateProviderDependencies`; the compile test must exercise the implementation rendered from `_template/module.go.txt`. Template snippets are not covered by normal `.go` target discovery, so this test is the required compile/diagnostics bridge for `_template/module.go.txt` and `_template/provider_contract_test.go.txt`.

The same test file must include rendered-template production omission cases. For each omission below, construct the rendered template provider with `contract.DependencyProfileProduction` and assert both direct `NewDriverFactory` construction and Fx graph construction fail with a dependency-specific error:

| Omitted field | Required failure |
|---|---|
| `Reporter` | runtime reporter required |
| `ToolbridgeProxy` | toolbridge/proxy required |
| `Mirror` | provider mirror required |
| `Recovery` | session recovery required |
| `Dependency` | dependency profile required |

These negative tests must exercise the rendered template constructor, not only current `codexapp` / `claudecli` constructors.

- [ ] **Step 2: Add provider scaffold graph tests**

Extend `internal/app/modules_graph_test.go` with provider constructor/graph cases that fail when a production provider can enter the app graph without critical dependencies. Use a table that exercises current `codexapp` and `claudecli` constructor params plus the new template expectations:

```go
func TestProviderScaffoldProductionGraphRequiresCriticalDependencies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		provider string
		omit    string
		wantErr string
	}{
		{name: "codex runtime reporter", provider: "codexapp", omit: "runtime_reporter", wantErr: "runtime reporter"},
		{name: "codex toolbridge manager", provider: "codexapp", omit: "toolbridge_manager", wantErr: "toolbridge"},
		{name: "codex toolbridge pool", provider: "codexapp", omit: "toolbridge_pool", wantErr: "toolbridge"},
		{name: "codex provider mirror", provider: "codexapp", omit: "provider_mirror", wantErr: "mirror"},
		{name: "claude runtime reporter", provider: "claudecli", omit: "runtime_reporter", wantErr: "runtime reporter"},
		{name: "claude proxy addr", provider: "claudecli", omit: "proxy_addr_fn", wantErr: "proxy"},
		{name: "claude proxy token", provider: "claudecli", omit: "proxy_token_fn", wantErr: "proxy"},
		{name: "claude provider mirror", provider: "claudecli", omit: "provider_mirror", wantErr: "mirror"},
		{name: "dependency profile", provider: "codexapp", omit: "dependency_profile", wantErr: "dependency profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProviderGraphWithOmission(tc.provider, tc.omit, contract.DependencyProfileProduction)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("graph omission %s error = %v, want %q", tc.omit, err, tc.wantErr)
			}
		})
	}
}
```

The graph tests must use the real provider constructor parameter surfaces, not a fake template-only struct. Current anchors to verify before editing are `internal/provider/codexapp/module.go` parameters such as `Reporter`, `Manager`, `Pool`, and `Mirror`, and `internal/provider/claudecli/module.go` parameters such as `Reporter`, `Reg`, `ProxyAddrFn`, `ProxyTokenFn`, and `Mirror`. Missing approval bridge/policy, mirror/recovery, toolbridge/proxy functions, runtime reporter, or dependency profile must either fail-fast in production or return a typed unsupported dependency error where the provider contract explicitly allows unsupported behavior.

Run:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider -run 'ProviderScaffoldProductionGraphRequiresCriticalDependencies|ProviderContract|TestProviderPackagesHaveContractTests' -count=1
```

Expected: FAIL until provider constructors and the app graph enforce the scaffold dependency contract.

- [ ] **Step 3: Update provider documentation through the code-map workflow**

After the provider template and contract suite source files exist, update the handwritten `docs/doc/codemap/09-provider.md` section "新增 provider 如何接入" to mention:

- copy `internal/provider/_template`,
- add package `Module`,
- register `contract.DriverFactory` in `group:"drivers"`,
- declare `NativeTools` governance,
- implement provider contract tests,
- refresh the prompt parity note so it reflects current `Boundary` and `SectionSnapshot` cloning behavior,
- run the verification commands below.

Generated code-map files must not be edited by hand. Run `make codemap-check`; if it reports stale generated output, run `make codemap-refresh` and review only the generated drift from that command.

Generated project-map files must not be edited by hand. Run `make project-map-check`; if it reports stale generated output, run `make project-map-refresh` and review only the generated drift from that command.

- [ ] **Step 4: Run docs and provider checks**

Use LSP `file(diagnostics)` for `internal/provider/_template` source snippets through the rendered temporary provider package exercised by `internal/provider/provider_template_compile_test.go`. For Markdown-only template files, run `git diff --check` and the provider contract tests below.

Run:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/provider/contracttest ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider -run 'ProviderScaffoldProductionGraphRequiresCriticalDependencies|ProviderContract|TestProviderPackagesHaveContractTests' -count=1
./scripts/test_with_guard.sh ./internal/provider -run TestProviderTemplateSnippetsCompile -count=1
make codemap-check
make project-map-check
git diff --check -- internal/provider/_template internal/provider/provider_template_compile_test.go docs/doc/codemap/09-provider.md ':(glob)internal/provider/**/testdata/event_snapshots/*.json' ':(glob)internal/provider/**/testdata/prompt_snapshots/*.json'
```

Expected: PASS.

## Final Verification

Before running final checks, print the owned-file boundary:

```bash
git status --short
git diff --name-only -- docs/plans/2026-07-07-ai-maintenance-boundaries.md \
  internal/contract/dependency.go \
  internal/contract/config.go \
  internal/provider/contracttest/suite.go \
  internal/provider/contracttest/suite_test.go \
  ':(glob)internal/provider/**/testdata/event_snapshots/*.json' \
  ':(glob)internal/provider/**/testdata/prompt_snapshots/*.json' \
  internal/provider/provider_contract_manifest_test.go \
  internal/provider/unified/contract_test.go \
  internal/provider/unified/provider_contract_test.go \
  internal/provider/claudecli/module.go \
  internal/provider/claudecli/driver.go \
  internal/provider/claudecli/driver_capability_test.go \
  internal/provider/codexapp/provider_contract_test.go \
  internal/provider/codexapp/module.go \
  internal/provider/codexapp/support.go \
  internal/provider/codexapp/driver_session_test.go \
  internal/provider/codexapp/runtime_report_session_url_test.go \
  internal/provider/claudecli/provider_contract_test.go \
  internal/provider/_template/README.md \
  internal/provider/_template/module.go.txt \
  internal/provider/_template/provider_contract_test.go.txt \
  internal/provider/provider_template_compile_test.go \
  internal/platform/config/config.go \
  internal/platform/config/dependency_profile_test.go \
  internal/platform/config/config_test.go \
  cmd/mcp-lsp/runtime_test.go \
  cmd/mcp-orch/sqlite_smoke_test.go \
  internal/platform/toolbridge/module.go \
  internal/platform/toolbridge/handler.go \
  internal/platform/toolbridge/diff_gen.go \
  internal/platform/toolbridge/handler_managed_launch.go \
  internal/app/runtime_reporter_adapter.go \
  internal/app/modules.go \
  internal/app/dependency_contract.go \
  internal/app/dependency_contract_test.go \
  internal/app/thread_orchestration_adapter.go \
  internal/app/thread_orchestration_adapter_test.go \
  internal/app/toolbridge_adapters.go \
  internal/app/modules_graph_test.go \
  internal/module/thread/lifecycle.go \
  internal/module/thread/bind_session_generation_status.go \
  internal/module/thread/lifecycle_bind_session_generation_test.go \
  internal/module/thread/module.go \
  internal/module/thread/service_constructor.go \
  internal/archtest/fx_graph_test.go \
  docs/doc/codemap/09-provider.md \
  docs/doc/codemap/ai-index.json \
  docs/doc/codemap/README.md \
  docs/doc/codemap/project-map \
  frontend-app/package.json \
  frontend-app/package-lock.json \
  frontend-app/src/App.jsx \
  frontend-app/src/pages/importSurfaceGuard.test-helper.js \
  frontend-app/src/pages/pageSurfaceManifest.js \
  frontend-app/src/pages/pageSurfaceManifest.test.js \
  frontend-app/src/pages/backendApiConsumer.surface.test.js \
  frontend-app/src/pages/files/FilesPage.jsx \
  frontend-app/src/pages/files/services/filesPageService.js \
  frontend-app/src/pages/files/services/filesPageService.test.js \
  frontend-app/src/pages/memory/MemoryPage.jsx \
  frontend-app/src/pages/memory/services/memoryPageService.js \
  frontend-app/src/pages/memory/services/memoryPageService.test.js \
  frontend-app/src/pages/observability/ObservabilityPage.jsx \
  frontend-app/src/pages/observability/services/observabilityPageService.js \
  frontend-app/src/pages/observability/services/observabilityPageService.test.js \
  frontend-app/src/pages/prompts/PromptPage.jsx \
  frontend-app/src/pages/prompts/services/promptPageService.js \
  frontend-app/src/pages/prompts/services/promptPageService.test.js \
  frontend-app/src/pages/shared/pageShared.js \
  frontend-app/src/pages/shared/sharedSurfaceBoundary.test.js \
  frontend-app/src/pages/shared/pageComponents.test.jsx \
  frontend-app/src/features/prompts/PromptPageView.jsx \
  frontend-app/src/services/modules/fileService.js \
  frontend-app/src/services/modules/fileService.test.js \
  frontend-app/src/adapters/fileAdapter.js \
  frontend-app/src/adapters/fileAdapter.test.js \
  frontend-app/src/shared/api/backendApi.contractMatrix.test.js
```

The generated code-map paths (`docs/doc/codemap/ai-index.json`, `docs/doc/codemap/README.md`, `docs/doc/codemap/project-map`) are owned only when `make codemap-refresh` or `make project-map-refresh` produced drift in this lane. If those commands did not run or produced no diff, keep the generated files out of staging and record that no generated drift was present.

If any owned new file is still untracked, either add it to the index intent-to-add or check it explicitly:

```bash
git add -N <owned-new-file>
git diff --check -- <owned-new-file>
# or, for a docs-only review before staging:
git diff --no-index --check /dev/null <owned-new-file>
```

Do not report whitespace or diff hygiene as PASS while owned files are untracked and unchecked. Keep unrelated dirty files out of the implementation lane and list them separately.

Provider contract snapshot fixtures are owned artifacts, not LSP source files. Before final acceptance, prove every changed or untracked prompt/event snapshot JSON is inside the owned artifact globs and has diff hygiene:

```bash
owned_artifacts="$(mktemp)"
cat > "$owned_artifacts" <<'EOF'
internal/provider/**/testdata/event_snapshots/*.json
internal/provider/**/testdata/prompt_snapshots/*.json
EOF
{
  git diff --name-only -- ':(glob)internal/provider/**/testdata/event_snapshots/*.json' ':(glob)internal/provider/**/testdata/prompt_snapshots/*.json'
  git diff --cached --name-only -- ':(glob)internal/provider/**/testdata/event_snapshots/*.json' ':(glob)internal/provider/**/testdata/prompt_snapshots/*.json'
  git ls-files --others --exclude-standard -- ':(glob)internal/provider/**/testdata/event_snapshots/*.json' ':(glob)internal/provider/**/testdata/prompt_snapshots/*.json'
} | sort -u | tee /tmp/ai-maintenance-owned-snapshot-artifacts.txt
while IFS= read -r artifact; do
  [ -n "$artifact" ] || continue
  if git ls-files --error-unmatch "$artifact" >/dev/null 2>&1; then
    git diff --check -- "$artifact"
  else
    git diff --no-index --check /dev/null "$artifact" || test "$?" -eq 1
  fi
done < /tmp/ai-maintenance-owned-snapshot-artifacts.txt
```

Any prompt/event snapshot JSON changed or created outside those globs is unrelated dirty work unless a lane explicitly adds it to the owned artifact list. Do not stage untracked generated snapshots that are not checked in as intentional golden fixtures.

For any owned path that is already dirty before implementation, capture a baseline diff before editing and do hunk-level review before reporting completion. This is mandatory for shared metadata files such as `frontend-app/package.json` and `frontend-app/package-lock.json`, where unrelated work may already exist:

```bash
git diff -- frontend-app/package.json frontend-app/package-lock.json > /tmp/ai-maintenance-owned-baseline.diff
# after edits:
git diff -- frontend-app/package.json frontend-app/package-lock.json
```

If pre-existing hunks such as unrelated `no-silent-async-failure` scripts or tests are present, leave them unstaged or stage with `git add -p` so only the parser dependency / Lane B hunks are included. The final report must list any pre-existing dirty hunks that were preserved but not owned by this plan.

Before final acceptance, print staged proof for these dirty-owned paths:

```bash
git diff --cached -- frontend-app/package.json frontend-app/package-lock.json
git diff -- frontend-app/package.json frontend-app/package-lock.json
```

The staged diff must contain only Lane B parser dependency hunks. Any baseline hunk from `/tmp/ai-maintenance-owned-baseline.diff` that appears in `git diff --cached` is a blocker until unstaged with `git restore --staged -p` or equivalent hunk-level staging.

Build the required LSP diagnostics target list from tracked, staged, and untracked dirty source files intersected with the explicit owned source-file list. Do not diagnose or report unrelated dirty source files as plan-owned evidence.

```bash
owned_sources="$(mktemp)"
cat > "$owned_sources" <<'EOF'
internal/contract/dependency.go
internal/contract/config.go
internal/provider/contracttest/suite.go
internal/provider/contracttest/suite_test.go
internal/provider/provider_contract_manifest_test.go
internal/provider/unified/contract_test.go
internal/provider/unified/provider_contract_test.go
internal/provider/claudecli/module.go
internal/provider/claudecli/driver.go
internal/provider/claudecli/driver_capability_test.go
internal/provider/codexapp/provider_contract_test.go
internal/provider/codexapp/module.go
internal/provider/codexapp/support.go
internal/provider/codexapp/driver_session_test.go
internal/provider/codexapp/runtime_report_session_url_test.go
internal/provider/claudecli/provider_contract_test.go
internal/provider/provider_template_compile_test.go
internal/platform/config/config.go
internal/platform/config/dependency_profile_test.go
internal/platform/config/config_test.go
cmd/mcp-lsp/runtime_test.go
cmd/mcp-orch/sqlite_smoke_test.go
internal/platform/toolbridge/module.go
internal/platform/toolbridge/handler.go
internal/platform/toolbridge/diff_gen.go
internal/platform/toolbridge/handler_managed_launch.go
internal/app/runtime_reporter_adapter.go
internal/app/modules.go
internal/app/dependency_contract.go
internal/app/dependency_contract_test.go
internal/app/thread_orchestration_adapter.go
internal/app/thread_orchestration_adapter_test.go
internal/app/toolbridge_adapters.go
internal/app/modules_graph_test.go
internal/module/thread/lifecycle.go
internal/module/thread/bind_session_generation_status.go
internal/module/thread/lifecycle_bind_session_generation_test.go
internal/module/thread/module.go
internal/module/thread/service_constructor.go
internal/archtest/fx_graph_test.go
frontend-app/src/App.jsx
frontend-app/src/pages/importSurfaceGuard.test-helper.js
frontend-app/src/pages/pageSurfaceManifest.js
frontend-app/src/pages/pageSurfaceManifest.test.js
frontend-app/src/pages/backendApiConsumer.surface.test.js
frontend-app/src/pages/files/FilesPage.jsx
frontend-app/src/pages/files/services/filesPageService.js
frontend-app/src/pages/files/services/filesPageService.test.js
frontend-app/src/pages/memory/MemoryPage.jsx
frontend-app/src/pages/memory/services/memoryPageService.js
frontend-app/src/pages/memory/services/memoryPageService.test.js
frontend-app/src/pages/observability/ObservabilityPage.jsx
frontend-app/src/pages/observability/services/observabilityPageService.js
frontend-app/src/pages/observability/services/observabilityPageService.test.js
frontend-app/src/pages/prompts/PromptPage.jsx
frontend-app/src/pages/prompts/services/promptPageService.js
frontend-app/src/pages/prompts/services/promptPageService.test.js
frontend-app/src/pages/shared/pageShared.js
frontend-app/src/pages/shared/sharedSurfaceBoundary.test.js
frontend-app/src/pages/shared/pageComponents.test.jsx
frontend-app/src/features/prompts/PromptPageView.jsx
frontend-app/src/services/modules/fileService.js
frontend-app/src/services/modules/fileService.test.js
frontend-app/src/adapters/fileAdapter.js
frontend-app/src/adapters/fileAdapter.test.js
frontend-app/src/shared/api/backendApi.contractMatrix.test.js
EOF
{
  git diff --name-only
  git diff --cached --name-only
  git ls-files --others --exclude-standard
} | sort -u | rg '^(internal|cmd|frontend-app/src)/.*\.(go|js|jsx|ts|tsx)$' | rg -F -x -f "$owned_sources"
```

Every file emitted by that command must have a fresh MCP LSP `file(diagnostics)` result before claiming diagnostics PASS. If a dirty source file is omitted because it is not in `owned_sources`, list it separately as unrelated dirty work and keep it out of staging/reporting. If an owned file is untracked, diagnostics coverage is still required; `git add -N` is optional for diff hygiene, not a substitute for LSP diagnostics. Any `Error`, `Warning`, `Information`, or `Hint` severity blocks completion unless fixed or recorded as an explicit blocker with file, line, rule, and reason.

After all lanes are implemented and reviewed, run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp ./cmd/mcp-orch ./internal/app ./internal/module/thread ./internal/platform/config ./internal/platform/toolbridge ./internal/provider/contracttest ./internal/provider/unified ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider ./internal/archtest -count=1
cd frontend-app && npm run lint && npm test && npm run build
make codemap-check
make project-map-check
make guard
git diff --check
```

Also obtain MCP LSP diagnostics for every modified or newly created Go and frontend source file from the target list above. Diagnostics with severity `Error`, `Warning`, `Information`, or `Hint` must be fixed or recorded as blockers with exact file and line.

## Acceptance Criteria

- Production dependency absence is no longer silently successful. It is either a startup error or a typed unsupported/deferred behavior allowed by the current dependency profile.
- `BindSessionGeneration` no longer returns silent success when the desktop host cannot perform the bind.
- Production toolbridge dependencies are validated by the dependency-profile contract and cannot degrade into empty registries, empty bindings, nil dispatchers, or no-op stores.
- Frontend page entries do not call backend facades directly; page-owned services own request DTO shaping and have golden tests.
- `frontend-app/src/pages/backendApiConsumer.surface.test.js` no longer depends on stale hardcoded migrated-page allowlists.
- Existing shared page primitives, including retryable sync error reporting, remain covered.
- New provider packages cannot merge without a provider contract test file and the shared contract suite.
- Provider onboarding has a checked-in template that names required wiring, tests, and verification commands.
