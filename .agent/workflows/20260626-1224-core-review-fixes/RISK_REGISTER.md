# Risk Register

| Risk | Trigger | Mitigation | Rollback |
|---|---|---|---|
| File write conflict | Two workers edit same package or file | Enforce `FILE_OWNERSHIP.tsv`; integration review rejects cross-lane edits | Revert offending lane patch only |
| Contract drift | P4 changes function signatures used by other modules | Prefer new helper or compatibility wrapper when needed | Restore old signature and add error-returning variant |
| Test flake | Cron time tests depend on wall clock | Use fixed `time.Time` and deterministic scheduler inputs | Keep production fix, rewrite flaky test with fixed clock |
| Fail-fast breaks intended compatibility | Existing tests assert historical fallback | Update tests only when current AGENTS fail-fast policy supersedes old behavior; document conflict in evidence | Keep old behavior and mark blocker if policy conflict is unresolved |
| Worker timeout | Any worker exceeds wait budget | Keep lane isolated, continue with completed lanes, re-dispatch only that lane | Close timed-out worker and preserve current diff |
| Guard baseline drift | Guard command edits or reports baseline changes | Inspect any baseline diff before accepting | Revert unapproved baseline edits |
| Unrelated dirty files | Background changes appear during workers | Do not stage or revert unrelated changes; report separately | Leave unrelated files untouched |
