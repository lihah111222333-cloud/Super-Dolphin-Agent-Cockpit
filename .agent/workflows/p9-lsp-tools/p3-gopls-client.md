---
description: Agent C1 — gopls LSP 客户端封装
---

# P3: Agent C1 — gopls 客户端 (~580行, 2文件)

## 前置条件
- [ ] P1 (Agent A) 完成 — 依赖 protocol/ 类型

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/gopls/client.go` (~360) — LSP Client: initialize/shutdown + request/notify
- `cmd/mcp-lsp/gopls/transport.go` (~220) — stdio JSON-RPC 传输

### 禁止触碰的文件 ⚠️
- `gopls/manager.go`, `gopls/manager_lifecycle.go`, `gopls/manager_symbols.go`, `gopls/manager_symbols_fallback.go`, `gopls/gomod.go` (C2负责)
- `gopls/bootstrap_doc.go`, `gopls/state.go`, `gopls/pool.go`, `gopls/recycler.go`, `gopls/cache.go` (E负责)
- `protocol/`, `format/`, `edit/`, `tools/`, `search/`, `exec/`, `middleware/`

## 核心契约
- 必须导出 `Client interface`，包含 `Initialize`、`Shutdown`、`Request`、`Notify`、`DidOpen`、`DidChange`、`DidClose`、`Close`
- transport 需要具备 `pending map`、`clearPending`、write-lock，以及 process-kill 语义
- capability negotiation 必须覆盖 11+1 capabilities

## 关键共识
- 共识#1: 懒启动 (ensureClient 双重检查锁) — client 不预启动

## 验证命令
```bash
go build ./cmd/mcp-lsp/gopls/...
go test ./cmd/mcp-lsp/gopls/... -run TestClient -v
```
