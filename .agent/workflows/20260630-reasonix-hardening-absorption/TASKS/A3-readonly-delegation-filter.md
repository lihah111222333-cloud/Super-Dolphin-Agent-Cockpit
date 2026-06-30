---
task_id: A3-readonly-delegation-filter
owner: agent-a3
status: done
depends_on: [A1-toolpolicy-core, A0-stage-source-inventory]
---

# A3-readonly-delegation-filter

## 1. Goal

Ensure read-only subagent or planning-only delegation receives a restricted tool surface instead of relying on prompt text.

## 2. Inputs

- A0 delegation surface inventory.
- `internal/provider/toolfilter/`
- Any read-only subagent or delegation launch path identified by A0.
- A1 `toolpolicy` package.

## 3. Outputs

- Tests proving restricted surfaces exclude writers, workflow/meta tools, job/process tools, planning-state mutators, recursive agent/skill tools, connector tools, and external untrusted read-only hints.
- Minimal integration with the concrete delegation surface found by A0.

## 4. File Permissions

- RW: `internal/provider/toolfilter/presets.go`
- RW: `internal/provider/toolfilter/presets_test.go`
- RW: `cmd/mcp-orch/tools/orchestration_tool_definitions.go`
- RW: `cmd/mcp-orch/tools/orchestration_tools.go`
- RW: `cmd/mcp-orch/tools/orchestration_tools_test.go`
- RW: `internal/contract/prompt.go`
- RW: `internal/contract/provider.go`
- RW: `internal/contract/toolbridge.go`
- RW: `internal/module/thread/factory_config.go`
- RW: `internal/module/thread/resume_test.go`
- RW: `internal/module/thread/start_session.go`
- RW: `internal/module/thread/start_session_helpers.go`
- RW: `internal/module/thread/start_session_helpers_test.go`
- RW: `internal/platform/toolbridge/codex_surface_test.go`
- RW: `internal/platform/toolbridge/handler_codex_surface_store.go`
- RW: `internal/platform/toolbridge/handler_peer_decode.go`
- RW: `internal/platform/toolbridge/handler_peer_decode_helpers.go`
- RW: `internal/provider/codexapp/driver.go`
- RW: `internal/provider/codexapp/driver_pool_routing.go`
- RW: `internal/provider/codexapp/driver_session_test.go`
- RW: `internal/provider/codexapp/driver_toolsurface_contract_test.go`
- RW: `internal/provider/codexapp/native_tool_policy_validation_test.go`
- RW: `internal/provider/codexapp/support.go`
- RO: other `internal/platform/toolbridge/` files.
- NO-TOUCH: unrelated provider/session lifecycle code outside the listed A3 repair files.

Source entry point from A0:

- `internal/provider/toolfilter/presets.go:5-14`
- `internal/provider/toolfilter/presets.go:22-30`
- `internal/provider/toolfilter/presets_test.go:17-40`

## 5. Steps

