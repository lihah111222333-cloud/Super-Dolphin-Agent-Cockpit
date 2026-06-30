# ADR: Writer Preview Contract Spike

日期：2026-06-30

状态：Accepted as ADR-only spike; production preview contract deferred

## 背景

Reasonix 的 `Previewer` 更接近 UI/checkpoint 预览：先把即将发生的文件或状态变化展示出来，再由外层决定是否继续。V3 的写入面并不是单一 in-process file writer，而是分散在 host-direct toolbridge、mcp-lsp、mcp-orch、provider-native runtime 和媒体进程/远端 API 里。

本 spike 只盘点 V3 一方模型可调用 writer / side-effect surfaces，并判断未来是否值得新增可选 pre-call preview contract。本 lane 不新增生产 preview API，也不改变工具调用、审批、difftracker 或 provider 行为。

## 与源计划验收的差异

源计划曾要求至少为一个 host-direct writer 补 preview/execute 一致性单元测试。本 ADR 不关闭该测试验收；C1 被有意收束为 ADR-only spike，因为当前没有 production preview API、host interface、MCP schema、provider hook、UI surface 或共享 preview/execute 路径可测。后续如要实现生产 preview contract，必须另开已批准任务并补对应测试。

## 当前事实

| Surface | Source anchor | Owner | 默认模型可调用 | Side-effect |
| --- | --- | --- | --- | --- |
| `memory_write` | `internal/platform/toolbridge/memory_write_tool.go:14`, `:51-57`, `:65-82`; `internal/platform/toolbridge/module.go:81-90`; `internal/platform/toolbridge/handler_host_tools.go:84-115` | host-direct | Yes, when memory writer capability and tool switch are enabled | Writes durable agent memory through `contract.AgentMemoryWriter` |
| `workflow_template_save` / `workflow_template_rollback` | `internal/platform/toolbridge/host_tools.go:57-84`, `:187-199`, `:236-281`, `:313-329`; `internal/platform/toolbridge/module.go:81-90`; `internal/platform/toolbridge/handler.go:247-249` | host-direct | No by default; write registry is separate and requires `WorkflowTemplateWriteAuthority` | Writes or rolls back workflow template assets |
| mcp-lsp `edit` action `replace_range` | `cmd/mcp-lsp/tools.go:33-41`, `:64-78`; `cmd/mcp-lsp/tools/tool_edit.go:68-98`; `cmd/mcp-lsp/tools/tool_edit_replace.go:170-249`; `cmd/mcp-lsp/tools/tool_edit_replace_update.go:17-37` | mcp-lsp | Conditional default: built-in `lsp` manifest family, listed when mcp-lsp and semantic LSP support are available | Writes target file, then syncs LSP or confirms via git diff |
| mcp-lsp `edit` action `rename` | `cmd/mcp-lsp/tools/tool_edit_rename.go:30-69`, `:71-123`; `cmd/mcp-lsp/multilsp/manager_symbols.go:344-364` | mcp-lsp | Conditional default | Requests LSP WorkspaceEdit, applies edits across one or more files, rolls back on failure |
| mcp-lsp `edit` action `code_action` | `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go:21-82`; `cmd/mcp-lsp/multilsp/manager_symbols.go:366-382` | mcp-lsp | Conditional default | Applies the single unambiguous WorkspaceEdit returned by LSP; multiple candidates return no-change |
| mcp-lsp `edit` action `format` | `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go:84-129`; `cmd/mcp-lsp/multilsp/manager_symbols.go:384-401` | mcp-lsp | Conditional default | Applies LSP TextEdits to disk |
| `shared_file_write` | `cmd/mcp-orch/tools/registry.go:37-48`; `cmd/mcp-orch/tools/shared_file_tools.go:50-61`, `:85-112` | mcp-orch | Yes when built-in `orch` sidecar is available and lifecycle policy permits | Validates shared path and upserts shared file content |
| `defineTaskWriteTool` tools | `cmd/mcp-orch/tools/task_tool_definitions.go:17-27`, `:49-129`, `:145-157` | mcp-orch | Yes when built-in `orch` sidecar is available and lifecycle policy permits | Mutates DAG definitions, run/runtime state, dispatch, termination, deletion, or recovery state |
| `workspace_create_run` | `cmd/mcp-orch/tools/workspace_tools.go:117-128`; `cmd/mcp-orch/workspace/service.go:68-88` | mcp-orch | Yes when built-in `orch` sidecar is available and lifecycle policy permits | Creates workspace directory, copies bootstrap files, persists run/file state |
| `workspace_merge_run` | `cmd/mcp-orch/tools/workspace_tools.go:140-145`; `cmd/mcp-orch/workspace/service.go:254-280`; `cmd/mcp-orch/workspace/service_merge.go:15-57`, `:90-169` | mcp-orch | Yes when built-in `orch` sidecar is available and lifecycle policy permits | Non-dry-run writes or deletes source files and mutates run/file state |
| `workspace_merge_run.dry_run=true` | `cmd/mcp-orch/workspace/service.go:271-274`; `cmd/mcp-orch/workspace/service_dry_run.go:10-38` | mcp-orch | Same tool as `workspace_merge_run` | Does not write source files, but still transitions persistent run status `active -> merging -> active` |
| `workspace_abort_run` | `cmd/mcp-orch/tools/workspace_tools.go:146-150`; `cmd/mcp-orch/workspace/service.go:282-294` | mcp-orch | Yes when built-in `orch` sidecar is available and lifecycle policy permits | Marks run aborted and emits abort/status events |
| `tts_generate` | `cmd/mcp-orch/tools/registry.go:46-48`; `cmd/mcp-orch/tools/tts_tools.go:23-37`, `:39-56`; `cmd/mcp-orch/tools/video_with_audio_tools.go:96-125` | artifact/process side effect via mcp-orch | Yes when built-in `orch` sidecar is available and lifecycle policy permits | Calls remote SiliconFlow TTS and writes local MP3 |
| `av_merge` | `cmd/mcp-orch/tools/registry.go:46-48`; `cmd/mcp-orch/tools/av_merge_tools.go:21-35`, `:37-81` | artifact/process side effect via mcp-orch | Yes when built-in `orch` sidecar is available and lifecycle policy permits | Runs local ffmpeg and writes/overwrites MP4 |
| `video_with_audio` | `cmd/mcp-orch/tools/registry.go:46-48`; `cmd/mcp-orch/tools/video_with_audio_tools.go:24-38`, `:40-93`, `:181-197` | artifact/process side effect via mcp-orch | Yes when built-in `orch` sidecar is available and lifecycle policy permits | Calls remote video/TTS APIs, downloads files, runs ffmpeg, writes local MP4 |
| Codex / Claude native writer tools | `docs/doc/codemap/09-provider.md:20-50`; `internal/provider/codexapp/support.go:463-470`; `internal/provider/codexapp/session_approval.go:47-75`; `internal/provider/claudecli/transport_config.go:196-230` | provider-native | Provider-dependent, not V3-owned host/MCP tool | Provider CLI/app owns native file writes; V3 only configures provider, dynamic tools, and approval bridge |
| post-call difftracker | `internal/platform/toolbridge/diff_gen.go:12-80`; `internal/platform/toolbridge/diff_fallback.go:14-72`, `:118-140`; `internal/platform/toolbridge/phase1_diff_test.go:291-417` | post-call observation | Not a writer | Captures or reconstructs diff after an edit call; it is not a pre-call approval preview |

