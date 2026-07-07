# AI Maintenance W2 Deepening Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deepen the executed W1 AI-maintenance boundaries only where the project can harden them into code and tests now: production dependency contracts, provider scaffold acceptance, and frontend feature-owned surfaces.
**Architecture:** Add a second-layer policy/acceptance/golden-test surface on top of the W1 anchors. Lane D centralizes dependency absence policy and removes local optional/noop/fallback switches from production paths. Lane E turns provider onboarding into scaffold plus machine-verifiable acceptance. Lane F makes frontend feature services own backend DTO normalization and page import boundaries.
**Tech Stack:** Go 1.25, Fx, `internal/contract`, `internal/app`, `internal/platform/toolbridge`, `internal/provider/contracttest`, React/Vite/Vitest, TypeScript AST import parsing, repo LSP MCP.
**Verification Surface:** `./scripts/test_with_guard.sh ./internal/contract ./internal/app ./internal/module/thread ./internal/platform/toolbridge ./internal/provider ./internal/provider/contracttest ./internal/provider/unified ./internal/provider/claudecli ./internal/provider/codexapp ./internal/archtest -count=1`; `cd frontend-app && npm run lint && npm test && npm run build`; LSP diagnostics for every modified source file; `git diff --check`.

---

## Scope

This W2 plan covers only the items the controller marked as solidifiable:

1. Production optional/noop/fallback ambiguity reduction.
2. Provider scaffold plus acceptance verifier.
3. Frontend feature-owned surface deepening.

This plan does not cover AI execution-loop hardening, prompt/process orchestration changes, or hook/CI stratification. Item 5 is external by controller instruction; verify its owner, branch, status, and evidence in its own task before any implementation, and do not mix it into this W2 plan.

## Baseline Evidence

W1 already landed the following anchors:

- `internal/contract/config.go` defines `DependencyProfile`, `DependencyBootstrapMode`, `DependencyConfig`, `ErrUnsupportedDependencyMode`, `ErrDependencyDeferred`, and `DependencyModeError`.
- `internal/app/dependency_contract.go` gates app-level missing dependencies through `newDependencyContract`.
- `internal/platform/toolbridge/handler.go` validates toolbridge dependencies before constructing `Handler`.
- `internal/platform/toolbridge/dependency_contract_test.go` covers production, desktop, and test dependency behavior.
- `internal/provider/contracttest/suite.go` defines required provider contract cases and evidence keys.
- `internal/provider/provider_contract_manifest_test.go` checks provider packages for contract cases, event snapshots, prompt snapshots, event capture, and shortcut avoidance.
- `internal/provider/_template/*.txt` provides provider scaffold snippets compiled by `internal/provider/provider_template_compile_test.go`.
- `frontend-app/src/pages/pageSurfaceManifest.js`, `frontend-app/src/pages/pageSurfaceManifest.test.js`, `frontend-app/src/pages/backendApiConsumer.surface.test.js`, and `frontend-app/src/pages/shared/sharedSurfaceBoundary.test.js` enforce first-layer frontend page boundaries.

Pre-plan LSP sample: `mcp__lsp.file(action="diagnostics")` with `work_dir=/Users/mima0000/Desktop/wj/super-agent-v3` returned no diagnostics for `internal/app/dependency_contract.go`, `internal/platform/toolbridge/dependency_contract_test.go`, `internal/provider/provider_contract_manifest_test.go`, and `frontend-app/src/pages/pageSurfaceManifest.js`. New implementation must repeat diagnostics after every source edit.

## Execution Order

- Wave 1 can run Lane D and Lane F in parallel after reading the baseline files above.
- Lane E can start read-only inventory in parallel, but code changes should start after Lane D exposes the shared dependency absence policy names.
- Final integration must run the full verification surface from the plan header and inspect `git diff` against this document before merging.

## Agent Ownership Matrix

| Agent | Owns | Must Not Touch | Ordering |
|---|---|---|---|
| D agent | D1, D2, D3; `internal/contract/dependency_policy*`, app dependency contract files, `internal/module/thread` bind-generation dependency tests, toolbridge dependency tests, optional-boundary archtest | Provider acceptance files and frontend files | D1 before D2; D3 after D1 policy names exist |
| E agent | E1, E2, E3; `internal/provider/contracttest`, `internal/provider/_template`, provider manifest tests and provider contract tests | App/toolbridge dependency policy internals except using exported policy names | E1 can start after D1 exported policy names are available; E2/E3 stay in one agent to avoid manifest/template drift |
| F agent | F1, F2, F3; `frontend-app/src/pages/pageSurfaceManifest.js`, page surface tests, feature service DTO tests | Go provider/app/toolbridge files | F1 before F2/F3; F1 and F3 must stay in the same agent because both edit `backendApiConsumer.surface.test.js` |

