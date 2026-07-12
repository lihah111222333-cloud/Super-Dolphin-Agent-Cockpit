# Reasonix Frontend Next — Surface B Prompt History Evidence

## Execution facts

- Execution HEAD: `8db4361fcf2c48002699da40b09200a8197b894e`
- Live `origin/main`: `7c3af8c390cce5157fe2824043165fe5d6dc9bba`
- Execution base: `a7df089e32e4135a90f10a52f6ef10069cab8353`
- Branch/worktree: `codex/reasonix-frontend-next-serial` at `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial`
- Serial handoff: Tasks 0-7 predecessor implementer -> Task 8 first replacement implementer (Surface A/B repair and C review) -> Task 8 second replacement implementer (C evidence, adjudicated C repair, and full gates). Root stopped the first replacement after its execution channel became unresponsive and only then started the second; no cherry-pick, rebase, merge, push, new worktree, parallel implementer, cleanup, or file loss occurred.
- Task 0-7 commit order: `59ed17f9a75495c153d6ec54fd32f2fe7b48e204`, `8c299a6c69a0f30b9571d8ae3064708d870f4363`, `e8e6ce864f63717ecc227b6bfd7c33268a07fe49`, `10bf5e54144085a1db02380d54c1900dc446dd7e`, `d48beefa79d7ce27614d5b5f651b68fd3e078a2d`, `1fdee79f084862a8ba46ccee528134a0ce9e1acc`, `ca9fbc7e7d0a30587a341f21ed3b8f86b805a9cd`, `8db4361fcf2c48002699da40b09200a8197b894e`.

## Review surface and LSP evidence

Surface B covered message source revision, exact-CWD authorization, prompt-history scanner/cursor/nonce/RPC, Claude JSONL paging, frontend facade/validator/controller/hook, and Composer navigation. The initial review was read-only; root subsequently adjudicated B1-B3 as `FIX_REQUIRED` and authorized only those repairs.

All LSP calls used the fixed worktree as `work_dir`:

- Locate: `grep(text_search)` located `sourceRevision`, `PromptHistory`, `thread/promptHistory`, `createPromptHistoryController`, and every frontend `invalidate` call.
- Inspect: definitions/hovers confirmed `service.ScanPromptHistory -> prompthistory.ScanPromptHistory`, Composer -> hook -> controller, and the sole backend facade owner.
- Xref: service scan references are RPC plus focused tests; controller is consumed only by the hook; hook is consumed only by ComposerDock; Claude `ReadMessagesPage` is consumed by thread history and focused tests.
- Exact read: `history.go:98-158` verifies active-thread exact CWD before any message read; `scanner.go:92-434` verifies bounded thread/limit state, stable ordering, nonce, strict cursor JSON, duplicate-text preservation, and stale handling; JSONL and Claude pager code verifies source revision and fallback behavior; frontend controller/hook/composer/validator code verifies sentinel, stale retry, IME/caret boundary, and request/response contracts.
- Diagnostics: the initial 32 Task 4/5 modified Go/JS/JSX source and test files returned `No diagnostics found`. After repair, the 18 currently modified Go/JS/JSX/JSON source and test files first returned one Go `forvar` Hint at `session_history_test.go:117`; the redundant loop-variable copy was removed and the complete batch then returned `No diagnostics found`.

## Focused verification

Frontend command:

```text
npm exec -- vitest run src/features/prompt-history src/pages/chat/composer/ComposerDock.test.jsx src/shared/api/backendApi.test.js src/shared/api/backendResponseValidators.test.js src/shared/api/backendApi.contractMatrix.test.js --no-file-parallelism --maxWorkers=1
```

Exit `0`: 6/6 files and 131/131 tests passed, duration `3.21s`.

Go command:

```text
go test ./internal/dto/provider ./internal/dto/thread ./internal/util/historyjsonl ./internal/provider/claudecli ./internal/module/thread/prompthistory ./internal/module/thread -count=1
```