`defineTaskWriteTool` currently exposes these write tools: `task_create_dag`, `task_dag_apply_ops`, `task_update_node`, `task_dispatch_node`, `task_start_dag`, `task_terminate_dag`, `task_delete_dag`, and `task_workflow_recovery_action`.

The default built-in MCP families are created by `internal/contract/manifest.go:17-72`: V3 starts with `lsp` and `orch`, then appends extra MCP binaries. This makes mcp-lsp and mcp-orch first-party default surfaces, but actual model visibility is still conditional on sidecar availability, semantic LSP availability for semantic tools, and lifecycle filtering.

## 分类

| Writer class | Default model-callable | Deterministic pre-call preview feasible | Reason |
| --- | --- | --- | --- |
| `memory_write` | Yes, capability-gated | Partial / good candidate | Input normalization is deterministic, including CRLF to LF and fixed scope/type checks. Final storage IDs, module-side validation, and memory write side effects still belong to the memory owner. |
| `workflow_template_save` / `rollback` | No default | Partial / candidate after authority | The registry can validate template ID/version and render a template diff, but preview must run after write authority and must not expose asset content before permission. |
| `edit.replace_range` | Conditional default | Yes / strongest candidate | Existing flow already builds a `replacePlan` before disk write. A future preview can reuse parser, path validation, line-ending preservation, and current-file hash before execute. |
| `edit.rename` | Conditional default | Partial | Needs an LSP `textDocument/rename` request to obtain WorkspaceEdit. Preview is feasible only if the exact WorkspaceEdit plus target file hashes are frozen and revalidated at execute time. |
| `edit.code_action` | Conditional default | Partial | Single WorkspaceEdit can be previewed; multiple actions intentionally return no-change today, so preview must not auto-select an opaque action. |
| `edit.format` | Conditional default | Partial | LSP TextEdits can be previewed, but they depend on current file content, language-server state, and formatting options. Execute must use the same edits or reject on drift. |
| `shared_file_write` | Yes, lifecycle-gated | Partial / candidate | Path validation and content size are deterministic. Preview should show path, byte count/hash, and bounded content excerpt, not unlimited body. |
| DAG/task write tools from `defineTaskWriteTool` | Yes, lifecycle-gated | Mostly no; targeted partial previews only | Definition edits with OCC may support a read-only plan preview. Runtime tools such as start, dispatch, terminate, delete, and recovery involve persisted state transitions, wakeups, active runs, and races that cannot be previewed as a guaranteed future result. |
| `workspace_create_run` | Yes, lifecycle-gated | Partial intent preview only | Can preview target roots and bootstrap file list, but real execution creates directories, copies files, and persists run/file rows. |
| `workspace_merge_run` | Yes, lifecycle-gated | Partial | Merge plan can be computed, but source file drift and delete safety must be rechecked at execute time. Non-dry-run writes source files. |
| `workspace_merge_run.dry_run=true` | Same as merge tool | Not a true pre-call preview today | It avoids source writes but still mutates run state while computing the result. It should not be reused as an approval preview without refactoring into a pure planner. |
| `workspace_abort_run` | Yes, lifecycle-gated | Partial / simple status preview | The intended status transition is easy to show, but execution still updates persistent state and emits events. |
| `tts_generate`, `av_merge`, `video_with_audio` | Yes, lifecycle-gated | No deterministic preview | Remote APIs, timestamps, downloads, local ffmpeg output, and media bytes cannot be predicted without doing the side effect. Only intent/command/input summaries are safe. |
| Provider-native Codex / Claude writers | Provider-dependent | No V3-owned preview | They are owned by the provider CLI/app and its permission model. V3 should not claim deterministic preview over native tools it does not execute. |
| difftracker / fallback diff | N/A | No | It observes actual post-call changes after execution or after ToolCallEnd fallback. It cannot satisfy pre-call approval because the side effect has already happened. |

