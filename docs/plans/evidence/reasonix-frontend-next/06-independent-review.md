# Reasonix Frontend Next — Serial Independent Review

## Execution identity

- Execution HEAD: `8db4361fcf2c48002699da40b09200a8197b894e`
- Live `origin/main`: `7c3af8c390cce5157fe2824043165fe5d6dc9bba`
- Execution base: `a7df089e32e4135a90f10a52f6ef10069cab8353`
- Branch/worktree: `codex/reasonix-frontend-next-serial` at `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial`
- Commit order: `59ed17f9a75495c153d6ec54fd32f2fe7b48e204`, `8c299a6c69a0f30b9571d8ae3064708d870f4363`, `e8e6ce864f63717ecc227b6bfd7c33268a07fe49`, `10bf5e54144085a1db02380d54c1900dc446dd7e`, `d48beefa79d7ce27614d5b5f651b68fd3e078a2d`, `1fdee79f084862a8ba46ccee528134a0ce9e1acc`, `ca9fbc7e7d0a30587a341f21ed3b8f86b805a9cd`, `8db4361fcf2c48002699da40b09200a8197b894e`.
- Serial implementer handoff: Tasks 0-7 predecessor implementer -> Task 8 first replacement implementer (Surface A/B repair and C review) -> Task 8 second replacement implementer (C evidence, adjudicated C repair, and full gates). The first replacement's execution channel became unresponsive; root stopped it before starting the second. No parallel implementer/session, cleanup, or lost worktree file is claimed. Root independently adjudicates each surface.

## Review Surface A — command / shortcut / palette / settings

### Coverage

The replacement implementer reviewed live commits/diffs and used LSP locate, inspect, xref, exact read, and diagnostics across command registry/runtime, dispatcher, App composition, palette, shortcut Settings, i18n, and `uistate` preference validation. Applicable dimensions: D01, D02, D04, D09, D10, D12, D14, D15, D16, D17, D18, and D19. Provider/runtime, store SQL, orchestration, release packaging, and Surface B/C behavior were not part of A.

Focused verification:

- Frontend final: exit `0`, 13 files / 356 tests.
- Go final: exit `0`, `internal/module/uistate` focused `Shortcut|Preference` tests.
- LSP final: no diagnostics, including no Hint.
- `git diff --check`: exit `0`.
- Root independent frontend verification: exit `0`, 2/2 files / 226/226 tests, `18.84s`.
- Root independent Go verification: exit `0`, focused package test, `0.037s`.
- Root LSP verification: six modified code/test files, zero diagnostics.

Full RED and rerun history is retained in `01-command.md`; no inconclusive or failed run is converted into PASS.

### Finding dispositions

| Severity / dimension | Location | Finding | Root adjudication |
|---|---|---|---|
| P2 / D02,D10,D14 | `internal/module/uistate/preferences.go:325-334`; `frontend-app/src/app/commands/appCommandRegistry.js:55-60` | Planned command ID length was not bounded. | `CLOSED/FIXED` — 1..128 Unicode code points; 128 accepted, 129 rejected in both layers; error does not echo the overlong ID. |
| P2 / D09,D15 | `frontend-app/src/App.jsx:549-574` | English palette could display a Chinese interrupt disabled reason. | `CLOSED/FIXED` — current locale copy is injected and English integration test passes. |
| P3 / D04,D12 | `internal/module/uistate/preferences.go:451` | LSP `mapsloop` Hint. | `CLOSED/FIXED` — `maps.Copy`, diagnostics empty. |
| P3 / D04,D12 | `internal/module/uistate/model_providers_test.go:78` | LSP `rangeint` Hint. | `CLOSED/FIXED` — range-over-int, diagnostics empty. |

Root adjudication for Surface A is `CLOSED/FIXED`. Surfaces B and C were subsequently adjudicated, repaired where authorized, and independently rerun; no review surface remains open.

## Review Surface B — message revision / prompt history / exact cwd

### Coverage and focused verification

