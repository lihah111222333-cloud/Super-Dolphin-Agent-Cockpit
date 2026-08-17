# Governance in Action

Super Dolphin Agent's guardrails were not designed from hypothetical failure lists. They were shaped by failures and near-misses recorded during the repository's private, AI-driven development history.

This document explains what the repository currently enforces, how a claim becomes reviewable evidence, and where the system's limits remain. For component ownership and dependency direction, read [Architecture](ARCHITECTURE.md).

## The Self-Guarding Repository Pattern

Super Dolphin separates implementation authority from acceptance authority:

- AI agents write and refactor all original product code, test code, and project-authored documentation.
- Humans own product intent, high-impact decisions, credentials, and releases.
- The repository owns deterministic acceptance rules.

Prompt instructions help an agent choose a path, but they are not the final boundary. Durable constraints live in contracts, typed registries, AST/SSA tests, generated maps, focused regression tests, and local Git gates.

```text
intent
  -> generated orientation
  -> LSP-scoped inspection
  -> narrow implementation
  -> AST / SSA / dependency guards
  -> focused tests and generated-state checks
  -> reviewable evidence
  -> accepted change
```

## Governance Layers

| Layer | Function | Failure behavior |
|---|---|---|
| Repository instructions | Route agents to current maps, contracts, tools, and verification | Missing required evidence remains a blocker |
| LSP workspace scope | Keep definitions, references, diagnostics, and edits inside the trusted checkout | Missing or stale scope fails before an edit |
| Typed contracts | Keep modules dependent on narrow ports rather than implementations | Compile, architecture, or contract tests fail |
| AST and SSA guards | Detect syntax, import, value-flow, and call-path violations | The named rule and location fail the test |
| Generated navigation | Keep file and capability maps synchronized with source | Check mode fails on drift |
| Change-aware gates | Map the current diff to required checks and evidence | Required checks remain explicit; warnings are not proof |
| Git hooks | Apply repository checks near commit and push | A failed check must be repaired or retained as a named blocker |

The backend boundary registry is a single typed source used by the evaluator and generated architecture map. Discovered bypasses are added as negative fixtures instead of being documented as exceptions.

## Evidence Standard

A result is reviewable only when it identifies:

1. the source commit or exact working-tree diff;
2. the command or tool action that produced the result;
3. the relevant rule, test, diagnostic, or generated artifact;
4. the exit status and retained failure output;
5. any skipped surface or unresolved blocker.

An agent's `done` status, an empty log, or a later green rerun does not erase earlier deterministic failures. Evidence should be narrow enough to reproduce and complete enough to prevent false closure.

## Public-Source Boundary

`release/open-source-policy.json` declares a default-deny public-source boundary. The repository currently contains policy, identity, Git-tree, and path-classification primitives. It does **not** yet claim an end-to-end public export, sealed receipt, or public CI verification workflow. Publication remains blocked until those pieces exist and pass the [Release Checklist](RELEASE_CHECKLIST.md).

## Incident and Proof Boundary

The private development history is intentionally not part of the public source export. The cases below therefore separate two kinds of evidence:

- **Historical incident** describes what happened before the public release.
- **Public proof** points to the guard, fixture, or command that remains reproducible in the exported source tree.

No benchmark result, adoption claim, or avoided-cost number is inferred from these incidents.

## Case 1: LSP Results Could Come from the Wrong Worktree

### Historical incident

The LSP sidecar once allowed a missing call-scoped CWD to fall back to a broader process or workspace root. In a multi-worktree session, that can make a valid request look successful while definitions, diagnostics, or edits are resolved against a sibling checkout.

The repair made trusted workspace scope mandatory, isolated manager pools by CWD, rejected missing scope, and added multi-project tests that place different diagnostics in different worktrees. Later hardening also rejects stale roots before a grep or edit can search a sibling worktree.

This is more dangerous than a visible tool failure: an AI agent can act confidently on correct-looking evidence from the wrong codebase.

### Public proof

- `cmd/mcp-lsp/multilsp/multi_cwd_test.go` proves worktree routing, diagnostic isolation, concurrent requests, and missing-CWD rejection.
- `cmd/mcp-lsp/tools/tool_grep_test.go` rejects stale roots and sibling-worktree fallback.
- `cmd/mcp-lsp/tools/tool_edit_support_test.go` rejects edits outside the trusted workspace, including symlink escapes.

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/multilsp \
  -run 'Test(MultiWorktree_RelativePathResolvesToCorrectModule|MultiCWD_DiagnosticsDoNotLeak|MultiCWD_StrictContextEnforcement_MissingCWD)' \
  -count=1