## 建议决策

Future work should add an optional first-party preview contract, but only after a separate approved implementation lane. The contract should be optional and capability-discovered; tools without a deterministic preview must remain executable only through existing policy/approval paths.

Recommended shape:

1. Add an optional `PreviewHostTool` or equivalent preview-capable interface for host-direct writers, returning a bounded structured preview rather than a free-form string.
2. For MCP first-party sidecars, prefer tool-specific pure preview/planning actions or an equivalent plan token, not a generic best-effort wrapper around every tool.
3. Treat `replace_range`, bounded `shared_file_write`, `memory_write`, and authorized workflow template writes as first candidates.
4. Treat `rename`, `code_action`, `format`, and `workspace_merge_run` as candidates only if preview produces a frozen plan containing target file hashes, LSP edits, and version/OCC material that execute must revalidate.
5. Exclude provider-native tools and media generation from V3-owned deterministic preview. They may expose intent summaries, but not approval-grade diffs.
6. Do not use difftracker as approval preview. Keep it as post-call observability and recovery evidence.

## Preview / Execute Consistency Requirements

Any future preview contract must preserve these constraints:

1. Schema and lifecycle validation run before preview generation; preview cannot bypass disabled/suspended/removed tool policy.
2. Path, workspace-root, symlink, app-managed-root, and write-authority checks run before reading file content for preview.
3. Preview and execute must share the same parser and normalization code. This includes CRLF/LF handling, final newline behavior, UTF-16 to byte offset conversion, and file mode preservation.
4. Large files and large content bodies need bounded previews with byte counts and hashes. Preview must not dump unbounded shared-file content, media bytes, or secrets.
5. Binary and unsupported encodings must fail closed or produce metadata-only previews; execute must not silently reinterpret them differently.
6. File previews must carry source file hashes or equivalent version material. Execute must re-read and reject if the file changed after preview.
7. DAG/workspace previews must carry OCC version, run status, source/workspace roots, and relevant file hashes. Execute must revalidate them after approval.
8. Permission and approval order must be stable: decode schema -> lifecycle/tool policy -> path/auth validation -> preview -> user approval -> execute with the same validated plan or reject on drift.
9. Preview output is not a transaction log. A preview that cannot prove execute consistency must label itself as intent-only.

