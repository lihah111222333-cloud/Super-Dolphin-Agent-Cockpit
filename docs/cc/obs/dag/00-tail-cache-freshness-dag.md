# OBS Tail Cache Freshness DAG

Goal: fix the high-confidence `OBS-F01` risk by ensuring identical tail-backed queries cannot reuse stale JSONL tail results after tail data changes.

Strict focus gate:

- Keep scope to `OBS-F01`.
- Do not expand into lower-vote findings unless required to make `OBS-F01` testable.
- Prefer removing persistent tail result caching while preserving in-flight call coalescing.

Orchestration note:

- Repository policy names `mcp-go-agent-orchestration`, but that tool is not exposed in this Codex session.
- This DAG is therefore coordinated with git worktrees plus `multi_agent_v1`.

Worker assignment:

| Worker | Worktree branch | Tasks |
| --- | --- | --- |
| W1 | `work/obs-tail-core` | 01, 02 |
| W2 | `work/obs-tail-service-tests` | 03, 04 |
| W3 | `work/obs-tail-rpc-tests` | 05, 06 |
| W4 | `work/obs-tail-api-docs` | 07, 08 |
| W5 | `work/obs-tail-verify` | 09, 10 |

Dependencies:

```text
01 -> 02
02 -> 03
02 -> 04
02 -> 05
02 -> 06
02 -> 08
07 -> 08
03,04,05,06,08 -> 09
09 -> 10
```

Integration rule:

- W1 is the only worker expected to change the core cache behavior in `internal/platform/observability/service.go`.
- W2/W3 add coverage after W1 is merged to the integration branch.
- W4 may run independently for API/docs checks.
- W5 only verifies after all accepted branches are merged.
