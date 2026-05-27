---
description: Agent S — cmd/mcp-lsp 薄装配层扩展
---

# P0: Agent S — 骨架 (~210行)

## 前置条件
- 无（第一批并行）

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/runtime.go` (~50行) — 启动关闭编排
- `cmd/mcp-lsp/tools.go` (~40行) — 9 工具注册表

## 关键输出要求
- `tools.go` 必须注册 9 个精确工具名：`lsp_file`, `lsp_inspect`, `lsp_xref`, `lsp_grep`, `lsp_structure`, `lsp_edit`, `lsp_completion`, `code_run`, `code_run_test`
- 必须定义 handler 类型签名：`type ToolHandler func(ctx context.Context, params json.RawMessage) (any, error)`，供 D/F/G 统一使用
- 所有 stub handler 必须返回 `{"error": "not implemented"}`
- Server 装配必须使用 `registryToolProvider + common.NewServer` 模式（参考 `cmd/mcp-orch`）
- `fx.go` 扩展后总行数守卫：已有约133行 + 新增约104行 = 约237行，必须 ≤400行

### 需要修改的文件
- `cmd/mcp-lsp/main.go` (新增~16行)
- `cmd/mcp-lsp/fx.go` (新增~104行 LSP 绑定)

### 禁止触碰的文件 ⚠️
- `cmd/mcp-lsp/` 下所有文件（其他 Agent 负责）

## 执行步骤
1. 读取 `docs/plans/迁移/p9-implementation-plan.md` §6.4 Agent S 详细文件清单
2. 用 LSP 读取 `cmd/mcp-lsp/fx.go` 和 `main.go` 现有代码
3. 新建 `runtime.go` — graceful shutdown 编排
4. 新建 `tools.go` — 9 工具 name→handler 映射表 + `ToolHandler` 类型 + stub handlers
5. 扩展 `fx.go` — 添加 Manager/ToolHandlers/Server fx.Provide，并按 `registryToolProvider + common.NewServer` 模式装配
6. 扩展 `main.go` — 添加 LSP 相关调用

## 完成标准
- [ ] `go build ./cmd/mcp-lsp/...` 通过
- [ ] 不修改 internal/ 下任何文件
- [ ] fx.go 保持已有 run()/newBootstrapRunner()/bindRuntime() 不变
- [ ] `fx.go` 扩展后总行数 ≤400

## 验证命令
```bash
go build ./cmd/mcp-lsp/...
go vet ./cmd/mcp-lsp/...
```