## 非目标

- 本 lane 不新增 production preview API、host interface、MCP schema、provider hook、UI surface 或 tests.
- 不修改 provider home、skill mirror、runtime install roots、frontend、generated files 或 workflow state.
- 不把 `workspace_merge_run.dry_run` 当作已经合格的 approval preview.
- 不把 difftracker 或 fallback diff 当作 pre-call preview.
- 不接管 Codex / Claude provider-native writer tools.
- 不为 media/artifact generation 伪造可预测输出 diff.

## 验证记录

Commands and LSP operations run for this spike:

```bash
git status --short
git rev-parse HEAD
git branch --show-current
git worktree list --porcelain
rg -n 'A0|tool surface|inventory|memory_write|workflow_template_save|shared_file_write|workspace_create_run' .agent/workflows/20260630-reasonix-hardening-absorption
rg -n 'func .*Rename|func .*CodeAction|func .*Format|ApplyWorkspaceEdit|Format\(' cmd/mcp-lsp/multilsp cmd/mcp-lsp/manager cmd/mcp-lsp/tools
rg -n 'func BuildManifest|buildDefault|FamilyLSP|FamilyOrch|mcp-lsp|mcp-orch' internal/contract internal/dto/provider internal/provider/manifestbuilder
```

The original discovery search was broad. For review or refresh, use a scoped command and do not count archive/codemap or generic UI/log `Preview` hits as current writer-surface evidence:

```bash
rg -n --glob '!docs/archive/**' --glob '!docs/doc/codemap/**' 'memory_write|workflow_template_save|workflow_template_rollback|shared_file_write|defineTaskWriteTool|workspace_create_run|workspace_merge_run|workspace_abort_run|tts_generate|av_merge|video_with_audio|difftracker' internal/platform/toolbridge internal/contract internal/provider/codexapp internal/provider/claudecli cmd/mcp-lsp cmd/mcp-orch/tools cmd/mcp-orch/workspace
```

LSP verification:

```text
grep(text_search): ToolNameMemoryWrite|ToolNameWorkflowTemplateSave|ToolNameWorkflowTemplateRollback|defineTaskWriteTool|workspace_merge_run|shared_file_write|tts_generate|av_merge|video_with_audio
grep(ast_search): func defineTaskWriteTool($$$) $$$ { $$$ }
structure(document_symbol): internal/platform/toolbridge/memory_write_tool.go
structure(document_symbol): internal/platform/toolbridge/host_tools.go
inspect(definition): internal/platform/toolbridge/memory_write_tool.go:14:7
xref(references): internal/platform/toolbridge/memory_write_tool.go:14:7
xref(call_hierarchy incoming): internal/platform/toolbridge/memory_write_tool.go:52:40
file(read_file): internal/platform/toolbridge/memory_write_tool.go:51
file(read_file): internal/platform/toolbridge/handler_host_tools.go:86
file(read_file): cmd/mcp-orch/tools/task_tool_definitions.go:19
file(diagnostics): internal/platform/toolbridge/memory_write_tool.go, internal/platform/toolbridge/host_tools.go, cmd/mcp-lsp/tools/tool_edit.go, cmd/mcp-orch/tools/task_tool_definitions.go, cmd/mcp-orch/tools/workspace_tools.go, cmd/mcp-orch/tools/shared_file_tools.go
```

The LSP diagnostics query returned existing hints/info in `internal/platform/toolbridge/host_tools.go` (`omitempty` hints and unused private helper info), with no C1 source changes made.
