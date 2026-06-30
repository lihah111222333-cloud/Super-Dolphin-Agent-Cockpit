# Worker Prompt Template

Use this template for implementation workers after approval.

```markdown
You are working in `/Users/mima0000/Desktop/wj/super-agent-v3` on workflow `20260630-reasonix-hardening-absorption`.

Task: <task id and title>

Read first:
- `AGENTS.md`
- `README.md`
- `docs/doc/codemap/README.md`
- Relevant codemap volume from the task card
- `docs/internal-notes/LSP系统提示词.md`
- `.agent/workflows/20260630-reasonix-hardening-absorption/CONTEXT.md`
- `.agent/workflows/20260630-reasonix-hardening-absorption/SOURCE_PLAN_SNAPSHOT.md`
- `.agent/workflows/20260630-reasonix-hardening-absorption/FILE_OWNERSHIP.tsv`
- `.agent/workflows/20260630-reasonix-hardening-absorption/TASKS/<task>.md`

Constraints:
- You are not alone in this codebase. Do not revert or overwrite unrelated changes.
- Work only inside the RW paths assigned to your task.
- Treat all NO-TOUCH paths as forbidden.
- Use LSP grep/structure/inspect/xref/file before shared-code edits.
- Add or update focused tests before production changes unless your task is read-only inventory.
- Do not silently fall back on missing config, missing stage source, malformed schema, or denied policy.
- Record exact commands, outputs, and blockers for the orchestrator.

Return:
- Status: DONE, DONE_WITH_CONCERNS, NOT_APPLICABLE_WITH_EVIDENCE, NEEDS_CONTEXT, or BLOCKED.
- Files changed.
- Tests and guard commands run.
- Exact blockers or unresolved risks.

The orchestrator maps `DONE` to DAG state `done`, `NOT_APPLICABLE_WITH_EVIDENCE` to `not_applicable_with_evidence`, and `BLOCKED` to `blocked`. `DONE_WITH_CONCERNS` and `NEEDS_CONTEXT` are not terminal DAG states.
```