1. If A0 did not identify a concrete delegation entry point, stop and mark this task `not_applicable_with_evidence`. If A0 did identify an entry point but the orchestrator did not apply exact `FILE_OWNERSHIP.tsv` paths and verification commands from A0 evidence, stop and mark this task blocked.
2. Add tests for writer exclusion.
3. Add tests for workflow/meta, job/process, and planning-state mutator exclusion, including `wait`, `bash_output`, `todo_write`, and `complete_step` if present in the surface.
4. Add tests for recursive agent/skill and connector tool exclusion, including `connect_tool_source` if present.
5. Add tests proving external untrusted read-only hints are not enough.
6. Reuse `internal/provider/toolfilter` reviewer presets as input only; make `toolpolicy` the decision owner.
7. Wire the concrete delegation surface to the restricted tool set.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh ./internal/provider/toolfilter -run 'Reviewer|Worker|FullAccess' -count=1
rg -n 'ReviewerDecision|reviewerAllowedTools|reviewerDeniedTools|shared_file_write|orchestration_launch_agent|lsp_edit|memory_write|task_|workspace_|workflow_template_|update_plan' internal/provider/toolfilter internal/platform/toolbridge cmd/mcp-orch/tools cmd/mcp-lsp
```

The orchestrator applied the exact delegation package command from A0 evidence before A3 dispatch. The original A3 branch completed at `2637e908adebffff737f9470adb84d647e017cbb`, then post-review repairs expanded the A3 surface:

- `836705200f7b4a7eca05bb93925dde4fbb9124f8` fixed read-only launch tool-surface propagation and is included before the final code verification head.
- `5f7406d992b4d2dba19408d738799b298673009a` fixed Codex native disabled-tool propagation from launch config through thread/provider startup and was merged by `0b16e06f`.
- `2803b0b5178959bc67bfac1951eb0da6b4f29099` fixed unknown non-empty `codexDisabledNativeTools` ID validation and was merged by `ae857bba`.
- `6fdba6c1a2552dd0cf21d25b0dcf43d5130faa2c` added structured `launch_agent.read_only=true` delegation input and was merged by `73fe7f12`.
- Final review2 recorded Mill PASS, Hume PASS, and Beauvoir FAIL P1: read-only launch `disallowed_tools` reached thread config but not `CodexToolSurfaceScope`, so dynamic host/MCP/skill tools could still be exposed or called through stale scoped calls.
- `d8754cc1e86bf3dfc62273390ebebf1f86b9b3fe` fixed the dynamic disabled-tool gap and was merged by `d08c6962`.
- The user required the six LSP modernization hints to be cleared.
- Einstein cleanup `dfeff4745e957ae6ee94d3f998f20c03f82ecd05` cleared `range over int`, `strings.SplitSeq`, `strings.CutPrefix`, and `slices.ContainsFunc`/`slices.Contains` hints in A3-owned files and was merged by `28aa3e54`.
- Final-review3 on `ba18c2e7` recorded Dirac FAIL P2 for missing final target HEAD documentation, Bacon FAIL P1 for scoped Codex reserved host-only calls falling back to ordinary backend when the surface was missing, and Turing FAIL P2 for missing `ba18c2e7` docs plus the remaining `history_rollout.go` `strings.Index` -> `strings.Cut` LSP hint.
- Tesla repair `e82c2300356cef51d3676b7231d77fb083ee2e47` updated `routeCodexSurfaceToolCall` so `surface == nil` first fail-fast rejects `req.Scoped && requiresCodexToolSurface(req.Name)`, then allows only non-scoped reserved host-only fallback; it also added `TestCodexToolSurfaceMissingSurfaceReservedHostOnlyDoesNotReachBackend` and changed `trimInjectedLSPHint` to `strings.Cut`.
- Integration merge `29fe8e130ed35716e8cae11698b281bd0601823d` brought `e82c2300356cef51d3676b7231d77fb083ee2e47` into the integration branch; it is now superseded by final-review4.
- Final-review4 on round6 docs sync head `4e38067ffb7c983eb58c86b57c7c1468c7e4a1b3` recorded Pasteur PASS, Popper PASS, and Meitner FAIL P1: resume runtime `codexDisabledNativeTools` malformed values were silently swallowed because `cleanResumeStringList` kept only strings from mixed arrays such as `[]any{"shell", 42}` and returned nil for object/integer values.
- Sartre repair `7dbfc068495ee91dffcf53e9fefb760058099add` made `cleanResumeStringList`, `codexDisabledNativeToolsFromRuntime`, and `resolveResumeCodexDisabledNativeTools` return errors; mixed array, object, and integer malformed runtime values fail fast with `codexDisabledNativeTools` and the offending type in the error before provider `ResumeSession`; valid `[]any` strings still trim/drop-empty/deduplicate/sort, and explicit typed `ResumeRequest.CodexDisabledNativeTools []string` still takes precedence.
- Integration merge `a09d7d2efde958c47087ec7b95d8fdcecd00357b` brought `7dbfc068495ee91dffcf53e9fefb760058099add` into the integration branch and is the latest code verification head.

Round2 verification recorded by the worker:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/codexapp ./internal/contract -run 'LaunchRequestFromExecutable|BuildStartSessionConfig|CodexNativeToolPolicy|ReadOnly' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/toolfilter ./internal/platform/toolpolicy ./internal/contract -count=1
git diff --check
```

Final P1 repair verification recorded by the controller:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/contract -run 'LaunchRequestFromExecutable|ReadOnly|NativeToolPolicy|CodexNativeToolPolicy|Tool|Provider' -count=1
./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/contract -run 'CodexToolSurface|PrepareCodexToolSurface|Disabled|Disallowed|ReadOnly' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/codexapp ./internal/platform/toolbridge ./internal/contract -run 'LaunchRequestFromExecutable|BuildStartSessionConfig|CodexToolSurface|PrepareCodexToolSurface|Disabled|Disallowed|ReadOnly' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/provider/codexapp ./internal/contract -run 'LaunchRequestFromExecutable|ReadOnly|NativeToolPolicy|CodexNativeToolPolicy' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/codexapp ./internal/contract -run 'LaunchRequestFromExecutable|BuildStartSessionConfig|CodexNativeToolPolicy|NativeToolPolicy|ReadOnly' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/toolfilter ./internal/platform/toolpolicy ./internal/contract -count=1
```

Final-review4 controller verification recorded:

```bash
# LSP diagnostics on internal/module/thread/factory_config.go,
# internal/module/thread/start_session.go,
# internal/module/thread/start_session_helpers.go,
# internal/module/thread/resume_test.go,
# cmd/mcp-orch/tools/orchestration_tools.go,
# internal/provider/codexapp/support.go,
# internal/contract/provider.go,
# internal/platform/toolbridge/handler_peer_decode.go,
# internal/platform/toolbridge/codex_surface_test.go,
# internal/provider/codexapp/history_rollout.go returned no diagnostics.
./scripts/test_with_guard.sh ./internal/module/thread -run 'Resume.*CodexDisabledNativeTools|CodexDisabledNativeTools|hydrateResume|ResumeSession' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/codexapp ./internal/contract -run 'LaunchRequestFromExecutable|BuildStartSessionConfig|CodexNativeToolPolicy|NativeToolPolicy|ReadOnly|Resume|CodexDisabledNativeTools' -count=1
make guard
git diff --check 5ccc29e69c48c407895b2d9a4182b0d124d6b813...HEAD
git diff --cached --check
python3 -m json.tool .agent/workflows/20260630-reasonix-hardening-absorption/STATE.json >/dev/null
```

Controller final gates passed on code verification head `a09d7d2efde958c47087ec7b95d8fdcecd00357b`; see `CHECKS/EVIDENCE.md`.

## 7. DoD

- [x] Restricted delegation tests cover all excluded tool classes from the source plan.
- [x] `FILE_OWNERSHIP.tsv` names every production and test file this task edits.
- [x] Prompt-only read-only enforcement is no longer the only boundary for the identified surface.
- [x] No broad provider refactor.

## 8. Rollback

Revert task-owned toolfilter and exact delegation entry changes.
