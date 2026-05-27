---
description: Agent D — 文件+搜索+诊断工具
---

# P5: Agent D — 文件+搜索+诊断 (~1,220行, 6文件)

## 前置条件
- [ ] P4 (Agent C2) 完成 — 依赖 Manager 接口

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/tools/tool_file.go` (~270) — lsp_file handler
- `cmd/mcp-lsp/tools/tool_grep.go` (~220) — lsp_grep handler
- `cmd/mcp-lsp/tools/tool_diagnostics.go` (~200) — diagnostics 子handler
- `cmd/mcp-lsp/search/fileutil.go` (~210) — 安全门 + 文件读取
- `cmd/mcp-lsp/search/searchutil.go` (~220) — 搜索匹配
- `cmd/mcp-lsp/middleware/budget.go` (~100) — 输出预算兜底

### 禁止触碰的文件 ⚠️
- `tools/tool_inspect.go`, `tool_xref.go`, `tool_structure.go`, `tool_completion.go` (F负责)
- `tools/tool_edit*.go`, `tool_coderun*.go` (G负责)
- `middleware/logging.go`, `middleware/recovery.go`, `middleware/timeout.go` (G负责)

## 关键共识
- 共识#2: 截断必须工具级
- 共识#7: markdown/json/yaml symbol fallback（`lsp_grep` 的 `ast_search` 依赖此能力）
- 共识#10: diagnostics waitStable (80ms/40ms/800ms) + generation tracking

## 验证命令
```bash
go build ./cmd/mcp-lsp/tools/... ./cmd/mcp-lsp/search/... ./cmd/mcp-lsp/middleware/...
go test ./cmd/mcp-lsp/tools/... -run "TestFile|TestGrep|TestDiag" -v
```
