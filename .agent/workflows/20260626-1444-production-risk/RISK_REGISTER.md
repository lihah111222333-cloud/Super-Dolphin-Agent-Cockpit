# Risk Register

| Risk | Trigger | Mitigation | Rollback |
|---|---|---|---|
| Write-set expansion | Worker needs an unlisted file | Stop with `NEEDS_APPROVAL`; controller reviews real paths and reason | Reject lane diff until approval exists |
| Write conflict | Two lanes edit same path | `FILE_OWNERSHIP.tsv`; L04/L11 pidregistry test split | Keep branches separate and request narrowed patch |
| Missing orchestration MCP | `task_create_dag` tools unavailable | Use DAG/STATE/HANDOFF docs plus Codex blank-context workers | Continue with manual controller ledger |
| Flaky or long tests | Lane verification times out or is nondeterministic | Worker records exact command, exit, and failure; controller decides retry/split | Do not integrate lane |
| Baseline drift | Guard baseline changes during lane | Worker must inspect and report baseline diff | Reject unexplained baseline changes |
| Silent fallback | Worker patches around errors with default behavior | Prompt requires fail-fast and explicit degraded/error result | Reject during review |
| Untracked main files | Plan/workflow files are not present in worker worktrees | Prompts include lane text and constraints directly | Pass exact lane prompt, not path-only context |
| Integration conflict | Lanes pass alone but conflict on merge | Controller merges by planned priority and reruns lane verification after each merge | Reset integration branch only, not worker branches |
