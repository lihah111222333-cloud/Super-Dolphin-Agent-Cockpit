# P9 执行计划 v2 — MCP LSP 工具族（9 个）

> 修订：2026-03-21（V2 LOC 沿用 2026-03-21 审查口径；当前 V3 状态已回刷）

---

## 1. V2 体量（沿用 2026-03-21 审查口径）

| 范围 | 文件数 | 生产代码 | 含测试 |
|---|---:|---:|---:|
| pkg/toolsdk/lsp/ | 111 | **10,876** | 33,117 |
| pkg/toolsdk/tools/ LSP 相关 | 12 | 881 | 2,205 |
| pkg/toolsdk/tooladapter/ | 19 | 1,083 | 5,429 |
| internal/mcp/ wiring | 10 | 1,303 | 2,243 |
| **总计** | **152** | **14,143** | **42,994** |

V2 最重的生产文件：
- tool_handlers_edit.go: 1,264 行
- tool_handlers_core.go: 1,040 行
- tool_handlers_file.go: 764 行
- protocol_ext_common.go: 702 行
- tool_handlers_search.go: 670 行
- client.go: 610 行
- manager.go: 577 行

## 2. 当前状态与 V3 预估（修正后）

> 注：上表 V2 LOC 沿用 `audit-mcp-lsp-tools.md` 的 2026-03-21 审查口径；当前仓库不包含独立第二统计源。
> 当前 V3 状态：`cmd/mcp-lsp` 仍是空 `fx.New(...)` 壳，`internal/mcpserver/common` 仍只有 `server.go` / `manifest.go` / `stdio.go`。

| 口径 | 乐观 | 中位 | 悲观 |
|---|---:|---:|---:|
| V3 生产代码 | 10,000 | **12,500** | 15,000 |
| V3 交付总量（含测试） | 18,000 | **24,000** | 32,000 |

**之前估 ~1000 行是数量级错误。**

## 3. 按工具拆分

| Tool | V2 行数 | 复杂度 |
|---|---:|---|
| lsp_file | 1,190 | 复杂 |
| lsp_inspect | 2,730 | 复杂 |
| lsp_xref | 1,932 | 复杂 |
| lsp_grep | 1,096 | 中 |
| lsp_structure | 1,725 | 复杂 |
| lsp_edit | 2,495 | 复杂 |
| lsp_completion | 635 | 中 |
| code_run | 612 | 复杂 |
| code_run_test | 612 | 中 |

## 4. Agent 拆分（6 实现 + 1 验证）

| Agent | 范围 | 预估行数 |
|---|---|---:|
| 1. common/wiring | cmd/mcp-lsp + mcpserver/common + fx | 600-900 |
| 2. protocol/client/manager | LSP protocol + gopls client + 进程池 + 健康检查 | 2,400-3,500 |
| 3. file/search | lsp_file + lsp_grep + 分页/batch/fallback | 1,100-1,700 |
| 4. inspect/xref/structure | lsp_inspect + lsp_xref + lsp_structure + lsp_completion | 1,500-2,250 |
| 5. edit/replace_range | lsp_edit 全量 + patch parser | 2,100-3,200 |
| 6. code_run | code_run + code_run_test + sandbox/timeout/approval | 900-1,400 |
| 7. verification | 测试 + schema snapshot + regression | 2,000-3,000 |

## 5. 关键风险

- gopls 进程管理（bootstrap/health/restart）是最重的层 ~2000 行
- AST grep sg 后端探测/fallback ~670 行
- code_run 安全沙箱（危险命令拦截/approval/timeout）
- 大文件分页/截断/预算限制
- replace_range patch 解析器 + 上下文定位

## 6. 代码守卫
每文件 ≤ 400 行，每函数 ≤ 80 行，CC ≤ 10

## 附：V2↔V3 / P7.5 核对发现的 P9 相关问题

| # | 问题 | 来源 | 影响 |
|---|---|---|---|
| V-6 | lsp/gui_structure、gui_inspect、gui_xref 仍是 stub | p7.5-lsp-gui | P9 GUI LSP 工具缺口 |
| V-7 | lsp/gui_grep ast_search 空实现 | p7.5-lsp-gui | P9 AST 搜索缺口 |
| V-8 | diagnostics stub | p7.5-wails-binding | P9 LSP diagnostics 缺口 |
| V-9 | provider reconnect/recovery 弱化 | v2v3-provider-reconnect | MCP session 恢复韧性 |
| V-10 | lsp/gui_structure、gui_inspect、gui_xref stub 标记前端不消费 | p7.5-r2-lsp-gui | 真实 LSP 后端 + 前端 stub 判断 |
| V-11 | lsp/gui_grep ast_search 空实现且返回空结果无标记 | p7.5-r2-lsp-gui | AST 搜索引擎 |
| V-12 | lsp/gui_file diagnostics 是空 stub | p7.5-r2-lsp-gui | gopls diagnostics 接入 |
| V-13 | gui_grep glob 只匹配 basename 不匹配路径 | p7.5-r2-lsp-grep | 路径级 glob |
| V-14 | gui_grep 手搓 WalkDir 非 LSP 索引 | p7.5-r2-lsp-grep | 真实 LSP 索引查询 |
