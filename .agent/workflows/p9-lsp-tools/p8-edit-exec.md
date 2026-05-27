---
description: Agent G — 编辑+执行+胶水 ★关键路径
---

# P8: Agent G — 编辑+执行+胶水 (~1,420行, 8文件) ★关键路径

## 前置条件
- [ ] P4 (Agent C2) 完成 — 依赖 Manager
- [ ] P2 (Agent B) 完成 — 依赖 edit/ patch 引擎

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/tools/tool_edit.go` (~280) — rename/code_action/format
- `cmd/mcp-lsp/tools/tool_edit_replace.go` (~280) — replace_range 多 hunk
- `cmd/mcp-lsp/tools/tool_coderun.go` (~200) — code_run handler
- `cmd/mcp-lsp/tools/tool_coderuntest.go` (~220) — code_run_test handler
- `cmd/mcp-lsp/exec/sandbox.go` (~220) — 命令执行+超时
- `cmd/mcp-lsp/middleware/logging.go` (~90) — 请求日志
- `cmd/mcp-lsp/middleware/recovery.go` (~60) — panic 恢复
- `cmd/mcp-lsp/middleware/timeout.go` (~70) — 超时控制

### 禁止触碰的文件 ⚠️
- `tools/tool_file.go`, `tool_grep.go`, `tool_diagnostics.go` (D负责)
- `tools/tool_inspect.go`, `tool_xref.go`, `tool_structure.go`, `tool_completion.go` (F负责)
- `edit/` (B负责), `middleware/budget.go` (D负责)

## 关键共识
- 共识#5/#6: patch + seek_sequence（调用 edit/ 包，不重新实现）
- 共识#12: code_run 审批不在 LSP 层

## 跨 Agent 接口约定
- `diagGeneration atomic.Uint64` 放在 C2 的 Manager 上
- G 的 `tool_edit.go` 中 didChange 后不推进 generation（V2 行为）
- G 通过 Manager interface 的 `CurrentDiagnosticGeneration()` 访问

## 验证命令
```bash
go test ./cmd/mcp-lsp/tools/... -run "TestEdit|TestCodeRun" -v
go test ./cmd/mcp-lsp/exec/... -v
go test ./cmd/mcp-lsp/middleware/... -v
```
