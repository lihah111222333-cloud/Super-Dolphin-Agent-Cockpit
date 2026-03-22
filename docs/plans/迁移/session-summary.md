# V3 迁移会话摘要

> 生成时间：2026-03-22
> 会话范围：P0-P7 全程 + P7.5 桥接 + P8 前置 + V2↔V3 全面核对 + archtest 收官
> Claude 会话 UUID：db2f267a-2f6b-4de9-9109-775a788ac9b3
> 前序会话 UUID：e925f0b-eba0-49c6-82f9-c306ceae2956

---

## 1. 当前结论

### 编译验证：四项全绿
```
✅ go build ./...              — 0 errors
✅ go vet ./...                — 0 warnings
✅ lsp diagnostics             — 0 diagnostics
✅ archtest CodeSizeGuard      — 0 violations
✅ archtest DependencyDirection — PASS
```

### 迁移状态

| 阶段 | 状态 | 内容 |
|---|---|---|
| P0 骨架 | ✅ | 4 二进制 + platform 6 包 + contract/dto + Zone A/B 工厂 |
| P1 Store | ✅ | sqlc 96 方法 + 19 repo adapter + WrapStoreError 全覆盖 |
| P2 Event Bus | ✅ | kelindar/event + 6 族 typed event + 泛型工厂 |
| P3 状态机 | ✅ | 10 状态 + 11 触发器 + 严格模式 |
| P4 Provider | ✅ | unified + claudecli + codexapp + SessionManager lifecycle |
| P5 RPC | ✅ | 79+ handler + 全部 Blocker 清零 |
| P6 Wails | ✅ | RunDesktop + CallAPI/Dispatch + 原生绑定 + 双向 shutdown + event bridge |
| P7w1 V2 兼容 | ✅ | D1-D12 全部修复 + 互审 8 问题修复 |
| P7w2 Dashboard | ✅ | module/dashboard — agent 监控 + 系统信息 + 日志 + query |
| P7w2 UI State | ✅ | module/uistate — bus projector + 实时快照 + sidebar + preferences + projects |
| **P7.5 桥接校准** | ✅ | **~24 handler 补建 + 4 事件桥接 + knownDiffRevision + 安全加固** |
| **P8 前置** | ✅ | **D-1 sqlc 漂移 + D-2 runtime 上报 + D-3 状态机封堵 + dbquery 执行器** |
| **V2↔V3 核对** | ✅ | **21 模块 1:1 核对 + 修复 + 互审，排除 MCP 后残留归零** |
| **archtest 收官** | ✅ | **35+6 项违规全部修复；现有守卫全绿，但尚未覆盖 MCP 新依赖方向规则** |
| P8 编排工具 | ⏳ | 把 `cmd/mcp-orch/orchestration/*`、相关 `internal/store/*` 和依赖的 `internal/store/sqlc/*` / `sql/queries/*.sql` 迁到独立 `cmd/mcp-orch` 服务；19 个可交付 + 1 个延后（`task_start_node`），计划已出；MCP 进程按共享服务建模，`agent_id` 从 tool call 参数传入 |
| P9 LSP 工具 | ⏳ | MCP LSP 工具独立服务（`cmd/mcp-lsp`），9 个工具，计划已出 |
| P10 工厂丰满 | ⏳ | Zone A 3.8%→目标 60%，计划已出 |

---

## 2. 本会话完成的重大任务

### 2.1 P8 前置任务（D-1/D-2/D-3）
- D-1：sqlc 漂移修复（threadbinding + ailog 穿透 dashboard + store 测试）
- D-2：AgentSnapshot runtime 上报（DTO + UpdateRuntime + strict decode + 变更检测 + 事件去重）
- D-3：状态机封堵 + dbquery 安全执行器（白名单 24 表 + 危险函数 15+ + CTE 防绕过 + float64 归一化 + 10s timeout + 10000 行上限）
- 经历 3 轮：实现 → 互辩 → 互审修复 → 终检
- P8 交接 1：`cmd/mcp-orch/orchestration/*` 将整体迁到 `cmd/mcp-orch/orchestration/*`；MCP binary 自己持有 agent runtime、report、runtime snapshot 与 DAG
- P8 交接 2：相关 `store/*` 与 `store/sqlc/*` 也要本地化到 `cmd/mcp-orch`；运行时只共享 `config/db/contract/dto`
- P8 交接 3：候选 store 包必须先做 xref 审计再决定删除；当前基线下 `taskdag`、`binding`、`workspace`、`prompt`、`commandcard`、`sharedfile` 都是 copy+keep
- P8 交接 4：`prompts/list|write|delete` 仍被前端页面直接使用，P8 不能为迁 MCP 把 prompt UI surface 搬空

