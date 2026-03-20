# P7 执行计划 — MCP 工具层 + V2 完整兼容收尾

> 生成时间：2026-03-21
> 前置：P0-P6 全部收官，0 Blocker，编译守卫全绿

---

## 1. 目标

将 V3 从"核心 RPC 等价"推进到"V2 完整功能等价"，覆盖：
- MCP 三族工具 server（lsp / orch / ida）
- V2 兼容性收尾（12 项推迟问题）
- Dashboard / UI State 模块
- Skills runtime + workspace 工具层

---

## 2. 推迟项清单（来自终审互审）

### 2.1 当场已修（本轮）
| # | 问题 | Agent |
|---|---|---|
| F1 | agent.submit* payload 丢 SelectedSkills/ManualSkillSelection/OutputSchema | fix-hotfix-submit |
| F2 | runner nil 返回导致 runtime 半死 | fix-hotfix-runner |

### 2.2 P7 范围

| # | 问题 | 模块 | 预估 | 优先级 |
|---|---|---|---|---|
| D1 | approval pending 只按 callID 去重，缺 requestId 维度 | platform/rpc/approval | 小 | P2 |
| D2 | turn/interrupt wire contract V2 漂移 | module/turn | 中 | P2 |
| D3 | MergeRun(dryRun) 仍是 TODO | module/workspace | 中 | P3 |
| D4 | approval callback method V2 不兼容 + request_user_input 桥接 | platform/rpc/approval | 中 | P2 |
| D5 | store 层错误处理无统一包装 | internal/store | 中 | P3 |
| D6 | orchestration/report requester 归并最小内存版 | module/orchestration | 大 | P3 |
| D7 | claudecli Configure 不影响运行中 session | provider/claudecli | 中 | P3 |
| D8 | claudecli capability 声明不一致（context_compact/turn_override） | provider/claudecli | 小 | P2 |
| D9 | ReadHistory 两 driver metadata 退化 | provider/claudecli+codexapp | 中 | P3 |
| D10 | codexapp/recovery.go 未接线 | provider/codexapp | 中 | P3 |
| D11 | json tag lowerCamelCase vs snake_case 全量治理 | dto | 大 | P2 |
| D12 | EventHeader "9层零重复" 不满足 | dto | 小 | P3 |

---

## 3. MCP 三族工具 Server

### 3.1 mcp-lsp（LSP 工具族）

| 工具 | 说明 | V2 参考 |
|---|---|---|
| lsp/diagnostics | 代码诊断 | internal/mcp/lsp/ |
| lsp/hover | 悬停信息 | |
| lsp/definition | 跳转定义 | |
| lsp/references | 引用查找 | |
| lsp/completion | 代码补全 | |
| lsp/rename | 重命名 | |
| lsp/format | 格式化 | |
| lsp/code_action | 代码操作 | |
| lsp/grep | 文本/AST 搜索 | |
| lsp/structure | 符号/大纲 | |
| lsp/file | 文件读写 | |
| lsp/edit | 编辑操作 | |

**当前状态**：mcp-lsp binary 已存在（cmd/mcp-lsp），内部实现待迁移
**预估**：~2000 行（V2 pkg/toolsdk/lsp/ 约 3000 行，V3 精简后）

### 3.2 mcp-orch（编排工具族）

| 工具 | 说明 |
|---|---|
| orchestration/launch_agent | 启动子 Agent |
| orchestration/stop_agent | 停止 Agent |
| orchestration/list_agents | 列出 Agent |
| orchestration/send_message | 发消息 |
| orchestration/get_agent_report | 获取报告 |

**当前状态**：mcp-orch binary 已存在，内部实现待迁移
**预估**：~500 行

### 3.3 mcp-ida（IDA 工具族）

| 工具 | 说明 |
|---|---|
| ida/analyze | 逆向分析 |
| ida/decompile | 反编译 |
| ida/symbols | 符号表 |

**当前状态**：mcp-ida binary 已存在，内部实现待迁移
**预估**：~800 行

---

## 4. Dashboard / UI State

| 模块 | 内容 | V2 参考 |
|---|---|---|
| uistate | 前端状态同步（thread list/agent list/turn status） | internal/uistate/ |
| dashboard | 监控面板（agent 健康/性能/日志） | internal/dashboard/ |

**预估**：~1500 行

---

## 5. 执行波次

### 波次 1：V2 兼容收尾（D1-D12）
- 3 Agent 并行：approval+turn (D1/D2/D4) / dto+store (D5/D11/D12) / provider (D7/D8/D9/D10)
- D3/D6 较复杂可推到波次 2

### 波次 2：MCP 工具族
- 3 Agent 并行：mcp-lsp / mcp-orch / mcp-ida
- 每个 Agent 独立 binary，完全并行

### 波次 3：Dashboard + UI State
- 2 Agent 并行：uistate / dashboard

### 波次 4：集成测试 + 最终验证
- 全量 handler 注册完整性测试（目标 151 方法全注册）
- V2 契约测试迁移
- smoke test

---

## 6. 代码守卫
每文件 ≤ 400 行，每函数 ≤ 80 行，CC ≤ 10

## 7. Done 标准
- V3 handler 数 ≥ V2 151 方法
- 全部 MCP 工具可用
- Dashboard SSE 可推送
- go build + go vet + archtest 全绿
- 迁移覆盖率 ≥ 95%