Do not split tasks that touch the same file across separate agents unless the controller creates a serial handoff and rechecks the full diff.

## Shared Rules

- Keep changes implementation-focused. Do not rewrite W1 docs except when a code-linked acceptance path needs a one-line pointer.
- No silent defaults, noop success, broad fallback, or best-effort production behavior.
- Every allowed missing dependency must have a named profile, named dependency key, owner, reason, and test.
- Every provider acceptance criterion must be backed by executable tests or golden snapshots, not prose.
- Every frontend page entry must talk to backend APIs through its own feature service or adapter surface.
- Do not stage unrelated dirty files or generated outputs.
- Each agent must record `git status --short --untracked-files=all` before starting and before handoff.
- Each agent must record both `git diff --name-only` and `git ls-files --others --exclude-standard` before handoff. If staged, unstaged, or untracked output includes an unowned file, stop and report drift.
- Do not use `git add .`. Stage only explicit owned files when the controller requests a commit.
- Until this plan is committed, `docs/plans/2026-07-08-ai-maintenance-w2-deepening.md` is controller-owned. Implementation agents must not edit it unless explicitly assigned a documentation correction. Do not dispatch implementation agents from this plan while the plan file is untracked unless the controller explicitly records that exception.

---

## Lane D: Production Dependency Contract Deepening

**Goal:** Move allowed-missing dependency knowledge out of local switches and into a shared policy that production paths can test, audit, and fail fast against.

### D1. Add A Shared Dependency Absence Policy

Files:

- `internal/contract/dependency_policy.go`
- `internal/contract/dependency_policy_test.go`
- `internal/contract/config.go`

RED:

- Add tests before implementation:
  - `TestDependencyAbsencePolicyRejectsProductionMissingDependencies`
  - `TestDependencyAbsencePolicyAllowsOnlyNamedDesktopDependencies`
  - `TestDependencyAbsencePolicyAllowsOnlyNamedTestDependencies`
  - `TestDependencyAbsencePolicyRejectsUnknownDependency`
  - `TestDependencyAbsencePolicyRejectsEmptyProfile`

Implementation shape:

```go
package contract

type DependencyAbsenceReason string

const (
	DependencyAbsenceDesktopExternal DependencyAbsenceReason = "desktop_external"
	DependencyAbsenceTestHarness     DependencyAbsenceReason = "test_harness"
)

type DependencyAbsencePolicy struct {
	Name     string
	Profile  DependencyProfile
	Reason   DependencyAbsenceReason
	Owner    string
	Error    error
}

func RegisteredDependencyAbsencePolicies() []DependencyAbsencePolicy
func AllowsMissingDependency(name string, profile DependencyProfile) bool
func MissingDependencyModeError(name string, profile DependencyProfile) error
```

Initial policy names must include all W1 allowed absences and no extra production absences:

- `runtime_reporter.orchestration_service` for `desktop_host` and `test`, reason `desktop_external`.
- `toolbridge.agent_thread_lookup` for `desktop_host`, reason `desktop_external`.
- `toolbridge.thread_config_override_store` for `desktop_host`, reason `desktop_external`.
- `toolbridge.lifecycle_backfiller` for `test`, reason `test_harness`.
- `toolbridge.skill_tools` for `test`, reason `test_harness`.
- `thread.bind_session_generation` for `desktop_host` and `test`, reason `desktop_external`.

GREEN:

- `dependency_policy.go` exposes the policy API and contains no provider-, app-, or frontend-specific implementation imports.
- The tests prove production has zero allowed missing dependencies.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/contract -run 'TestDependencyAbsencePolicy' -count=1
```

Definition of done:

- Production profile never returns allowed missing.
- Unknown dependency names never return allowed missing.
- Empty profile returns a hard error, not a typed unsupported/deferred success path.

### D2. Replace Local Allowed-Missing Switches With Shared Policy

Files:

- `internal/app/dependency_contract.go`
- `internal/app/dependency_contract_test.go`
- `internal/app/runtime_reporter_adapter.go`
- `internal/app/thread_orchestration_adapter.go`
- `internal/app/thread_orchestration_adapter_test.go`
- `internal/module/thread/lifecycle.go`
- `internal/module/thread/lifecycle_bind_session_generation_test.go`
- `internal/platform/toolbridge/handler.go`
- `internal/platform/toolbridge/dependency_contract_test.go`

RED:

- Extend `internal/app/dependency_contract_test.go` so desktop/test behavior is asserted through the shared policy names.
- Extend `internal/app/thread_orchestration_adapter_test.go` and `internal/module/thread/lifecycle_bind_session_generation_test.go` so `thread.bind_session_generation` uses the shared policy for desktop/test typed unsupported behavior and still hard-fails in production.
- Extend `internal/platform/toolbridge/dependency_contract_test.go` so the allowed desktop/test dependencies are read from the shared policy and any extra local allowance fails.

GREEN:

- `dependencyContract.allowsMissing` must be removed or reduced to a call into `contract.AllowsMissingDependency`.
- `toolbridgeMissingDependencyError` must use the shared policy for typed unsupported behavior.
- Production missing dependency errors must remain ordinary hard failures with the dependency name and profile in the message.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/contract ./internal/app ./internal/module/thread ./internal/platform/toolbridge -run 'TestDependency|TestToolbridge.*Dependency|TestRuntimeReporter|TestBindSessionGeneration|TestThreadLifecycle.*BindSessionGeneration' -count=1
```

Definition of done:

- App and toolbridge no longer maintain separate allowlists for the same dependency names.
- A new allowed desktop/test absence requires editing `internal/contract/dependency_policy.go` and adding tests.
- Production profile cannot accidentally inherit a desktop/test allowance.

### D3. Add An Optional Dependency Boundary Archtest

Files:

- `internal/archtest/dependency_optional_boundary_test.go`

RED:

- Add an archtest that scans Go files under:
- `internal/app`
- `internal/module/thread`
- `internal/platform/toolbridge`
- `internal/provider`

The scan must inventory and classify:

- `fx.Optional`
- struct tags containing `optional:"true"`
- dependency constructor functions returning success after building a known noop/deferred implementation without using `contract.MissingDependencyModeError`

Do not implement this as a blanket ban on every existing optional tag. Classify each match as one of:

- `dependency_absence`: a production-relevant dependency whose missing behavior must route through `DependencyAbsencePolicy`.
- `adjunct_optional`: logger, tracer, UI lifecycle, or auxiliary capability that is optional but cannot make the production main path silently succeed with missing critical behavior.
- `test_or_template`: test fixture or provider template snippet whose production omission behavior is verified elsewhere.

Allowlist format:

```go
type optionalDependencyAnchor struct {
	Path     string
	Snippet  string
	Class    string
	Owner    string
	Evidence string
}

var allowedOptionalDependencyAnchors = []optionalDependencyAnchor{
	{Path: "internal/app/runtime_reporter_adapter.go", Snippet: "Service    contract.OrchestrationService `optional:\"true\"`", Class: "dependency_absence", Owner: "Lane D", Evidence: "runtime_reporter.orchestration_service"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "BindingStore    agentThreadLookup         `optional:\"true\"`", Class: "dependency_absence", Owner: "Lane D", Evidence: "toolbridge.agent_thread_lookup"},
	{Path: "internal/provider/_template/module.go.txt", Snippet: "Tracer          TemplateTracer `optional:\"true\"`", Class: "test_or_template", Owner: "Lane E", Evidence: "TestRenderedTemplateProductionOmissions"},
}
```

GREEN:

- Real Go source entries classified as `dependency_absence` must be tied to a registered `DependencyAbsencePolicy`.
- Real Go source entries classified as `adjunct_optional` must name an owner and a test or constructor that proves the optional value does not create a silent production success path.
- Template `.txt` entries may be allowed only when `internal/provider/provider_template_compile_test.go` also proves production omissions fail.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run 'TestOptionalDependencyBoundary' -count=1
```

Definition of done:

- A future AI edit adding `optional:"true"` to a production Fx param fails locally before review.
- Existing intentional template optional fields remain explainable and tested.

---

## Lane E: Provider Scaffold Acceptance Verifier

