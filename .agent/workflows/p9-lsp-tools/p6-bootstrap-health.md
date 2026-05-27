---
description: Agent E — Bootstrap + 进程池 + 缓存
---

# P6: Agent E — Bootstrap+健康 (~1,030行, 5文件)

## 前置条件
- [ ] P4 (Agent C2) 完成 — 依赖 Manager 接口

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/gopls/bootstrap_doc.go` (~280) — BootstrapDocument + sibling
- `cmd/mcp-lsp/gopls/state.go` (~210) — bootstrap 状态机
- `cmd/mcp-lsp/gopls/pool.go` (~180) — 进程池
- `cmd/mcp-lsp/gopls/recycler.go` (~180) — RSS 监控 + 回收
- `cmd/mcp-lsp/gopls/cache.go` (~180) — cache_store

### 禁止触碰的文件 ⚠️
- `gopls/client.go`, `gopls/transport.go` (C1负责)
- `gopls/manager*.go`, `gopls/gomod.go` (C2负责)

## 关键共识
- 共识#8: cache_store 内存默认 + env-gated persistent
- 共识#9: 池回收 + RSS 跨平台监控
- 共识#11: bootstrap + sibling bootstrap (cap=20)

## 验证命令
```bash
go build ./cmd/mcp-lsp/gopls/...
go test ./cmd/mcp-lsp/gopls/... -run "TestBootstrap|TestPool|TestRecycler|TestCache" -v
```
