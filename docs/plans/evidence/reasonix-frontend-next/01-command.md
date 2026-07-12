# Reasonix Frontend Next — Surface A Command Evidence

## Execution facts

- Execution HEAD: `8db4361fcf2c48002699da40b09200a8197b894e`
- Live `origin/main`: `7c3af8c390cce5157fe2824043165fe5d6dc9bba`
- Execution base / merge-base: `a7df089e32e4135a90f10a52f6ef10069cab8353`
- Branch: `codex/reasonix-frontend-next-serial`
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial`
- Live divergence: `origin/main...HEAD = 19 / 8`; Surface A did not fetch, rebase, merge, cherry-pick, push, or create another worktree.
- Serial implementer handoff: Tasks 0-7 were completed by the predecessor implementer. After Task 7, the user explicitly requested a replacement; the first Task 8 replacement implementer owned Surface A/B repair and Surface C review. After that implementer's execution channel became unresponsive, root stopped it before starting a second Task 8 replacement implementer for C evidence, the adjudicated C repair, and full gates. At most one implementer was active; no file was cleaned or lost across either handoff.

## Linear Task 0-7 commits

1. `59ed17f9a75495c153d6ec54fd32f2fe7b48e204` — Task 0 baseline
2. `8c299a6c69a0f30b9571d8ae3064708d870f4363` — Task 1 registry and shortcut model
3. `e8e6ce864f63717ecc227b6bfd7c33268a07fe49` — Task 2 runtime, dispatcher, typed preference
4. `10bf5e54144085a1db02380d54c1900dc446dd7e` — Task 3 palette and shortcut settings
5. `d48beefa79d7ce27614d5b5f651b68fd3e078a2d` — Task 4 prompt history backend
6. `1fdee79f084862a8ba46ccee528134a0ce9e1acc` — Task 5 prompt history frontend
7. `ca9fbc7e7d0a30587a341f21ed3b8f86b805a9cd` — Task 6 performance pressure
8. `8db4361fcf2c48002699da40b09200a8197b894e` — Task 7 deterministic benchmark

No cherry-pick was required: all eight commits are already on this branch in that order.

## Surface and owned files

Review Surface A covered command registry/runtime, shortcut projection/dispatcher, palette, shortcut Settings, App composition, locale copy, and the `uistate` preference validator. The root agent authorized only these repair paths:

- `frontend-app/src/App.jsx`
- `frontend-app/src/App.test.jsx`
- `frontend-app/src/app/commands/appCommandRegistry.js`
- `frontend-app/src/app/commands/appCommandRegistry.test.js`
- `frontend-app/src/shared/i18n/appI18n.en.json`
- `frontend-app/src/shared/i18n/appI18n.zh.json`
- `internal/module/uistate/preferences.go`
- `internal/module/uistate/model_providers_test.go`
- `docs/plans/2026-07-12-reasonix-frontend-next-absorption.md`
- this evidence file and the Surface A section of `06-independent-review.md`

No Surface B/C production file was modified.

## LSP evidence

All calls used `work_dir=/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial`.

- Locate: `grep(text_search)` located `APP_COMMAND_REGISTRY` at `appCommandRegistry.js:96`, runtime creation at `appCommandRuntime.js:42`, the frontend preference owner at `shortcutSettingsModel.js:3`, the Go preference key at `preferences.go:26`, and palette composition at `App.jsx:549-574,639-647`.
- Inspect: `inspect(definition|hover)` resolved the App registry import to `appCommandRegistry.js:96`, the preference-port key import to `shortcutSettingsModel.js:3`, `createAppCommandRuntime` to its exact runtime signature, and `validateShortcutBindings` to `preferences.go:315`.
- Xref: registry references are limited to App composition and tests; runtime references are limited to App and focused tests; the frontend preference key is consumed by the narrow port and one controller; the Go validator is called by `validatePreferenceValue`.
- Read: exact reads covered descriptor/binding allowlists, platform resolution/conflict, dispatcher listener ownership, App controller/runtime composition, palette FocusTrap/disabled behavior, Settings read-after-write, and the Go 5-field/64-entry/deep-clone contract.
- Final diagnostics: `file(diagnostics)` on every modified A source/test/JSON file and the plan returned `No diagnostics found`. This closes the previous `mapsloop` and `rangeint` hints.

## RED and rerun history

### Review baseline

- Frontend focused command: `npm exec -- vitest run src/app/commands src/shared/keyboard src/features/command-palette src/features/shortcut-settings src/pages/settings/SettingsPage.test.jsx src/App.test.jsx scripts/frontend-z-index-token-guard.test.mjs --no-file-parallelism --maxWorkers=1`
- Exit `0`: 13 files / 354 tests passed before repair.
- Go focused command: `go test ./internal/module/uistate -run 'Shortcut|Preference' -count=1`
- Exit `0`.

### Test-first RED

- Frontend registry/App RED command: `npm exec -- vitest run src/app/commands/appCommandRegistry.test.js src/App.test.jsx --no-file-parallelism --maxWorkers=1`
- Exit `1`: 2 failed / 224 passed / 226 total. The 129-character id was accepted. The first App fixture did not actually disable interrupt and therefore exposed no reason; this was a test setup defect, not a GREEN result.
- Refined App RED command: `npm exec -- vitest run src/App.test.jsx -t 'localizes the disabled interrupt reason in the English command palette' --no-file-parallelism --maxWorkers=1`
- Exit `1`: received `Interrupt current taskInterrupt the active task当前没有可中断任务escape`, proving the English palette used a Chinese disabled reason.
- Go RED command: `go test ./internal/module/uistate -run TestShortcutBindingsPreferenceValidation -count=1`
- Exit `1`: the `command_id_too_long` subtest received `error=nil`.

The implementer initially added a non-ASCII rejection case, but root rejected that scope expansion before production repair. The case was removed and no charset restriction was implemented.

### Post-repair non-green attempts

- The first combined frontend/Go attempt returned a valid Go exit `0` but only the frontend Vitest start banner, without exit or summary. It is recorded as `INCONCLUSIVE`, not PASS.
- The next explicit frontend run exited `1`: 12/13 files and 355/356 tests passed. The sole failure showed the new length error had accidentally replaced the pre-existing blank-id error contract. The implementation was narrowed so blank/trimmed failures retain `invalid command descriptor`, while only IDs longer than 128 use the bounded error.

### Final focused GREEN

- Frontend focused command above: exit `0`, 13/13 files and 356/356 tests passed, duration `23.98s`.
- Go focused command above: exit `0`, package `ok`, duration `0.021s`.
- `git diff --check`: exit `0`.

### Root independent verification

- Frontend: `appCommandRegistry.test.js + App.test.jsx`, exit `0`, 2/2 files and 226/226 tests passed, duration `18.84s`.
- Go: `go test ./internal/module/uistate -run 'Shortcut|Preference' -count=1`, exit `0`, duration `0.037s`.
- LSP: six modified code/test files returned zero diagnostics.
- Root final Surface A adjudication: `CLOSED/FIXED`.

At this Surface A checkpoint, later gates were not yet claimed. They were subsequently completed with first failures and final GREEN results retained in `05-full-gates.md`.

## Findings and root adjudication

| Finding | Root adjudication | Evidence and fix |
|---|---|---|
| P2 D02/D10/D14 — command IDs lacked the planned length bound | `CLOSED/FIXED` | Plan now specifies 1..128 Unicode code points. Frontend rejects >128 at `appCommandRegistry.js:55-60`; Go rejects >128 without echoing the untrusted ID at `preferences.go:325-334`. Tests accept 128 and reject 129. |
| P2 D09/D15 — English palette could show a Chinese interrupt disabled reason | `CLOSED/FIXED` | `App.jsx:549-574` consumes current locale copy; en/zh fields are at each locale JSON line 185; `App.test.jsx:1049-1062` proves English text and rejects Chinese text. |
| P3 D04/D12 — `mapsloop` LSP Hint | `CLOSED/FIXED` | `preferences.go:451` uses Go 1.25 `maps.Copy`; final diagnostics are empty. |
| P3 D04/D12 — `rangeint` LSP Hint | `CLOSED/FIXED` | `model_providers_test.go:78` uses `for index := range 65`; final diagnostics are empty. |

Remaining Surface A blockers: none. Surfaces B/C, full gates, generated-state audit, and final 22-file diagnostics were subsequently completed; only exact-path staging, commit-hook evidence, and final commit remain.