**Goal:** Make “new provider” a fixed scaffold plus executable acceptance checklist. A provider is not accepted because it compiles; it is accepted when event translation, prompt parity, approval, interrupt, resume, toolbridge, and runtime-report evidence pass.

### E1. Formalize Provider Acceptance Criteria In contracttest

Files:

- `internal/provider/contracttest/acceptance.go`
- `internal/provider/contracttest/acceptance_test.go`
- `internal/provider/contracttest/suite.go`
- `internal/provider/contracttest/suite_test.go`

RED:

- Add tests proving `ValidateAcceptanceSpec` fails when any criterion is missing:
  - `event_translation`
  - `prompt_snapshot_parity` or `prompt_materialized_carrier`
  - `approval`
  - `interrupt`
  - `force_complete`
  - `resume`
  - `toolbridge`
  - `runtime_report`

Implementation shape:

```go
package contracttest

type AcceptanceCriterion = CaseKey

const (
	AcceptanceEventTranslation     AcceptanceCriterion = CaseEventMatrix
	AcceptancePromptSnapshotParity AcceptanceCriterion = CasePromptParity
	AcceptancePromptMaterialized   AcceptanceCriterion = CasePromptMaterializedCarrier
	AcceptanceApproval             AcceptanceCriterion = CaseApproval
	AcceptanceInterrupt            AcceptanceCriterion = CaseInterrupt
	AcceptanceForceComplete        AcceptanceCriterion = CaseForceComplete
	AcceptanceResume               AcceptanceCriterion = CaseResume
	AcceptanceToolbridge           AcceptanceCriterion = CaseToolbridge
	AcceptanceRuntimeReport        AcceptanceCriterion = CaseRuntimeReport
)

func RequiredAcceptanceCriteria(spec Spec) []AcceptanceCriterion
func ValidateAcceptanceSpec(spec Spec) error
```

GREEN:

- `ValidateAcceptanceSpec` must be a projection over existing `CaseKey` / `RequiredCases`; do not create a parallel required-case registry that can drift from `ValidateSpec`.
- `RunSpecForTest` must call `ValidateAcceptanceSpec` before running provider behavior.
- Existing provider tests still pass without changing their evidence semantics.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/provider/contracttest -run 'Test.*Acceptance|TestRunSpecForTest' -count=1
```

Definition of done:

- Missing acceptance criteria fail before a provider starts running behavior tests.
- Prompt parity alternative remains explicit: either snapshot parity or materialized carrier, not both required.

### E2. Turn The Provider Template Into An Acceptance Scaffold

Files:

- `internal/provider/_template/README.md`
- `internal/provider/_template/module.go.txt`
- `internal/provider/_template/provider_contract_test.go.txt`
- `internal/provider/provider_template_compile_test.go`

RED:

- Extend the rendered template tests with:
  - `TestRenderedTemplateAcceptancePlaceholdersFail`
  - `TestRenderedTemplateAcceptanceCriteriaDeclared`
  - `TestRenderedTemplateProductionOmissions`

GREEN:

- The rendered template must compile.
- The rendered template must expose the same acceptance criteria as real providers.
- Placeholder contract cases must fail with actionable messages until replaced by provider-specific capture helpers.
- `module.go.txt` must keep production omissions fail-fast for runtime reporter, toolbridge proxy, provider mirror, session recovery, and dependency profile.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/provider -run 'TestProviderTemplateSnippetsCompile|TestProviderPackagesHaveContractTests' -count=1
```

Definition of done:

- A copied provider scaffold cannot pass acceptance by leaving placeholder cases in place.
- A copied provider scaffold cannot enter the production Fx graph with missing critical dependencies.

### E3. Add A Provider Acceptance Manifest Test

Files:

- `internal/provider/provider_acceptance_manifest_test.go`
- `internal/provider/provider_contract_manifest_test.go`
- `internal/provider/unified/provider_contract_test.go`
- `internal/provider/claudecli/provider_contract_test.go`
- `internal/provider/codexapp/provider_contract_test.go`

RED:

- Add a manifest test that discovers provider packages from `fx.Module("provider.<name>", provider module options)` and requires:
  - `provider_contract_test.go` exists.
  - `contracttest.Run(t, Complete<Provider>ContractSpec())` is present.
  - `contracttest.LoadExpectedEventSnapshot` is used.
  - `contracttest.LoadExpectedPromptSnapshot` is used.
  - `contracttest.CaptureProviderEventTranslation` receives a provider-local translator.
  - `contracttest.ValidateAcceptanceSpec` or `contracttest.Run` covers acceptance criteria.
  - `testdata/event_snapshots` and `testdata/prompt_snapshots` contain at least one `.json` snapshot each.

