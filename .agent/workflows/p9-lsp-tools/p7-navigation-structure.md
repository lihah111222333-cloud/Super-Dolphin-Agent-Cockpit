---
description: Agent F — 导航+结构+补全工具
---

# P7: Agent F — 导航+结构+补全 (~1,060行, 4文件)

## 前置条件
- [ ] P4 (Agent C2) 完成 — 依赖 Manager
- [ ] P1 (Agent A) 完成 — 依赖 format/ + protocol/

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/tools/tool_inspect.go` (~260) — hover/definition/impl/typedef
- `cmd/mcp-lsp/tools/tool_xref.go` (~280) — references/call_hierarchy/type_hierarchy
- `cmd/mcp-lsp/tools/tool_structure.go` (~300) — document_symbol/workspace_symbol
- `cmd/mcp-lsp/tools/tool_completion.go` (~220) — compact/full 补全

### 禁止触碰的文件 ⚠️
- `tools/tool_file.go`, `tool_grep.go`, `tool_diagnostics.go` (D负责)
- `tools/tool_edit*.go`, `tool_coderun*.go` (G负责)

## 关键共识
- 共识#3: func_start/func_end 必须包含
- 共识#4: display 坐标转换
- 共识#7: markdown/json/yaml symbol fallback（`document_symbol` 需支持 markdown/json/yaml 非 LSP fallback）

## 验证命令
```bash
go test ./cmd/mcp-lsp/tools/... -run "TestInspect|TestXref|TestStructure|TestCompletion" -v
```
