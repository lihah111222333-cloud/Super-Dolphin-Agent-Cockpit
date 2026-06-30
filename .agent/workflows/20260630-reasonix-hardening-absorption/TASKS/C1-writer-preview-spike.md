---
task_id: C1-writer-preview-spike
owner: agent-c1
status: needs_approval
depends_on: [A0-stage-source-inventory, B2-sessionpaths-migration]
---

# C1-writer-preview-spike

## 1. Goal

Inventory model-callable first-party writer and side-effect surfaces before deciding whether V3 needs a pre-call preview contract.

## 2. Inputs

- `SOURCE_PLAN_SNAPSHOT.md` Lane C.
- A0 tool surface inventory.
- `internal/platform/toolbridge/host_tools.go`
- `cmd/mcp-lsp/`
- `cmd/mcp-orch/tools/`
- `internal/platform/toolbridge/diff_gen.go`
- `internal/platform/toolbridge/diff_fallback.go`
- Provider-native tool boundary notes from codemap 09.

## 3. Outputs

- `docs/adr/2026-06-30-writer-preview-contract-spike.md` or `docs/plans/2026-06-30-writer-preview-contract-spike-amendment.md`, classifying writer surfaces by owner, model-callable status, default exposure, preview feasibility, and excluded side-effect class.
- No production preview API unless separately approved.

## 4. File Permissions

- RW: `docs/adr/2026-06-30-writer-preview-contract-spike.md` if ADR output is chosen.
- RW: `docs/plans/2026-06-30-writer-preview-contract-spike-amendment.md` if plan amendment output is chosen.
- RO: `internal/platform/toolbridge/`, `cmd/mcp-lsp/`, `cmd/mcp-orch/tools/`, provider packages.
- NO-TOUCH: production code unless user approves a follow-up implementation.

## 5. Steps

1. Inventory host-direct default writer `memory_write`.
2. Inventory host-direct non-default writers `workflow_template_save` and `workflow_template_rollback`.
3. Inventory mcp-lsp `edit` actions: `replace_range`, `rename`, `code_action`, and `format`.
4. Inventory mcp-orch `shared_file_write` and all tools exposed through `defineTaskWriteTool`.
5. Inventory workspace run tools: `workspace_create_run`, `workspace_merge_run`, `workspace_abort_run`; classify `workspace_merge_run.dry_run` separately.
6. Inventory artifact/media generation side-effect tools such as `tts_generate`, `av_merge`, and `video_with_audio` if present.
7. Mark Codex/Claude native writer tools as provider-native boundary only for this spike.
8. For each writer, record whether deterministic preview can be produced without side effects.
9. Recommend whether to add an optional `PreviewHostTool` interface in a later approved lane.

## 6. Verification Commands

```bash
rg -n 'memory_write|workflow_template_save|workflow_template_rollback|shared_file_write|defineTaskWriteTool|workspace_create_run|workspace_merge_run|workspace_abort_run|tts_generate|av_merge|video_with_audio|difftracker|Preview' internal cmd docs
```

## 7. DoD

- [ ] Output marks writer owner as host-direct, mcp-lsp, mcp-orch, provider-native, or artifact/process side effect.
- [ ] Output marks whether the writer is default model-callable.
- [ ] Output does not treat post-call diff as pre-call approval preview.
- [ ] Output records preview/execute consistency requirements before any future interface work.

## 8. Rollback

Revert only the named ADR or named plan amendment produced by this spike.
