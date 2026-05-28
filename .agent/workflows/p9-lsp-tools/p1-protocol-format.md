---
description: Agent A — LSP 协议类型 + 输出格式化
---

# P1: Agent A — 协议+输出 (~1,040行, 9文件)

## 前置条件
- 无（第一批并行）

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/protocol/types.go` (~130) — LSP 基础类型
- `cmd/mcp-lsp/protocol/methods.go` (~40) — 方法常量
- `cmd/mcp-lsp/protocol/codec.go` (~130) — JSON-RPC 编解码
- `cmd/mcp-lsp/protocol/ext.go` (~130) — 扩展类型
- `cmd/mcp-lsp/protocol/notification.go` (~90) — 通知处理
- `cmd/mcp-lsp/format/display.go` (~200) — 坐标转换 0→1-based
- `cmd/mcp-lsp/format/compact.go` (~120) — compact 输出
- `cmd/mcp-lsp/format/funcrange.go` (~80) — func_start/func_end
- `cmd/mcp-lsp/format/render.go` (~120) — 渲染工具

### 禁止触碰的文件 ⚠️
- `edit/`, `gopls/`, `tools/`, `search/`, `exec/`, `middleware/`

## 关键常量
- `protocol/ext.go`: `XRefResultLimit = 50`
- `protocol/ext.go`: `SemanticTokenResultLimit = 200`
- `format/compact.go`: `lspReferencesCompactLimit = 30`
- `format/compact.go`: `lspCompletionCompactLimit = 20`
- `format/compact.go`: `lspWorkspaceSymbolCompactLimit = 20`

## 完成标准
- [ ] `go build ./cmd/mcp-lsp/protocol/... ./cmd/mcp-lsp/format/...` 通过
- [ ] 单文件 ≤400行，单函数 ≤80行
- [ ] 共识#3 func_start/func_end 实现在 funcrange.go
- [ ] 共识#4 display 0→1-based 坐标转换

## 验证命令
```bash
go build ./cmd/mcp-lsp/protocol/... ./cmd/mcp-lsp/format/...
go test ./cmd/mcp-lsp/protocol/... ./cmd/mcp-lsp/format/... -v
```
