# V3 迁移会话摘要

> 生成时间：2026-03-24（P8.5 lifecycle hooks 完整落地更新）
> 会话范围：P0-P8.5 全程 + P7.5 桥接 + V2↔V3 核对 + archtest 收官 + MCP 独立服务 + ctl/* 回调框架 + lifecycle hooks 完整实现
> Claude 会话 UUID：58fdd978-cc4b-41e6-bd26-d40f3ff66854
> 前序会话 UUID：ea3ad84e-7b52-422d-bc46-cff9da3ea9f9

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
| **P8 编排工具** | ✅ | **orchestration 整体迁移到独立 cmd/mcp-orch 服务，19 个 MCP tool handler，stdio server 可用，共享模式（per-binary），ctl/* 回调框架 14 方法** |
| **P8.5 ctl 回调框架** | ✅ | **ctl/* 回调框架：14 方法（基线 9 + hook 扩展 5），注册表 + bootstrap client + 3 binary 接入** |
| **P8.5 lifecycle hooks** | ✅ | **hooks 核心基础设施完整落地：7 层 transport 链路（DTO/handler/fanout/核心逻辑/契约/bootstrap/store）+ selector 交集 + 重入防护 + subscriber_lost 自动取消 + shutdown 竞态 + env 统一 + config/changed 发送端 + 接口拆分 + 覆盖率 85.1%** |
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

### 2.5 P8 编排工具族迁移（本会话核心成果）
- orchestration 整体迁移到 cmd/mcp-orch/orchestration/（22 文件），internal/module/orchestration/ 已删除
- 6 个 store 迁移到 cmd/mcp-orch/store/（taskdag/taskack/workspace/prompt/commandcard/sharedfile）
- sqlc 生成链独立：cmd/mcp-orch/sqlc.yaml + sql/queries/ + sqlc generate
- 19 个 MCP tool handler（V2 parity + nil guard + requireTrimmed + workspaceRunDTO）
- stdio server 接线：tools/list + tools/call 通过 stdin/stdout 可用
- 共享模式：per-binary 服务所有 agent，agent_id 从 tool call 参数传入
- 核心层只提供 ctl/* RPC 接口，不启动不托管 MCP 进程

### 2.6 P8.5 ctl/* 回调框架
- 14 方法（基线 9 + hook 扩展 5）
- LeaseKey + peer_kind + client_kind + 核心侧注册表 + 工具侧 bootstrap client
- 3 个 binary 接入，经 3 Codex + 1 Claude 四方评审 + 两轮互辩

### 2.7 核心层架构原则
- 核心层只做：Agent 管理、工具管理（manifest）、Hooks（事前/事中/事后）、提供 ctl/* 接口
- Hooks 支持工具可见性控制（AllowedTools/DeniedTools）
- MCP 进程是共享服务，核心层不启动不管理

### 2.8 基础设施
- run-debug.sh 适配 V3（4 二进制、archtest 替代 code_size_guard、去除 Frida）
- debug 端口 4500/4501 → 20799/20800（与 V2 不冲突）
- LSP 高级工具指南：shared file prompts/lsp-advanced-guide.md + lsp-mandatory-prefix.md
- P10 执行计划：docs/plans/迁移/p10-execution-plan.md

### 2.9 P8.5 Lifecycle Hooks 完整实现（本会话核心成果）

**产出统计**：19 文件、~3,500 行代码、覆盖率 85.1%、35+ 测试用例

**7 层 transport 链路全量落地**：
- 契约层：`contract/hooks.go`（HookManager 6 方法 + HookReviewStore 8 方法 + HookLifecycle + PeerCallback）
- DTO 层：`dto/mcp/hook.go`（7 类型 + Depth 字段）+ `constants.go`（5 方法常量 + 10 决策常量）
- 核心层：`platform/hooks/` 11 文件（registry + dispatcher + merge + resolver + manager + points + module）
- 持久化层：`store/hookstore/`（migration + 8 方法 + GetResolvedReview + subscriber_lease）
- Handler 接线：`mcpcontrol/handlers.go`（subscribe/resolve）+ `router.go`（before/check/after fanout + selector 交集）
- Bootstrap 消费：`bootstrap/hooks.go`（callback 接收 + subscribe 发起 + reconnect 重放）
- 架构守卫：rchtest rule13/14 双向隔离

**关键特性**：
- Before/During/After 三阶段模型（fail-closed / fail-open）
- 多订阅方决策合并（优先级排序 + AllowedTools 交集 / DeniedTools 并集）
- escalate → pending_hook_review → resolve 幂等收敛
- hook 重入防护（maxHookDepth=3）
- subscriber_lost 自动取消（连续 3 次失败 + ForgetLease + CancelByLease）
- shutdown 竞态处理（HookLifecycle.ShutdownHooks: Unsubscribe → ForgetLease → CancelByLease）
- Selector 交集查询（IntersectTargets 最小桶算法 + 6 维度索引）
- hook dispatch selector 穿透（payload AgentID/ThreadID 自动派生 Selector.Scope）
- ctl/config/changed 发送端（bus 事件桥接 + configVersion 递增）
- env 统一（GO_AGENT_MCP_* → GO_AGENT_CTL_* + deprecation 2026-06-30）
- ToolRegistry 接口拆分（ToolRegistry / ToolNotifier / ToolHookCallback / PeerCallback / ToolControlPlane）
- PeerCallback fx 装配层桥接（app/modules.go）
- Manager slog 注入（WithManagerLogger + optional fx 注入）

**审查流程**：初审 → 复审 → 二审互辩 → 五方终审 → 1:7 互审（含代码优雅度）→ 设计债务收敛

---

## 3. 未完成

| 项 | 状态 | 说明 |
|---|---|---|
| ~~lifecycle hooks 代码实现~~ | ✅ | 已完成：7 层 transport + selector + 重入防护 + subscriber_lost + shutdown 竞态 + 覆盖率 85.1% |
| ~~ctl/config/changed 发送端~~ | ✅ | 已完成：bus 事件桥接 + configVersion 递增 + Selector.Scope 填充 |
| ~~env 统一~~ | ✅ | 已完成：GO_AGENT_MCP_* → GO_AGENT_CTL_* 读写两端 + deprecation 日志 |
| ~~hooks 集成测试~~ | ✅ | 已完成：6 个场景（merge/escalate/depth/lost/shutdown/scoped） |
| TestTimeoutLocality | ⚠️ | 预存债务 12 处，hooks/dispatcher.go 已修复脱离违规列表 |
| DAG SQL 适配 | ⏳ | cmd/mcp-orch/store/taskdag/ 10 个接口重写，属 MCP 工具侧消费，非核心层 |
| **P9 LSP 工具族** | ⏳ | 9 个工具，6+1 Agent，~12,500 行 |
| P10 工厂丰满 | ⏳ | Zone A 3.8%→60%，3 波次 |
| IDA 工具族 | ⏳ | 82 个工具，暂缓 |
| 前端功能测试 | ⏳ | run-debug.sh 已就绪，等手动启动验证 |

---

## 4. 下一步（优先级排序）

1. **P9 LSP 工具族** — 读 p9-execution-plan.md，cmd/mcp-lsp 9 个工具，6+1 Agent
2. **P10 工厂丰满** — Zone A 3.8%→60%，3 波次
3. **DAG SQL 适配** — cmd/mcp-orch/store/taskdag/ 10 个接口重写（工具侧，非核心层）

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
| P8.5 ctl 回调框架 | docs/plans/迁移/p8.5-execution-plan.md |
| P8 lifecycle hooks | docs/plans/迁移/p8-lifecycle-hooks.md |
| P8 handler 审查 | docs/plans/迁移/p8-handler-review-1.md + p8-handler-review-2.md |
| MCP 服务契约 | docs/契约/mcp-service-convention.md |
| 会话习惯 | docs/会话习惯.md |
| P9 执行计划 | docs/plans/迁移/p9-execution-plan.md |
| P10 执行计划 | docs/plans/迁移/p10-execution-plan.md |
| V2↔V3 终极报告 | docs/plans/迁移/v2v3-final-report.md |
| 两级工厂方案 | docs/plans/迁移/v3-two-zone-dry-enrichment.md |
| LSP 高级指南 | shared file: prompts/lsp-advanced-guide.md |
| LSP 强制前缀 | shared file: prompts/lsp-mandatory-prefix.md |
| P8.5 Hooks 审查报告 | docs/plans/迁移/p8.5-hooks-review.md |

---

## 7. Agent 使用统计

| 类型 | 数量 |
|---|---|
| 本会话累计拉起 Agent | ~350+ |
| P8 编排迁移+审查 | ~80 |
| P8.5 ctl 框架+审查 | ~40 |
| P8 handler+parity | ~30 |
| P8 共享模式+stdio | ~25 |
| P8 死代码扫描 | ~15 |
| 文档/lifecycle hooks | ~20+ |
| **P8.5 hooks 实现+审查+修复** | **~60** |
| **P8.5 hooks 设计债务收敛** | **~15** |
| V2↔V3 核对 | 21 |
| P7.5 实施+审查 | ~20 |
| P8 前置 | ~15 |
| archtest 修复 | ~15 |
| 两级工厂核查 | 10 |
| 其他（汇总/分类） | ~10+ |

---

## 8. 最近 10 条对话摘要

| # | 用户指令 | 执行内容 |
|---|---|---|
| 1 | "hooks 审查文档 + 五方终审" | p8.5-hooks-review.md 三轮审查（初审→复审→二审互辩→五方终审），12 项问题清单 |
| 2 | "T0 前置任务 6 并行" | contract/hooks + DTO + constants + hookstore + bootstrap/hooks + archtest rule13/14 + LeaseID Deprecated |
| 3 | "T1 Phase 1 核心 7 任务" | registry + dispatcher + merge + resolver + manager + hookstore_test + mcpcontrol handler 接线 |
| 4 | "R1/R2/R3 第一波修复" | timeout archtest 修复 + hook depth 重入防护 + subscriber_lease + HookLifecycle shutdown |
| 5 | "R4/R5/R6 第二波修复" | Selector 交集查询 + subscriber_lost 自动取消 + 测试补全 69.5%→85.1% |
| 6 | "P8 剩余 8 并行" | hook selector 穿透 + PeerCallback 装配 + config/changed + env 统一 + 接口拆分 + 集成测试 + slog 收敛 + ForgetLease 测试 |
| 7 | "1:7 互审（含代码优雅度）" | 8 Agent 交叉审查 5 维度，识别 2 阻塞 + 3 非阻塞问题 |
| 8 | "阻塞项+非阻塞项修复" | config scope 填充 + slog fx 注入 + PeerCallback 合并 + scoped 测试 + ForgetLease 纯行为验证 |
| 9 | "设计债务收敛" | config 路径统一 + PeerCallback 合并为 contract + ToolControlPlane 纳入 + thread/多维度 scope 测试 |
| 10 | "session-summary 更新" | 本文档更新为 P8.5 hooks 完整落地状态 |

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

### Codex + Claude 混用
默认用 Codex Agent（provider="codex"）实施，Claude Agent（provider="claude"）用于架构评审和全局视角审查
