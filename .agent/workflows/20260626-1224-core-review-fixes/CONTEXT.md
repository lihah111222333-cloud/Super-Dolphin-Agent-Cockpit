# Context

The source review found confirmed current-code issues in `internal/module/**`, `internal/contract`, and `internal/app`:

- cron accepts invalid schedule/timezone and later falls back to old `next_run_at`.
- turn `StartTurn` ignores durable dedupe writes.
- memory/prompt paths silently fall back on errors.
- skill remote reads and import/home resolution hide boundary errors.
- orchestration metadata/noop paths collapse errors into success-like values.

Repository constraints:

- Source code and tests are truth.
- No silent fallback, swallowed errors, or hidden defaults for these repaired paths.
- Go changes must use TDD: add failing regression tests before production edits.
- After each Go file edit, run `./scripts/test_with_guard.sh <file.go>`.
- Final verification must run affected-package guards.

Non-goals:

- Do not redesign provider mirrors.
- Do not update generated codemap or archive reports.
- Do not touch frontend or legacy Vue packages.
