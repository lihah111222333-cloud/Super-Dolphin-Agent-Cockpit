---
description: Agent C2 — gopls Manager 核心 + 符号查询
---

# P4: Agent C2 — 管理器核心 (~900行, 5文件)

## 前置条件
- [ ] P1 (Agent A) 完成 — 依赖 protocol/ 类型
- C1 通过 interface 解耦，C1/C2 可并行

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/gopls/manager.go` (~280) — Manager 主体 + workspace 路由
- `cmd/mcp-lsp/gopls/manager_lifecycle.go` (~130) — 生命周期 + ensureClient
- `cmd/mcp-lsp/gopls/manager_symbols.go` (~250) — 符号查询主路径
- `cmd/mcp-lsp/gopls/manager_symbols_fallback.go` (~250) — markdown/json/yaml fallback
- `cmd/mcp-lsp/gopls/gomod.go` (~90) — go.mod root 发现

### 禁止触碰的文件 ⚠️
- `gopls/client.go`, `gopls/transport.go` (C1负责)
- `gopls/bootstrap_doc.go`, `gopls/state.go`, `gopls/pool.go`, `gopls/recycler.go`, `gopls/cache.go` (E负责)
- `protocol/`, `format/`, `edit/`, `tools/`, `search/`, `exec/`, `middleware/`

## 关键输出：Manager interface
C2 必须首先定义并导出 `type Manager interface`，包含 D/E/F/G 需要的所有方法：
- `EnsureClient` / `Close`
- `Definition` / `Implementation` / `TypeDefinition` / `Hover` / `SignatureHelp`
- `References` / `CallHierarchy` / `TypeHierarchy`
- `DocumentSymbol` / `WorkspaceSymbol` / `FoldingRange` / `SemanticTokens`
- `Completion`
- `Rename` / `CodeAction` / `Format`
- `DidOpen` / `DidChange` / `DidClose`
- `BootstrapDocument`
- `Diagnostics` / `WaitDiagnosticsStable`
- `CurrentDiagnosticGeneration` / `AdvanceDiagnosticGeneration`

## 关键共识
- 共识#1: ensureClient 双重检查锁
- 共识#7: markdown/json/yaml symbol fallback

## 验证命令
```bash
go build ./cmd/mcp-lsp/gopls/...
go test ./cmd/mcp-lsp/gopls/... -run TestManager -v
```