```

## Case 2: Missing Provider Identity Silently Routed to a Shared Server

### Historical incident

When Codex server-pool routing was enabled, a missing or malformed provider identity could silently fall through to the legacy shared app-server path. That preserved apparent availability, but it defeated the isolation requested by the caller and could route a session through the wrong home, instance, or model-provider boundary.

The repair changed opt-in pool routing to fail closed, persisted the canonical identity across start and resume, rejected conflicting identities, and moved the process spawner outside the pool mutex so a slow spawn could not block unrelated pool operations.

### Public proof

- `internal/provider/codexapp/driver_pool_routing_test.go` rejects missing, malformed, and inconsistent identities before pool acquisition.
- `internal/provider/codexapp/driver_pool_resume_test.go` requires persisted identity when resuming through the pool.
- `internal/provider/codexapp/server_pool_test.go` proves the spawner runs outside the mutex and identities remain isolated.

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp \
  -run 'Test(ResolveSessionOptionsFailsClosedOnIdentityError|ResumeSessionFailsClosedWhenPoolEnabledAndIdentityMissing|ServerPoolSpawnerRunsOutsideMutex)' \
  -count=1
```

## Case 3: A Global Default Could Create a Persistent Subagent Without Runtime Truth

### Historical incident

The persistent-subagent path had a dangerous compatibility shape: with the global default enabled, missing thread identity or missing runtime metadata could reach a fallback that appeared successful. The caller could believe it had created a recoverable agent even though the repository lacked the state required to own and resume it.

The repair promoted owner-neutral sentinel errors into `internal/contract`, removed the private fallback contract, and added an umbrella test that deliberately enables the permissive global default while deleting thread or runtime truth. Both cases must still fail closed.

### Public proof

- `internal/platform/toolbridge/p4_s3a_fail_closed_test.go` covers missing thread identity and missing runtime metadata with `PersistentSubagentDefault=true`.
- `internal/platform/toolbridge/handler.go` returns `ErrThreadRuntimeRequired` or `ErrPersistentSubagentRuntimeRequired` instead of manufacturing success.
- `internal/platform/toolbridge/proxy.go` preserves those failures across the tool protocol boundary.

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge \
  -run TestToolbridgePersistentSubagentRejectsMissingRuntime \
  -count=1
```

## Case 4: Empty Async Error Handlers Produced False UI Success

### Historical incident

Frontend workflows contained asynchronous paths where an empty `.catch(() => {})`, an empty `catch {}`, or a handler that only discarded the error could hide a failed user action. The repair added a TypeScript-AST guard, negative fixtures for each silent form, and visible error handling in the affected production paths.

The rule does not ban every fallback. A documented, intentional best-effort path remains allowed; an unexplained empty handler fails.

### Public proof

- `frontend-app/scripts/no-silent-async-failure.mjs` parses JavaScript and TypeScript syntax instead of searching raw text.
- `frontend-app/scripts/no-silent-async-failure.test.mjs` covers empty handlers, discarded error variables, comments, strings, and documented fallbacks.
- `frontend-app/package.json` wires the rule into `guard:critical-skip`, which is part of the frontend test path.

```bash
cd frontend-app
npm run guard:critical-skip
```

## Case 5: The Boundary Guard Itself Had a Type-Alias Blind Spot

### Historical incident

An early orchestration boundary guard counted direct uses of the wide `OrchestrationService` contract. A local type alias could preserve the same wide dependency while avoiding the original selector count. A regression fixture closed that bypass; the current guard has since evolved from selector counting to Go type and SSA analysis.

This matters because a guard is not trusted merely because it exists. Its bypasses become fixtures, and the guard is expected to defend its own single source of truth.

### Public proof

- `internal/archtest/orchestration_service_boundary_test.go` rejects production consumers of the wide service.
- `internal/archtest/orchestration_service_type_boundary_test.go` follows aliases and container types through Go type information.
- `internal/archtest/orchestration_service_ssa_boundary_test.go` checks propagation through function calls and values.

```bash
./scripts/test_with_guard.sh ./internal/archtest \
  -run 'TestOrchestrationService(ConsumersUseNarrowPorts|TypeConsumersUseNarrowPorts|SSAConsumersDoNotPropagateFullService)' \
  -count=1
```

## What These Cases Demonstrate

The claim is not that guardrails make AI-generated changes infallible. The repository makes a narrower claim:

1. Wrong-scope evidence must fail before an agent edits the wrong checkout.
2. Missing identity or runtime ownership must not degrade into apparent success.
3. User-visible failures must not disappear into empty handlers.
4. A discovered guard bypass must become a permanent negative fixture.
5. A completed agent run is not accepted until the relevant proof is green.

That is the Self-Guarding Repository pattern: not autonomous code generation without review, but a repository that helps AI agents detect, localize, and repair their own mistakes before those mistakes become the next layer of architecture.

## Limits

- Encoded guards only prove the invariants they check.
- Repository-specific architecture rules are not a general-purpose scanner for arbitrary projects.
- Language-server depth depends on the installed language server and workspace configuration.
- A test can contain a blind spot; discovered bypasses must become regression fixtures.
- A green local gate does not replace security review, release review, or verification of external services.
- Pre-publication incidents are maintainer reports. The public tree proves the retained guard and test, not the private historical event itself.

Current and planned work are separated in the [Roadmap](ROADMAP.md).