GREEN:

- Keep AST parsing. Do not replace this with raw text grep.
- Real provider packages must pass without special casing `unified`, `claudecli`, or `codexapp`.
- Non-provider helper packages stay excluded only through `isNonProviderPackage`.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/provider ./internal/provider/unified ./internal/provider/claudecli ./internal/provider/codexapp -run 'TestProvider.*Manifest|Test.*ProviderContract' -count=1
```

Definition of done:

- Adding a provider without snapshots, event translator capture, or full acceptance criteria fails in `internal/provider`.
- Provider scaffold, real providers, and manifest tests all enforce the same acceptance language.

---

## Lane F: Frontend Feature-Owned Surface Deepening

**Goal:** Make each frontend feature own the DTO normalization between page state and backend API, so future AI UI edits do not touch `App.jsx`, shared API facades, or unrelated pages.

### F1. Enrich The Page Surface Manifest

Files:

- `frontend-app/src/pages/pageSurfaceManifest.js`
- `frontend-app/src/pages/pageSurfaceManifest.test.js`
- `frontend-app/src/pages/backendApiConsumer.surface.test.js`

RED:

- Extend `pageSurfaceManifest` entries to declare:
  - `entry`
  - `servicePrefix`
  - `adapterPrefix`
  - `serviceEntry`
  - `ownershipMode`
  - `dtoGoldenTest` when `ownershipMode` is `dto-golden`
  - `ownedStateFiles`

Example shape:

```js
files: {
  entry: 'pages/files/FilesPage.jsx',
  servicePrefix: 'pages/files/services/',
  adapterPrefix: 'pages/files/adapters/',
  serviceEntry: 'pages/files/services/filesPageService.js',
  ownershipMode: 'dto-golden',
  dtoGoldenTest: 'pages/files/services/filesPageService.test.js',
  ownedStateFiles: ['pages/files/FilesPage.jsx'],
}
```

GREEN:

- `pageSurfaceManifest.test.js` must fail when a feature lacks `serviceEntry`, `ownershipMode`, or `ownedStateFiles`.
- `pageSurfaceManifest.test.js` must fail when an entry with `ownershipMode: 'dto-golden'` lacks `dtoGoldenTest`.
- Entries with `ownershipMode: 'service-boundary'` are not allowed to add placeholder DTO golden tests; they must be covered by import-boundary tests until a real DTO contract is added.
- `backendApiConsumer.surface.test.js` must treat `serviceEntry` as the only place that may import `shared/api/backendApi.js` or `services/modules/*` for that feature.
- Page entries may import their own services/adapters and shared UI primitives, but not raw backend facades or another feature service.

Verification:

```bash
cd frontend-app
npm exec vitest -- run --no-file-parallelism --maxWorkers=1 src/pages/pageSurfaceManifest.test.js src/pages/backendApiConsumer.surface.test.js
```

Definition of done:

- The manifest names the owner surface and ownership mode for every page entry.
- Adding a backend API import to a page component fails even if the import is dynamic, re-exported, or hidden behind a computed Vitest mock.

### F2. Add Shared DTO Golden Harness For Feature Services

Files:

- `frontend-app/src/pages/shared/featureDtoGolden.test.js`
- `frontend-app/src/pages/files/services/filesPageService.test.js`
- `frontend-app/src/pages/memory/services/memoryPageService.test.js`
- `frontend-app/src/pages/observability/services/observabilityPageService.test.js`
- `frontend-app/src/pages/prompts/services/promptPageService.test.js`

RED:

- Create a shared test helper inside `featureDtoGolden.test.js` that can:
  - import each feature service factory,
  - inject a fake backend API,
  - capture outbound payloads,
  - assert normalized DTOs,
  - assert missing required fields throw synchronously before backend calls.

Initial W2 golden DTO scope is limited to features that already have meaningful DTO normalization seams. Do not create placeholder golden tests for `chat`, `settings`, `skills`, or `workflows`; those entries stay `service-boundary` unless this implementation adds real DTO normalization tests for them.

Initial golden DTO cases:

- `createFilesPageService().saveTextFile` trims `defaultFilename`, requires `content`, and preserves explicit `defaultPath`.
- `createMemoryPageService().upsertMemoryEntry` trims `cwd`, `target`, `existingPath`, `name`, `description`, `type`, and `content`.
- `createMemoryPageService().mergeMemoryEntries` rejects identical source and target identity.
- `createObservabilityPageService().listObservabilityRecent` normalizes limit and rejects unsupported limits.
- `createPromptPageService().draftPromptIntent` requires `cwd`, `kind`, and `rawInput`.
- `createPromptPageService().writePrompt` requires either `id` or `key`.

GREEN:

- Keep feature-local service tests focused on service behavior.
- The shared golden harness may import service factories, but page components must not import the harness or backend facades.
- Do not snapshot React DOM for DTO contracts; snapshot payload objects and thrown error messages.

Verification:

```bash
cd frontend-app
npm exec vitest -- run --no-file-parallelism --maxWorkers=1 src/pages/shared/featureDtoGolden.test.js src/pages/files/services/filesPageService.test.js src/pages/memory/services/memoryPageService.test.js src/pages/observability/services/observabilityPageService.test.js src/pages/prompts/services/promptPageService.test.js
```

Definition of done:

- Service DTO changes produce small, localized golden diffs.
- Page UI changes do not need to touch backend facade imports.
- Missing DTO fields fail before backend calls.

### F3. Close Cross-Feature And Shared State Escape Hatches

Files:

- `frontend-app/src/pages/backendApiConsumer.surface.test.js`
- `frontend-app/src/pages/shared/sharedSurfaceBoundary.test.js`
- `frontend-app/src/App.jsx`
- `frontend-app/src/pages/shared/pageShared.js`
- Feature service files named by `pageSurfaceManifest.js`

RED:

- Extend guards so:
  - `App.jsx` may import page-feature services only when they are explicitly listed by the manifest.
  - `App.jsx` app-shell backend API imports, such as update and sidebar state calls, must be listed in a small app-shell allowlist inside `sharedSurfaceBoundary.test.js` until they are moved behind an app-shell service.
  - `pages/shared/pageShared.js` may not import `services/modules/*`.
  - a feature page may not import another feature's `services/` or `adapters/`.
  - shared page components may not import backend facades.

GREEN:

- Existing memory badge and prompt feature view must remain behind their page-owned services.
- The guard must not force app-shell backend APIs into `pageSurfaceManifest`; page entries and app-shell services are separate ownership surfaces.
- New guard tests must use `parseStaticImports` / `importSpecifiers`; do not add brittle regex-only parsing for imports.

Verification:

```bash
cd frontend-app
npm exec vitest -- run --no-file-parallelism --maxWorkers=1 src/pages/shared/sharedSurfaceBoundary.test.js src/pages/backendApiConsumer.surface.test.js
npm run lint
npm test
npm run build
```

Definition of done:

- `App.jsx` app-shell imports, shared page utilities, and feature pages have distinct import rights.
- A future AI edit touching one feature cannot accidentally couple another page to its service or backend facade.

---

## Final Integration Gate

Run these commands from the repository root unless a command changes directory explicitly:

```bash
./scripts/test_with_guard.sh ./internal/contract ./internal/app ./internal/module/thread ./internal/platform/toolbridge ./internal/provider ./internal/provider/contracttest ./internal/provider/unified ./internal/provider/claudecli ./internal/provider/codexapp ./internal/archtest -count=1
cd frontend-app && npm run lint && npm test && npm run build
git diff --check
```

Required LSP evidence before merge:

- `mcp__lsp.grep` for the changed dependency policy symbols, provider acceptance symbols, and page surface manifest.
- `mcp__lsp.inspect` for at least one changed exported Go symbol per lane.
- `mcp__lsp.xref` for changed Go symbols that are consumed outside their package.
- `mcp__lsp.file(action="read_file")` for the changed source ranges after definition/xref confirms the target.
- `mcp__lsp.file(action="diagnostics")` for every changed Go and frontend source file.

Merge criteria:

- All W2 tests are green.
- Diff matches only Lane D/E/F scope.
- No production path gains a new unregistered optional/noop/fallback behavior.
- No provider can pass with missing acceptance evidence.
- No page component imports raw backend API or another feature's service surface.
