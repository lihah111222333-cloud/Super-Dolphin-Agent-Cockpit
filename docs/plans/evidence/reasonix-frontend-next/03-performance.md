# Reasonix Frontend Next — Surface C Performance Evidence

## Execution facts and serial ownership

- Execution HEAD: `8db4361fcf2c48002699da40b09200a8197b894e`
- Live `origin/main`: `7c3af8c390cce5157fe2824043165fe5d6dc9bba`
- Execution base: `a7df089e32e4135a90f10a52f6ef10069cab8353`
- Branch/worktree: `codex/reasonix-frontend-next-serial` at `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial`
- Serial handoff: Tasks 0-7 predecessor implementer -> Task 8 first replacement implementer (Surface A/B repair and C review) -> Task 8 second replacement implementer (this C evidence, the adjudicated C repair, and full gates).
- The first replacement implementer's execution channel became unresponsive. Root stopped it before the second replacement started. There was no parallel implementer, cherry-pick, rebase, merge, push, new worktree, cleanup, or file loss.

## Scope and live LSP evidence

Surface C performance review covered `frontendPerformancePressure.js`, its tests, the `main.jsx` observer adapter, `AppErrorBoundary` coverage, Wails bridge trace tests, and the canonical trace allowlist/ingest path. The first Task 8 replacement implementer completed live LSP locate, inspect, xref, exact read, and diagnostics before the serial handoff. Across the complete Surface C performance and benchmark set, 16 source/test/config files returned zero diagnostics, including no Hint. The second replacement records this completed evidence and does not misstate it as a repeated review.

Applicable review dimensions were D02 fail-fast, D04 LSP, D09 frontend lifecycle/bridge, D11 observability contract, D12 testing, D14 resource lifecycle/performance, D16 workflow provenance, D18 DRY, and D19 SSOT. Go/provider/store/orchestration/release behavior was outside this Surface C review.

## Focused GREEN retained from the live review

```text
npm exec -- vitest run src/shared/diagnostics/frontendPerformancePressure.test.js src/app/AppErrorBoundary.test.jsx src/shared/api/wailsBridge.test.js --no-file-parallelism --maxWorkers=1
```

Pre-repair exit `0`: 3/3 files and 71/71 tests passed, duration `1.89s`.

This focused GREEN proves the bounded pressure categories, bridge survival, and existing lifecycle cases. It does not cover the partial-initialization failure below and is not used to close that finding.

## Finding C1 — non-transactional partial startup

`P1 | D02,D09,D12,D14 | frontend-app/src/shared/diagnostics/frontendPerformancePressure.js:225-251; frontend-app/src/main.jsx:118-127 | start acquires observer/listeners/timer incrementally but does not roll back earlier resources if a later acquisition throws | deterministic root probe returned {"error":"focus subscribe failed","disconnected":0,"listeners":1}; createLongTaskObserver constructs an observer and calls observe without disconnect-on-throw | test-first transactional rollback, rethrow original initialization error, and preserve idempotent stop`

- Root adjudication: `CLOSED/FIXED` after `FIX_REQUIRED`.
- Pre-repair evidence: `runtime.observer` started as `undefined`; observer creation and two subscriptions happened before the handle was returned. A focus subscription failure left the observer connected and the visibility listener registered. A scheduler failure left both listeners and the observer active. In `main.jsx`, an `observe()` exception left the newly constructed observer connected.
- RED command: the performance monitor and raw-main-source tests exited `1`, 2/2 files failed, 3 failed / 21 passed / 24 total, `837ms`. Focus rollback at test line 286 and scheduler rollback at line 297 each expected one disconnect but received zero; the raw source test at `AppErrorBoundary.test.jsx:36` found no disconnect-on-observe-error structure.
- Repair: `frontendPerformancePressure.js` clears timer/listeners/observer through one idempotent cleanup path and registers each validated unsubscribe immediately. Explicit catch paths attempt secondary cleanup without replacing the authoritative initialization error; `main.jsx` applies the same rule to `PerformanceObserver.observe()` failure. A later gate repair added a cleanup-also-throws test that proves the original error object remains identical.
- Immediate GREEN: the same two files exited `0`, 2/2 files and 24/24 tests passed, `840ms`.
- Final C focused GREEN: monitor + AppErrorBoundary + real Wails bridge exited `0`, 3/3 files and 74/74 tests passed, `1.65s`.
- LSP diagnostics: the four modified C source/test files returned zero diagnostics, including no Hint. `git diff --check` exited `0`.
- Root independent C verification: monitor + AppErrorBoundary exited `0`, 2/2 files and 24/24 tests passed, `914ms`; all four C source/test files returned zero LSP diagnostics.
- Full frontend and repository gates later passed; exact first-failure and repair evidence is retained in `05-full-gates.md`.

## Rejected candidate — phase-specific performance sanitizer

- Root adjudication: `REJECTED_WITH_EVIDENCE`.
- Evidence: there is one exact bounded producer, and the real Wails bridge tests exercise all four performance phases through canonical ingest without returning `false` or filtering the event.
- Reason: a second phase-specific sanitizer would duplicate the canonical trace contract and create another policy owner. No reachable malformed producer path was demonstrated.

## Generated artifacts and remaining work

- Generated artifacts: unchanged; no generator run.
- Remaining: exact-path staging, commit-hook evidence, and final commit.
