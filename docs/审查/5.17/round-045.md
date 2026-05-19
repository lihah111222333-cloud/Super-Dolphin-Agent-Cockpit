# Round 045 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:47:36 KST
- 结束：2026-05-17 07:55:10 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 sharedfile 的 MCP 工具、registry 工具、UI memory shared-file 操作、auto-continue 内部状态和 dashboard retention，重点看量化输出文件的读写权限、删除保护和提升为 memory 的边界。

- `cmd/mcp-orch/tools/shared_file_tools.go`
- `cmd/mcp-orch/tools/registry_tools.go`
- `cmd/mcp-orch/tools/parity_v2_test.go`
- `internal/module/memory/ui_rpc.go`
- `internal/module/memory/ui_rpc_mutations.go`
- `internal/module/memory/ui_rpc_auto_continue_state.go`
- `internal/module/memory/ui_rpc_merge_shared_shard19_test.go`
- `internal/module/dashboard/ui_page.go`
- `internal/contract/orchestration.go`

## Findings

1. **[major] MCP `shared_file_read` 对 agent 读权限比写权限宽，可读取 legacy/_internal 路径**
   - 证据：`readSharedFile()` 只调用 `ValidateReadPath()`（`cmd/mcp-orch/tools/shared_file_tools.go:59-76`），该策略只防 traversal/absolute，不做 prefix whitelist（`internal/platform/sharedfilepath/policy.go:92-98`）。测试也只覆盖路径标准化和 not found 翻译（`cmd/mcp-orch/tools/parity_v2_test.go:178-193`）。
   - 风险：量化 agent 能读取任何 DB 中存在的相对路径 sharedfile，包括 `_internal/auto-continue`、历史 legacy 前缀或系统调试文件；这比 `shared_file_write` 的 agent 限制更宽。
   - 建议：shared_file_read 增加 caller-aware policy；agent 读默认限制公开 prefix，`_internal` 和 legacy 需 UI/system 权限。

2. **[major] MCP `shared_file_write` 总是覆盖写入，没有 CAS、append 或 lock mode**
   - 证据：工具说明是 “Creates or overwrites”（`cmd/mcp-orch/tools/shared_file_tools.go:52-55`），`writeSharedFile()` 只传 `Path/Content/UpdatedBy` 给 `Upsert()`（`cmd/mcp-orch/tools/shared_file_tools.go:79-105`）。测试只断言覆盖路径标准化、10MB 上限和保留段拒绝（`cmd/mcp-orch/tools/parity_v2_test.go:196-300`）。
   - 风险：多个量化 agent 或长任务按照 handoff 协议写 `_internal/progress/<task>.md` 时会互相覆盖；文档提示“追加一行”，但工具实际没有 append 语义。
   - 建议：工具参数支持 `mode=append|create|overwrite` 和 expected_version/hash；默认拒绝覆盖已有文件。

3. **[major] UI 删除保护只检查 run metadata.final_output，不检查 DAG node config 和节点 result 引用**
   - 证据：`ensureSharedFileDeleteAllowed()` 只调用 `sharedFileReferencedByFinalOutput()`（`internal/module/memory/ui_rpc.go:463-475`）；后者遍历 DAG runs，仅通过 `FinalOutputFileFromRunMetadata()` 解析 run metadata（`internal/module/memory/ui_rpc.go:477-528`；`internal/contract/orchestration.go:354-379`）。测试也只保护 `metadata.final_output.path`（`internal/module/memory/ui_rpc_merge_shared_shard19_test.go:479-539`）。
   - 风险：量化 DAG 的 `outputs.to_sharedfile`、`inputs.from_sharedfiles`、node result 中的 `{"sharedfile":{"path":...}}` 仍可能被 UI 删除，导致后续重放或下游节点读不到依赖文件。
   - 建议：删除保护同时扫描 DAG 当前模板、run nodes 的 config/result 和 active wakeup；不能完整扫描时只允许软删除。

4. **[moderate] 删除保护达到扫描上限时 fail-closed，可能阻断合法清理**
   - 证据：DAG 扫描达到 500 或某 DAG run 达到 100 会直接返回错误（`internal/module/memory/ui_rpc.go:458-520`）。
   - 风险：量化系统历史 run 多时，UI 清理任意 sharedfile 都可能失败，形成无法删除的积压文件；操作员会倾向绕过保护直接删磁盘或 DB。
   - 建议：使用按 path 反向索引或 SQL 查询引用；上限场景返回“需要更精确查询”而不是全局阻断。

5. **[moderate] promote sharedfile to memory 允许请求体 `Content` 覆盖原文件内容**
   - 证据：`promoteSharedFileToMemory()` 先读 sharedfile，但如果 `req.Content` 非空就用请求体内容而不是文件内容（`internal/module/memory/ui_rpc.go:530-549`）。测试 happy path 只覆盖未传 Content 时使用文件内容（`internal/module/memory/ui_rpc_merge_shared_shard19_test.go:430-477`）。
   - 风险：UI 或调用方可把一个可信量化报告路径“提升”为完全不同的 memory 内容，审计上看似来源于 sharedfile，实际内容可被替换。
   - 建议：promote 默认锁定源文件内容；若允许编辑，memory 记录应同时保存 original_path、original_hash 和 edited=true。

6. **[moderate] `_internal/auto-continue/state/<thread>.json` 删除只校验 path 形状，不校验调用方是否拥有该 thread**
   - 证据：`deleteAutoContinueState()` 校验 path 后直接 `SharedFilesDeleter.Delete()`（`internal/module/memory/ui_rpc_auto_continue_state.go:173-185`）。upsert 只校验 payload threadId 与 path threadId 一致（`internal/module/memory/ui_rpc_auto_continue_state.go:138-170`）。
   - 风险：如果 UI RPC 暴露给跨线程上下文，任意调用方可删除或覆盖其他 thread 的自动续命抑制状态，影响长量化任务继续/停止策略。
   - 建议：RPC 层绑定当前 thread/session，path threadId 必须等于授权上下文；删除也应做同样校验。

## 误报与已覆盖项

- agent 写入 `handoff/tasks/` 系统保留段会被拒绝（`cmd/mcp-orch/tools/shared_file_tools.go:87-98`；`cmd/mcp-orch/tools/parity_v2_test.go:281-300`）。
- MCP 写入有 10MB 内容上限，超过会在 Upsert 前拒绝（`cmd/mcp-orch/tools/shared_file_tools.go:13-16`、`cmd/mcp-orch/tools/shared_file_tools.go:91-93`）。
- dashboard retention 能把 run metadata 中的 final_output 标为 protected（`internal/module/dashboard/ui_page.go:315-347`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/memory ./internal/module/dashboard ./internal/contract -count=1
```

结果：通过。

## 下一轮建议

- Round 046 审查 command card store/tools、command card versioning、list/get/upsert/delete 以及 DAG automation 对 command_ref 的信任边界。