### 2.2 P7.5 前后端桥接校准
- 补建 ~24 个 RPC handler：config/read, lspPromptHint, prompts/*, ui/code/*, ui/copyText, ui/selectProjectDir(s), ui/openNewWindow, ui/projects/*, lsp/gui_*
- 4 个 V3 事件缺口桥接：thread/tokenusage→patch, thread/compacted, skills/changed(防抖), ui/thread/patch
- knownDiffRevision 参数接入 + scopeParams 修复
- 安全加固：路径沙箱、scope 白名单、save 拒绝新建、大文件保护、headless 降级、projects 加锁去重
- prompts/write 事务化 + 内容长度限制
- 文档：p7.5-bridge-calibration.md 经过 3 轮审查+修正

### 2.3 V2↔V3 全面核对
- 21 个 Codex Agent 覆盖全部模块（thread/turn/approval/orch/workspace/skill/dashboard/uistate/provider×3/rpc/bus/store/wails/fx）
- V2 代码路径：/Users/mima0000/Desktop/wj/go-agent-v2/
- 初始核对：9✅ / 40⚠️ / 56❌
- 修复后（排除 MCP）：残留归零
- 报告：docs/plans/迁移/v2v3-final-report.md

### 2.4 archtest 守卫收官
- 初始违规：35 项代码守卫 + 6 项依赖方向 = 41 项
- 修复方式：3 轮 Agent 修复 + 互审
  - 第一轮：5 Agent 修复主要违规
  - 互审发现 3 个编译阻塞（skill/orch/uistate 重复定义）
  - 第二轮：5 Agent 修编译阻塞 + 守卫超标
  - 第三轮：1 Agent 修最后 5 项
- sqlc 生成代码豁免（guardlib.go SkipDir）
- unified/event_map.go CC=46 → table-driven dispatch
- 最终状态：现有规则下 0 violations + PASS
- 待补守卫 1：`cmd/mcp-* -> internal/*` 单向依赖检查
- 待补守卫 2：`cmd/mcp-*` 之间禁止交叉 import
- 待补守卫 3：`internal/*` 禁止反向 import `cmd/mcp-*`

### 2.5 基础设施
- run-debug.sh 适配 V3（4 二进制、archtest 替代 code_size_guard、去除 Frida）
- debug 端口 4500/4501 → 20799/20800（与 V2 不冲突）
- LSP 高级工具指南：shared file prompts/lsp-advanced-guide.md + lsp-mandatory-prefix.md
- P10 执行计划：docs/plans/迁移/p10-execution-plan.md

---

## 3. 未完成

| 项 | 状态 | 说明 |
|---|---|---|
| MCP 问题写入 P8/P9 | ⏳ | V2↔V3 + P7.5 发现的 MCP 相关问题需归档到 p8/p9-execution-plan.md |
| **P8 编排工具族** | ⏳ | 整体迁移 `cmd/mcp-orch/orchestration/*`、相关 `internal/store/*` 与依赖 sqlc/query 到独立 `cmd/mcp-orch` 服务；候选 host store 先 xref 决定 copy+keep 还是迁移+删除；19 个可交付 + 1 个延后（`task_start_node`），按 copy/cleanup/adapt/server-wire 推进 |
| MCP 新依赖方向守卫 | ⏳ | 需补 `cmd/mcp-* -> internal/*` 单向、`cmd/mcp-*` 禁止互相 import、`internal/*` 禁止反向 import `cmd/mcp-*` |
| **P9 LSP 工具族** | ⏳ | 9 个工具，6+1 Agent，~12,500 行 |
| P10 工厂丰满 | ⏳ | Zone A 3.8%→60%，3 波次 |
| IDA 工具族 | ⏳ | 82 个工具，暂缓 |
| 前端功能测试 | ⏳ | run-debug.sh 已就绪，等手动启动验证 |

---

## 4. 下一步（优先级排序）

1. **MCP 问题归档** — 把 V2↔V3 和 P7.5 发现的 MCP 相关问题写入 P8/P9 文档
2. **手动启动测试** — 用 `./run-debug.sh` 选项 6（快速编译 agent-terminal）启动，验证 UI 可用性
3. **P8 编排工具族开工** — 读 p8-execution-plan.md，按“整体迁移 orchestration + store + sqlc，本地化后对 `internal/store/*` 零运行时依赖 + 19 个可交付 + 1 个延后（`task_start_node`）”推进
4. **P9 LSP 工具族开工** — 读 p9-execution-plan.md，拉 6+1 Agent
5. **P10 工厂丰满**（P8/P9 后）

---

## 5. 风险/延后项

| # | 问题 | 严重度 | 归期 |
|---|---|---|---|
| 1 | MCP 相关 V2↔V3 差异未归档 P8/P9 | P2 | 下一会话首任务 |
| 2 | MCP 新依赖方向 archtest 尚未落地 | P2 | P8/P9 前置 |
| 3 | lsp/gui_structure/inspect/xref 仍是 stub | P3 | P9 |
| 4 | knownDiffRevision 仍是 no-op（有 TODO P8） | P3 | P8 |
| 5 | ui/thread/patch 新字段前端不消费 | P3 | P8 前端适配 |
| 6 | thread/name/set provider 未接（有 TODO P8） | P3 | P8 |
| 7 | Claude reconnect 无生产路径（有 TODO P8） | P3 | P8 |
| 8 | sidebar_test 异步投影已修但 projector 仍偏重 | P3 | P10 |

---

## 6. 关键文档清单

| 文档 | 路径 |
|---|---|
| 主迁移计划 | docs/plans/迁移/v3-migration-plan.md |
| P7.5 桥接校准 | docs/plans/迁移/p7.5-bridge-calibration.md |
| P8 执行计划 | docs/plans/迁移/p8-execution-plan.md |
| P9 执行计划 | docs/plans/迁移/p9-execution-plan.md |
| P10 执行计划 | docs/plans/迁移/p10-execution-plan.md |
| V2↔V3 终极报告 | docs/plans/迁移/v2v3-final-report.md |
| 两级工厂方案 | docs/plans/迁移/v3-two-zone-dry-enrichment.md |
| LSP 高级指南 | shared file: prompts/lsp-advanced-guide.md |
| LSP 强制前缀 | shared file: prompts/lsp-mandatory-prefix.md |

---

## 7. Agent 使用统计

| 类型 | 数量 |
|---|---|
| 本会话累计拉起 Agent | ~150+ |
| V2↔V3 核对 | 21 |
| P7.5 实施+审查 | ~20 |
| P8 前置 | ~15 |
| archtest 修复 | ~15 |
| 两级工厂核查 | 10 |
| 其他（汇总/分类/文档） | ~10+ |

---

## 8. 最近 10 条对话摘要

| # | 用户指令 | 执行内容 |
|---|---|---|
| 1 | "进行 p8 前置任务安排" | 3 Explorer Agent 调研 → 3 Codex Agent 实施 D-1/D-2/D-3 |
| 2 | "P7.5 前后端桥接校准" | 发现 ~24 handler 缺失，文档+3 审查 Agent+3 轮互辩 |
| 3 | "派出 20 agent V2↔V3 核对" | 21 Agent 全模块核对，56❌/40⚠️/9✅ |
| 4 | "修复所有问题" | 21 Agent 原地修复，排除 MCP 后残留归零 |
| 5 | "P7.5 实施开工" | 4 Agent 补建 handler + 事件桥接 + 安全加固 |
| 6 | "archtest 修复" | 3 轮 5+5+1 Agent，从 41 违规降到 0 |
| 7 | "run-debug.sh 适配 V3" | 重写编译脚本，4 二进制 + archtest + 去 Frida |
| 8 | "debug 端口改 20800" | 8 文件改 4500/4501→20799/20800 |
| 9 | "终极裁定排除 MCP" | 3 裁定 Agent，50 项核查，残留 5→0 |
| 10 | "archtest 全绿收官" | go build/vet/diagnostics/archtest 四项全绿 |

---

## 9. 子 Agent 提示词模式

### LSP 强制指令（所有 Agent 追加）
```
先读 shared_file_read prompts/lsp-mandatory-prefix.md
完整指南在 prompts/lsp-advanced-guide.md
禁止只用 lsp_grep + lsp_file，每个任务至少 4 种 LSP 工具
```

### 仓库契约引用
```
守卫标准：文件 ≤400行，函数 ≤80行，CC ≤10，包非测试文件 ≤15
Zone B 模式：docs/plans/迁移/v3-two-zone-dry-enrichment.md §3
sqlc 生成代码豁免：internal/store/sqlc/ SkipDir
```

### 只用 Codex Agent（provider="codex"）
Claude Agent 禁用，所有子 Agent 走 orchestration_launch_agent(provider="codex")