The replacement implementer completed live LSP locate/inspect/xref/read/diagnostics across the Task 4/5 backend and frontend chain. The initial review returned zero diagnostics across 32 modified Go/JS/JSX source and test files. After repair, the 18 currently modified source/test files produced one `forvar` Hint for an unnecessary loop-variable copy; it was removed and the full batch then returned zero diagnostics.

- Frontend focused: exit `0`, 6/6 files and 131/131 tests, `3.21s`.
- Go focused: exit `0`, all six packages passed.
- Frontend repair RED: exit `1`, 3 failed files, 9 failed / 44 passed / 53 total.
- Go repair RED: B1 emitted a 2,688-byte wire cursor without error; B3 validated a resolved cursor against the target file and swallowed a fallback read error.
- Frontend final focused: exit `0`, 6/6 files and 139/139 tests, `4.28s`.
- Go final focused: exit `0`, all six packages passed.
- Surface A regression: frontend App + registry 2/2 files and 226/226 tests, exit `0`, `19.01s`; Go uistate focused test exit `0`, `0.024s`.
- Root independent post-fix: frontend hook + Composer + response validator 3/3 files and 53/53 tests, exit `0`, `2.15s`; Go prompthistory + claudecli exit `0`, `0.007s` / `2.140s`; 10 Surface B modified files returned zero LSP diagnostics.
- Full commands, package timings, and exact evidence are retained in `02-prompt-history.md`.

### Findings

| Severity / dimension | Location | Finding | Root adjudication |
|---|---|---|---|
| P1 / D02,D09,D10,D12 | `internal/module/thread/prompthistory/scanner.go:399-439`; `frontend-app/src/shared/api/backendResponseValidators.js:301-320` | Cursor cap was applied to decoded/raw JSON rather than the base64url wire value, so a server-generated cursor could exceed the request cap and become unusable. | `CLOSED/FIXED` — encoded wire is capped on server encode/decode; response cursor and nonce use UTF-8 byte caps; boundary tests pass. |
| P1 / D08,D09,D15 | `frontend-app/src/features/prompt-history/hooks/usePromptHistory.js:17-31`; `frontend-app/src/pages/chat/composer/ComposerDock.jsx:122` | Same-CWD create/delete/archive/rename had no controller invalidation signal; plan's empty-composer sentence contradicted the retained unfinished-draft sentinel. | `CLOSED/FIXED` — existing `store?.threads` reference is a loss-only invalidation signal; no second truth source; create/delete/archive/rename and draft-boundary tests pass. |
| P1 / D02,D05,D12 | `internal/provider/claudecli/session_history.go:57-235` | Claude target-to-resolved fallback could emit a cursor from one file then validate it against another, and fallback errors could be swallowed. | `CLOSED/FIXED` — bounded/private resolved-source wrapper preserves pagination source, strict decoder fails fast, filtered-empty `HasMore` is retained, and fallback errors propagate. |

Root adjudicated B1-B3 as `FIX_REQUIRED`; the authorized repairs are complete and root's independent post-fix verification passed. Full RED, GREEN, retry, and diagnostics history is retained in `02-prompt-history.md`.

## Review Surface C — trace / performance / benchmark

### Coverage and focused verification

The first replacement implementer completed live LSP locate/inspect/xref/read/diagnostics across the performance monitor, `main.jsx` observer adapter, trace bridge contract, error-boundary integration, deterministic fixture, benchmark runner, and timeline materialization model/hook. Sixteen Surface C source/test/config files returned zero diagnostics. The second replacement implementer preserves those live findings and records the root adjudication; it does not claim to have independently repeated the completed C review.

