# Handoff

## Current Status

Overall: done. A3 round2 commit `5f7406d992b4d2dba19408d738799b298673009a` was merged by `0b16e06f`, docs round2 commit `ade6151d28c4d6480029cfe087322ec2acb2aebf` was merged by `e17cb8b3`, R1/R2 P1 repairs were merged by `ae857bba` and `73fe7f12`, dynamic disabled-tool repair `d8754cc1e86bf3dfc62273390ebebf1f86b9b3fe` was merged by `d08c6962`, LSP hint cleanup `dfeff4745e957ae6ee94d3f998f20c03f82ecd05` was merged by `28aa3e54`, and final-review3 scoped missing-surface repair `e82c2300356cef51d3676b7231d77fb083ee2e47` was merged by `29fe8e13`. `ba18c2e7` was the previous docs sync head and final-review3 target, `29fe8e130ed35716e8cae11698b281bd0601823d` is the final code verification head, and this round6 docs commit is the final workflow sync head (`pending_this_docs_commit` inside the commit because a commit cannot reliably contain its own SHA).

## Completed

- P0 workflow structure created.
- DAG and task cards created.
- File ownership matrix created.
- Risk register and gates initialized.
- Initial source anchors recorded in evidence.
- A0 completed in `codex/reasonix-hardening-a0-20260630` at `9aa751b451fa10ddb10730138437d251c1527ab2`.
- A2 closed as `not_applicable_with_evidence` because A0 found no authoritative stage source.
- A0's A3 ownership and verification proposal was applied to `FILE_OWNERSHIP.tsv` and `TASKS/A3-readonly-delegation-filter.md`.
- A1 completed in `codex/reasonix-hardening-a1-20260630` at `5bffbcca075917b5fad03cc09ffb831c0ef5f6c1`.
- A1 initial implementation was held after independent review found unsafe `git branch` read-only classification; the worker fixed it before integration.
- A3 pre-round2 completed in `codex/reasonix-hardening-a3-20260630` at `2637e908adebffff737f9470adb84d647e017cbb`.
- A3 was later reopened by post-review findings on read-only launch/native tool propagation.
- Post-review repair `836705200f7b4a7eca05bb93925dde4fbb9124f8` is integrated in current HEAD.
- Round2 repair `5f7406d992b4d2dba19408d738799b298673009a` fixed Codex native disabled-tool propagation through orchestration launch, thread config, and provider tests; it was merged by `0b16e06f`.
- Round2 Lane A focused gates and `make guard` passed on the round2 code head before later P1 repairs.
- R2 P1 repair `2803b0b5178959bc67bfac1951eb0da6b4f29099` was merged by `ae857bba`: non-empty unknown `codexDisabledNativeTools` IDs now fail-fast across start/resume typed paths.
- R1 P1 repair `6fdba6c1a2552dd0cf21d25b0dcf43d5130faa2c` was merged by `73fe7f12`: `launch_agent.read_only=true` is the structured read-only/review/planning delegation flag; Plan/Explore compatibility remains, and ordinary workers are not made read-only.
- R3 passed with no additional code repair.
- Final review2 results: Mill PASS, Hume PASS, Beauvoir FAIL P1.
- Dynamic disabled-tool repair `d8754cc1e86bf3dfc62273390ebebf1f86b9b3fe` was merged by `d08c6962`: `CodexToolSurfaceScope` carries `DisabledTools`; Codex reads `disallowed_tools`/`disallowedTools` with fail-fast validation; toolbridge filters disabled host/skill/MCP dynamic surfaces and rejects stale disabled scoped calls before the backend.
- User required the six LSP modernization hints to be cleared.
- Einstein cleanup `dfeff4745e957ae6ee94d3f998f20c03f82ecd05` was merged by `28aa3e54`: it cleared `range over int`, `strings.SplitSeq`, `strings.CutPrefix`, and `slices.ContainsFunc`/`slices.Contains` hints in `cmd/mcp-orch/tools/orchestration_tools.go`, `cmd/mcp-orch/tools/orchestration_tools_test.go`, and `internal/contract/provider.go`.
- Final-review3 results on `ba18c2e7`: Dirac FAIL P2 because workflow docs did not record final target HEAD `ba18c2e7`; Bacon FAIL P1 because scoped Codex reserved host-only calls could fall back to ordinary host-direct backend when the surface was missing; Turing FAIL P2 because workflow docs did not record `ba18c2e7` and `internal/provider/codexapp/history_rollout.go` still had a `strings.Index` -> `strings.Cut` LSP hint.
- Tesla repair `e82c2300356cef51d3676b7231d77fb083ee2e47` was merged by `29fe8e13`: `routeCodexSurfaceToolCall` now fail-fast rejects `req.Scoped && requiresCodexToolSurface(req.Name)` when `surface == nil` before allowing non-scoped reserved host-only fallback; `TestCodexToolSurfaceMissingSurfaceReservedHostOnlyDoesNotReachBackend` proves stale scoped `memory_write` missing-surface returns a missing surface error and `host.calls == 0`; `trimInjectedLSPHint` now uses `strings.Cut`.
- B1 completed in `codex/reasonix-hardening-b1-20260630` at `2f8c85a569037f25950498dc484848b80cc942a1`.
- B1 added `internal/platform/sessionpaths`, golden tests, and the stdlib dependency guard without migrating callers.
- B2 completed in `codex/reasonix-hardening-b2-20260630` at `28e36eae9cc079daca0ffbbbd05d0836125a7d2e`.
- B2 migrated provider/util/thread callers to `sessionpaths`; Lane B focused gates and `make guard` passed after integration.
- C1 completed in `codex/reasonix-hardening-c1-20260630` at `e13c157123fd4999b21387af0583d3a2d45f13b7`.
- C1 produced `docs/adr/2026-06-30-writer-preview-contract-spike.md` only; Lane C focused gates passed after integration.
- PN final gates passed after the final-review3 P1/P2 repair on code verification head `29fe8e130ed35716e8cae11698b281bd0601823d`.