Exit `0`: all six packages passed. Timings: provider DTO `0.006s`, thread DTO `0.006s`, historyjsonl `0.022s`, claudecli `2.284s`, prompthistory `0.008s`, thread `0.345s`.

These were the pre-repair GREEN suites and did not cover the three findings below. Full Task 8 gates were not run in Surface B.

## Findings — root adjudicated and repaired

### B1 — P1 contract/liveness: generated cursor can exceed its wire cap

`CLOSED/FIXED`

- Evidence: `prompthistory/scanner.go:427-432` caps pre-base64 JSON at 2048 bytes, while `scanner.go:399-403` checks decoded bytes after base64 decode. RPC request validation at `thread/rpc.go:153-157` and frontend payload validation at `backendApiFactoryThread.js:144-151` cap the wire string itself at 2048 bytes. `backendResponseValidators.js:301-315` checks only cursor/nonce types and cursor presence, not byte caps.
- Deterministic probe: a legal cursor JSON with 1,900-byte `before` is 2,016 raw bytes but 2,688 base64url wire bytes. The server can therefore emit a response cursor the frontend refuses to send back.
- Minimal repair: enforce the 2048-byte limit on the encoded cursor before returning it; reject raw wire cursor length before decoding; add response validator byte caps for `nextCursor` and `nonce`; add server/frontend tests proving the largest accepted cursor round-trips and the next byte fails.
- Root adjudication: `FIX_REQUIRED`. The 2048-byte contract is the base64url wire length. Server decode rejects oversized wire before decoding at `prompthistory/scanner.go:399-405`, encode validates the encoded wire at `scanner.go:430-439`, and frontend response validation applies a UTF-8 byte cap to `nextCursor` and `nonce` at `backendResponseValidators.js:301-320`.
- RED: `TestPromptHistoryCursorEnforcesEncodedWireLimit` exited `1`; the server emitted a 2,688-byte wire cursor without error. Frontend response validation also accepted 2,049-byte cursor/nonce values.
- GREEN: the focused Go cursor test exited `0` (`0.006s`), and the combined frontend validator/hook/Composer run passed 53/53 tests.

### B2 — P1 stale behavior: same-CWD lifecycle changes do not invalidate the controller

`CLOSED/FIXED`

- Evidence: plan line 175 requires explicit invalidation on thread create/delete/archive/rename. `usePromptHistory.js:22-32` rebuilds only for `activeThreadId`, `cwd`, or `fetchPage`; `usePromptHistory.js:46-52` invalidates only after successful send or explicit caller use. Composer passes no thread-lifecycle signal. Existing tests cover cwd and send, not same-CWD non-active thread lifecycle changes.
- Documentation contradiction: plan line 173 says the composer must be empty, while Task 5's `captureDraft('unfinished')` sentinel and Step 4 caret-boundary contract deliberately allow non-empty drafts. The implementation matches the sentinel behavior.
- Minimal repair: pass one canonical thread-list lifecycle revision/token from the existing store projection into the per-composer hook and invalidate on its change; add create/delete/archive/rename tests, including non-active threads in the same CWD. Repair the plan sentence to allow non-empty drafts at top/bottom caret boundaries; do not remove the sentinel or add a second state truth source.
- Root adjudication: `FIX_REQUIRED`. `ComposerDock.jsx:122` now passes the existing `store?.threads` reference as a loss-only lifecycle signal; `usePromptHistory.js:17-31` includes that signal in controller memoization. No thread content is serialized, copied, uploaded, or persisted as another truth source.
- Plan repair: an unfinished draft remains the sentinel. Navigation requires collapsed selection, no IME composition, and ArrowUp on the first logical line or ArrowDown on the last logical line.
- RED: the hook's create/delete/archive/rename cases retained the previous `before`; the four Composer cases timed out. The combined frontend RED run exited `1` with 9 failed and 44 passed tests.
- GREEN: all create/delete/archive/rename hook and Composer cases passed inside the 53/53 targeted run and the 139/139 final Surface B frontend run.

### B3 — P1 pagination continuity/error handling: Claude fallback can switch sources across pages