- Performance pre-repair focused: exit `0`, 3/3 files and 71/71 tests, `1.89s`.
- C1 RED: exit `1`, 2/2 files failed, 3 failed / 21 passed / 24 total, `837ms`.
- C1 immediate GREEN: exit `0`, 2/2 files and 24/24 tests, `840ms`.
- Performance final focused: exit `0`, 3/3 files and 74/74 tests, `1.65s`.
- Root independent C verification: exit `0`, monitor + AppErrorBoundary 2/2 files and 24/24 tests, `914ms`; four modified C source/test files returned zero diagnostics.
- Benchmark/model focused: exit `0`, 4/4 files and 21/21 tests, `1.69s`.
- Default benchmark: one JSON array with exactly six rows for `200/1000/5000 × toolsPerTurn 1/3`; every row has the exact eight planned keys, `materializedCount=80`, finite measurement fields, and no fixture content.
- Extended benchmark: exactly eight rows; only the final two cases append `10000 × toolsPerTurn 1/3`.
- LSP final: 16 Surface C source/test/config files, zero diagnostics.

### Findings

| Severity / dimension | Location | Finding | Root adjudication |
|---|---|---|---|
| P1 / D02,D09,D12,D14 | `frontend-app/src/shared/diagnostics/frontendPerformancePressure.js:225-298`; `frontend-app/src/main.jsx:118-139` | Startup was not transactional. If focus subscription or timer scheduling threw after prior resources were acquired, the function rethrew without disconnecting the observer or removing already-installed listeners. `PerformanceObserver.observe()` could likewise throw after construction without disconnecting the observer. Root's deterministic probe observed `{"error":"focus subscribe failed","disconnected":0,"listeners":1}`. | `CLOSED/FIXED` — test-first rollback cases failed 3/24, then the shared idempotent cleanup path removed timer/listeners/observer and preserved the original initialization error; the main adapter disconnects on `observe()` failure. Final focused 74/74, four modified files LSP-zero, and diff-check exit `0`. |
| Candidate / D02,D09,D11,D19 | canonical trace phase/status/metadata sanitizer | Add a phase-specific sanitizer for performance events. | `REJECTED_WITH_EVIDENCE` — the sole producer emits an exact bounded payload and the real bridge tests prove all four performance phases survive canonical ingest. Adding a second phase-specific sanitizer would create duplicate policy rather than close a reachable gap. |

Detailed Surface C RED/GREEN and benchmark evidence is retained in `03-performance.md` and `04-benchmark.md`. Root's Surface C adjudication is complete: C1 is `CLOSED/FIXED`, and the phase-specific sanitizer candidate is `REJECTED_WITH_EVIDENCE`. The later full-lint repair removed `unsafe-finally`, added a secondary-cleanup failure identity test, and passed 3/3 focused files with 33/33 tests plus zero diagnostics.

## Full gates, generated state, and final diagnostics

- Exact targeted suite: exit `0`, 18/18 files and 252/252 tests.
- Frontend lint: first exit `1` retained with 2 errors / 1 warning; root-authorized repair rerun exit `0`.
- Frontend full test: exit `0`, 144/144 files and 1,772/1,772 tests. Earlier Task 7 teardown and `ChatPage.core` failures remain separately preserved in `05-full-gates.md`.
- Frontend build: exit `0`, 5,567 modules; build sync left no tracked `web-dist` diff.
- Focused backend packages: exit `0`.
- Repository guard: first exit `2` retained with three `session_history.go` complexity/comment findings; root-authorized same-file refactor passed claudecli tests, then `make guard` exited `0`.
- Final `git diff --check`: exit `0`.
- Final LSP diagnostics: all 22 modified source/test/config files returned zero diagnostics, including no Hint.
- Root final independent verification: 3/3 focused frontend files and 33/33 tests passed in `1.47s`; lint exited `0` in `6.06s`; claudecli focused Go tests exited `0` with package duration `2.146s`; the final 22-file root diagnostics batch returned zero diagnostics.
- Generated state: no guard named a generator; no codemap, project-map, or `web-dist` path changed, so no pre-commit generator refresh was run.
- Pre-commit evidence cut: no verification blocker remains. The commit SHA and hook outcome are recorded by the root final handoff because this evidence document cannot self-reference its containing commit.