## In Progress

- None.

## Blocked

- A2 runtime blocking must remain absent until a future approved source proves a concrete planning/execution stage source.
- C1 production preview API and host-direct preview/execute tests remain deferred; the integrated C1 result is ADR-only.

## Recommended Next Action

Review this round6 docs-only status update. Controller final gates already passed on code verification head `29fe8e130ed35716e8cae11698b281bd0601823d` with:

```bash
git diff --check 5ccc29e69c48c407895b2d9a4182b0d124d6b813...HEAD
git diff --cached --check
python3 -m json.tool .agent/workflows/20260630-reasonix-hardening-absorption/STATE.json >/dev/null
# LSP diagnostics on internal/platform/toolbridge/handler_peer_decode.go,
# internal/platform/toolbridge/codex_surface_test.go,
# internal/provider/codexapp/history_rollout.go,
# cmd/mcp-orch/tools/orchestration_tools.go,
# cmd/mcp-orch/tools/orchestration_tools_test.go,
# and internal/contract/provider.go returned no diagnostics.
./scripts/test_with_guard.sh ./internal/platform/toolbridge -run TestCodexToolSurfaceMissingSurfaceReservedHostOnlyDoesNotReachBackend -count=1
./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/provider/codexapp -run 'CodexToolSurface|Scoped|HostOnly|History|Rollout|Injected|LSP' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/codexapp ./internal/platform/toolbridge ./internal/contract -run 'LaunchRequestFromExecutable|BuildStartSessionConfig|CodexToolSurface|PrepareCodexToolSurface|Disabled|Disallowed|ReadOnly|Scoped' -count=1
```

## Historical Execution Sequence

1. P0 created the workflow structure and ownership files.
2. A0 ran first, recorded `stage_source_found=false`, and proposed the concrete A3 ownership/verification paths.
3. A2 closed as `not_applicable_with_evidence`; no runtime planning-stage blocking was wired.
4. A1 landed the `toolpolicy` package and tests.
5. A3 applied the A0 delegation proposal and landed restricted read-only delegation tests.
6. Post-review A3 repairs expanded the task from toolfilter presets to orchestration launch, contract, thread startup config, Codex provider start/resume native-tool validation, dynamic host/skill/MCP disabled-tool filtering, LSP hint cleanup, and final-review3 scoped missing-surface fail-fast in the same A3-owned surfaces.
7. Lane B landed `sessionpaths` helper extraction, golden tests, and caller migration.
8. C1 produced only `docs/adr/2026-06-30-writer-preview-contract-spike.md`; no production preview contract or tests were added.

## Review Entry Points

- Start with `STATE.json` for task statuses and integrated branch/commit pointers.
- Use `CHECKS/EVIDENCE.md` for source anchors and verification logs.
- Use `CHECKS/RESULT_GATES.md` to distinguish completed gates from deferred A2/C1 production work.

## Verification Commands

Lane A:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy ./internal/provider/toolfilter ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Shell|Lifecycle|Sandbox|Permission|Reviewer|Worker|FullAccess|Native|Tool' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Tool|Provider' -count=1
make guard
```

Lane B:

```bash
./scripts/test_with_guard.sh ./internal/platform/sessionpaths ./internal/provider/codexapp ./internal/module/thread ./internal/util/historyjsonl -run 'Rollout|Scratchpad|Path|Cleanup|CodexHome|History' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Path|Provider|Thread' -count=1
make guard
```

Lane C:

```bash
rg -n 'memory_write|workflow_template_save|workflow_template_rollback|shared_file_write|defineTaskWriteTool|workspace_create_run|workspace_merge_run|workspace_abort_run|tts_generate|av_merge|video_with_audio|difftracker|Preview' internal cmd docs
./scripts/test_with_guard.sh ./internal/archtest -run 'VideoSkill|Tool|Preview|Workflow|Dependency' -count=1
```

## Ownership Reminder

Stage only files owned by the active task. Keep unrelated dirty and stash-preserved `.githooks`, older plan, and guard test modifications out of this workflow unless the user explicitly expands scope.