`CLOSED/FIXED`

- Evidence: `claudecli/session_history.go:49-59` reads the requested target first and only falls back to resolved session ID when the page has no items. If an existing empty/noise-only target file causes the first page to fall back to a resolved file, `NextBefore` belongs to that resolved file. On continuation, the target file is read first with the resolved offset; `historyjsonl/page.go:340-353` rejects an offset larger than the target file before fallback can run. The fallback branch at `session_history.go:56` also drops a non-nil fallback error and returns the original empty page.
- Existing tests cover resolved fallback for non-paged `ReadHistory` and direct paging, but not fallback paging continuity or fallback error propagation.
- Minimal repair: make source selection stable for the whole opaque provider cursor (for example, tag the selected target in a bounded/private wrapper cursor or deterministically resolve one source before emitting the first cursor), and propagate fallback read errors. Add a two-page test with an existing empty/noise local-ID file plus a multi-page resolved UUID file, and a fallback read-error test.
- Root adjudication: `FIX_REQUIRED`. `claudecli/session_history.go:57-235` keeps source selection stable and uses the bounded private prefix `claude-resolved:` plus base64url of the exact JSON fields `version`, `source`, and `before`. The wrapper contains no thread ID, path, or message content, and both wire and decoded payload are capped at 2048 bytes.
- Source selection: raw unwrapped cursors continue against the requested target. A wrapped cursor reads the resolved source directly and keeps wrapping while `HasMore`; first-page fallback is selected for either visible items or `HasMore=true`, so a filtered empty page cannot discard older valid messages. Fallback read errors are propagated.
- RED: the two-page test failed with `history page cursor offset 93 outside file size 6`; the fallback read-error test received `nil` instead of an error.
- GREEN: resolved pagination/error tests and strict decoder tests exited `0` (`0.039s`, then `0.012s` after the Hint repair). The table covers malformed base64, unknown version/source/field, trailing JSON, empty `before`, wire over 2048 bytes, and raw non-prefix passthrough.

## Repair verification

- Frontend targeted RED command: validator + hook + Composer, exit `1`, 3 failed files, 9 failed / 44 passed / 53 total.
- Go B1 RED: exit `1`; encoded wire length was 2,688 bytes with no error.
- Go B3 RED: exit `1`; resolved continuation used the target file and fallback read errors were swallowed.
- Frontend targeted GREEN: exit `0`, 3/3 files and 53/53 tests, `2.89s`.
- Frontend final focused command shown above: exit `0`, 6/6 files and 139/139 tests, `4.28s`.
- Go final focused command shown above: exit `0`; provider DTO `0.007s`, thread DTO `0.007s`, historyjsonl `0.025s`, claudecli `2.407s`, prompthistory `0.008s`, thread `0.497s`.
- Surface A regression: frontend App + command registry exit `0`, 2/2 files and 226/226 tests, `19.01s`; Go uistate `Shortcut|Preference` exit `0`, `0.024s`.
- `git diff --check`: exit `0`.
- LSP retry record: an initial broad validator replacement and an initial large B3 replacement did not apply; both were re-read and narrowed before successful edits. One test insertion returned `patch_no_match`; the exact region was re-read and the narrower insertion succeeded. No failed edit was treated as applied.
- Root independent post-fix verification: frontend hook + Composer + response validator passed 3/3 files and 53/53 tests, exit `0`, `2.15s`; Go prompthistory + claudecli passed, exit `0`, `0.007s` / `2.140s`; all 10 Surface B modified files returned zero LSP diagnostics.

## Status

- Root adjudication: B1-B3 were `FIX_REQUIRED` and are now `CLOSED/FIXED`; root's independent post-fix rerun passed.
- Surface B production fixes: applied only for B1-B3; no Surface C edit was made.
- Generated artifacts: unchanged; no generator run.
- Remaining Task 8 work: Surface C, full gates, generated-state audit, and final 22-file diagnostics were subsequently completed; only exact-path staging, commit-hook evidence, and final commit remain.
