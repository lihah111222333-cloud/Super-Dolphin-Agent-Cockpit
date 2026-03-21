# V3 迁移会话摘要

> 生成时间：2026-03-21
> 会话范围：P0-P7 全程 + P8/P9 计划
> Claude 会话 UUID：e925f0b-eba0-49c6-82f9-c306ceae2956
> 前序会话 UUID：91811e30-1bb9-4920-b546-a2d19515a6fd

---

## 1. 用户目标

将 `go-agent-v2`（87,900 行）迁移到 `super-agent-v3`（目标 30,000-40,000 行），采用 6 个框架：fx / run.Group / sqlc / stateless / jrpc2 / kelindar/event。

核心约束：
- 两级工厂架构（Two-Zone DRY）：Zone A platform/ 跨模块工厂，Zone B module/ 三件套
- 代码守卫：文件≤400行，函数≤80行，嵌套≤4，CC≤10
- 子 Agent 必须 LSP 工具链，默认 Codex
- 相似代码超 3 处必须抽工厂
- 审查→互辩→修复→验证 闭环流程

---

## 2. 当前结论

### 迁移状态：P0-P7 收官 ✅

| 阶段 | 状态 | 内容 |
|---|---|---|
| P0 骨架 | ✅ | 4 二进制 + platform 6 包 + contract/dto + Zone A/B 工厂 |
| P1 Store | ✅ | sqlc 96 方法 + 19 repo adapter + WrapStoreError 全覆盖 |
| P2 Event Bus | ✅ | kelindar/event + 6 族 typed event + 泛型工厂 |
| P3 状态机 | ✅ | 10 状态 + 11 触发器 + 严格模式（去 force fallback） |
| P4 Provider | ✅ | unified + claudecli + codexapp + SessionManager lifecycle |
| P5 RPC | ✅ | 79+ handler + 全部 Blocker 清零 |
| P6 Wails | ✅ | RunDesktop + CallAPI/Dispatch + 原生绑定 + 双向 shutdown + event bridge |
| P7w1 V2 兼容 | ✅ | D1-D12 全部修复 + 互审 8 问题修复 |
| P7w2 Dashboard | ✅ | module/dashboard 新建 — agent 监控 + 系统信息 + 日志 |
| P7w2 UI State | ✅ | module/uistate 新建 — bus projector + 实时快照 + sidebar + preferences |
| P8 编排工具 | ⏳ | MCP 编排工具族 20 个，~1,200 行，计划已出 |
| P9 LSP 工具 | ⏳ | MCP LSP 工具族 9 个，~12,500 行，计划已出 |
| IDA 工具 | ⏳ | MCP IDA 工具族 82 个，暂缓 |

### 编译验证
```
✅ go build ./...     — 0 errors
✅ go vet ./...       — 0 warnings
✅ go test archtest   — ALL PASS
✅ lsp diagnostics    — 0 diagnostics
```

### 审查覆盖
| 类型 | Agent 数 | 报告数 |
|---|---|---|
| 模块审查+互辩 | ~40 | 20+ |
| 后端总审查 10 模块 | 10 | 10 |
| V2↔V3 1:1 对齐 | 20 | 20 |
| 架构合规检查 | 10 | 10 |
| 能力+容错审查 | 10 | 10 |
| P7w2 审查+验证 | 12 | 12 |
| **累计 Agent 投入** | **~150+** | **80+ 份报告** |

---

## 3. 已完成（详细）

### P0-P4（前序会话完成）
- P0 骨架：4 二进制 + platform 6 包 + contract/dto
- P1 Store：sqlc + 96 方法 + 19 repo
- P2 Event Bus：kelindar/event + 6 类事件 + EventHeader 嵌入
- P3 状态机：10 状态 + 11 触发器 + orchestration
- P4 Provider：unified/claudecli/codexapp + Registry + Client + SessionManager

### P5 RPC（本会话完成）
- 波次 0：strict + codec + ws + push + approval + handler + fx
- 波次 1：thread 29 方法 + turn 6 方法 + SessionResolver + cmd/capCmd 工厂
- 波次 1 修复：stale session + approval 契约 + start 参数
- 波次 2：skill 22 方法 + workspace 8 方法 + orchestration 13 方法
- 波次 2 审查+互辩+修复：20 原始 Blocker 全部清零
- 系统级修复 S1-S8：bus→push + TurnCompleted 链路 + approval 闭环 + CloseAll + store 聚合 + fireOrForceLocked + AgentID fallback
- P2 功能补全：B6-B15 + I1-I8 全部完成

### P6 Wails 集成（本会话完成）
- go.mod Wails v3 alpha.74 依赖
- rpc.Server.Dispatch（jrpc2 本地调用复用中间件链）
- RunDesktop()：fx.Start → Wails Run → fx.Stop
- 单一 fx Wails app（PB1+PB2 修复）
- binding.go：CallAPI + GetBuildInfo + GetGroup
- binding_native.go：SaveClipboardImage + SelectProjectDir + SelectFiles
- lifecycle.go：ShouldQuit + quit overlay + 双向 shutdown
- bridge.go：bus → Wails bridge-event
- window.go：1440x900 + file drop

### P7（本会话完成）
- 波次 1 V2 兼容收尾：D1-D12（approval/turn/dto/store/provider）
- 波次 1 互审修复 8 项
- 波次 2：Dashboard + UI State 新模块
- 全量修复：3 并发竞态 + 12 V2 降级 + JSON 治理 + 文档 + 测试

---

## 4. 未完成

| 项 | 状态 | 说明 |
|---|---|---|
| P7w2 uistate 5 问题修复 | 🔧 Agent 执行中 | TurnCompleted 终态 + thread 事件 + token 发布 + preferences + sidebar |
| P7w2 dashboard + 4❌ 修复 | 🔧 Agent 执行中 | Snapshot 实测 + sqlc 标注 + 状态机封堵 + awaiting 闭环 |
| 6 个⚠️现修 | 🔧 4 Agent 执行中 | CapabilityError + submit 错误 + event name + 时间统一 + JSON + recover |
| P8 前置必修 | ⏳ | sqlc 漂移 / Snapshot runtime 上报 / 状态机三方联动（已写入 P8 文档） |
| **P8 编排工具族** | ⏳ | 20 个工具，V2 4,612 行 → V3 ~1,200 行，4 Agent 并行 |
| **P9 LSP 工具族** | ⏳ | 9 个工具，V2 14,143 行 → V3 ~12,500 行，6+1 Agent |
| **IDA 工具族** | ⏳ | 82 个工具，暂缓 |

---

## 5. 风险/延后项

| # | 问题 | 严重度 | 归期 |
|---|---|---|---|
| 1 | sqlc 生成层漂移（threadbinding/dbquery/ailog） | P2 | P8 前专项 |
| 2 | AgentSnapshot port/provider 仅推断非实测 | P3 | P8 |
| 3 | 状态机外直写路径 + awaiting_user_input 闭环 | P2 | P8 前专项 |
| 4 | UI State 仍是轻量版（V2 50 文件完整投影系统） | P3 | 逐步补齐 |
| 5 | V2 RPC 覆盖率 79/151 = 52%（缺 dashboard/UI/MCP） | P2 | P8+P9 |

---

## 6. 涉及文件（核心目录结构）

```
cmd/
├── agent-terminal/main.go          ← RunDesktop() 入口
├── mcp-lsp/main.go + fx.go         ← LSP 工具 server（骨架）
├── mcp-orch/main.go + fx.go        ← 编排工具 server（骨架）
└── mcp-ida/main.go + fx.go         ← IDA 工具 server（骨架）

internal/
├── app/app.go + modules.go + runner.go     ← fx 装配 + RunDesktop
├── archtest/guardlib.go + 8 tests          ← 代码守卫
├── contract/provider.go + approval.go + session_resolver.go
├── dto/agent/ provider/ shared/ turn/ tool/ task/ workspace/ ui/
├── mcpserver/common/server.go + manifest.go
├── module/
│   ├── thread/     ← 29 handler + lifecycle + history + command
│   ├── turn/       ← 6 handler + assembler + tracker + skills + manifest
│   ├── orchestration/ ← 13+ handler + dag + report + runner_actor + recover
│   ├── skill/      ← 22 handler + cards + exec + match + fs
│   ├── workspace/  ← 8 handler + merge + service_helpers
│   ├── uistate/    ← NEW: projector + state + rpc
│   └── dashboard/  ← NEW: service + rpc
├── platform/
│   ├── bus/        ← 9 files (Route/ResilientSubscribe/Projector)
│   ├── config/     ← config + timeouts
│   ├── db/         ← pool + tx + errors + WrapStoreError
│   ├── rpc/        ← server + handler + strict + push + approval + codec + ws
│   ├── runner/     ← group + module
│   ├── statemachine/ ← factory
│   └── shared/     ← retry + validation + idgen
├── provider/
│   ├── unified/    ← registry + client + session + resolver + event_map
│   ├── claudecli/  ← driver + session + history
│   └── codexapp/   ← driver + session + approval + recovery + history
├── store/          ← 19 repo + sqlc + module.go (全聚合)
└── ui/wails/       ← module + binding + bridge + lifecycle + window + assets
```

---

## 7. 当前运行中 Agent

| Agent | 任务 | 状态 |
|---|---|---|
| audit-p7w2-uistate-fix | uistate 5 问题修复 | running |
| audit-p7w2-dashboard-fix | dashboard + 4❌修复 | running |
| verify-align-thread | CapabilityError 优雅降级 | running |
| verify-align-agent | submit 明确错误 + event name 扩展 | running |
| verify-align-store-sm | 事件时间统一 + recover TODO | running |
| verify-align-fx-wails | JSON 治理 + 延后项→P8 文档 | running |

---

## 8. 下一步

1. ✅ 等 6 个 Agent 完成 → 全量验证
2. P8 编排工具族开工（20 个工具，4 Agent 并行）
3. P9 LSP 工具族开工（9 个工具，6+1 Agent）
4. P8 前置专项：sqlc 漂移 + Snapshot runtime + 状态机封堵
5. IDA 工具族（暂缓）

---

## 9. 关键文档清单

| 文档 | 路径 |
|---|---|
| 主迁移计划 | docs/plans/迁移/v3-migration-plan.md |
| 模块迁移明细 | docs/plans/迁移/v3-module-migration-details.md |
| P7 执行计划 v2 | docs/plans/迁移/p7-execution-plan-v2.md |
| P8 执行计划 v2 | docs/plans/迁移/p8-execution-plan.md |
| P9 执行计划 v2 | docs/plans/迁移/p9-execution-plan.md |
| MCP LSP 工具盘点 | docs/plans/迁移/audit-mcp-lsp-tools.md |
| MCP 编排工具盘点 | docs/plans/迁移/audit-mcp-orch-tools.md |
| MCP IDA 工具盘点 | docs/plans/迁移/audit-mcp-ida-tools.md |
| 后端总审查 ×10 | docs/plans/迁移/review-*.md |
| V2↔V3 对齐 ×20 | docs/plans/迁移/align-*.md |
| 架构合规 ×10 | docs/plans/迁移/arch-*.md |
| 能力容错 ×10 | docs/plans/迁移/cap-*.md |
| 对齐验证 ×10 | docs/plans/迁移/verify-*.md |
| P7w2 审查 | docs/plans/迁移/audit-p7w2-*.md |
| 最终裁定 ×3 | docs/plans/迁移/final-verdict-*.md |

---

## 10. 最近 10 条对话摘要

| # | 用户指令 | 执行内容 |
|---|---|---|
| 1 | "p7w2 派2个agent 审查，后端总审查 10 模块V2↔V3 1:1 对齐 派10个agent 验证" | 拉起 12 Agent：2 个审查 P7w2（uistate+dashboard）+ 10 个验证之前的 V2↔V3 对齐修复是否生效 |
| 2 | "都完成了，汇总一下报告" | 从磁盘读取 12 份报告结论段，输出终极验收报告：42✅ / 9⚠️ / 4❌ / 0🔴 |
| 3 | "P7w2 审查agent 原地修复" | 2 个审查 Agent 原地修：uistate 5 问题 + dashboard 审查问题 + 4 个❌（Snapshot/sqlc/状态机/awaiting） |
| 4 | "V2↔V3 对齐修复验证（10 模块）列出哪些可以修复，不延后，哪些必须延后" | 逐项分析：6 个⚠️可现修（CapabilityError/submit错误/event name/时间/JSON/recover），3 个❌必须延后（sqlc漂移/Snapshot runtime/状态机三方联动） |
| 5 | "现在可以修的，安排对应审查agent修复，延后的修复到p8文档中" | 4 个 verify Agent 原地修 6 个⚠️ + 延后项写入 P8 文档 |
| 6 | "输出任务摘要，写到交接的session-summary.md" | 生成本文档 |
| 7 | "所有agent都完成了" | 跑全量验证 build+vet+archtest+diagnostics 四项全绿 |
| 8 | "开20个agent进行1:1对齐审查" + "再开10个架构合规检查" | 30 Agent 并行：20 个 V2↔V3 能力对齐 + 10 个架构合规 |
| 9 | "p7w1 互审结果出来了" + "当场和推迟都安排原agent修复" | 汇总 P7w1 互审 8 问题 + 分配 3 Agent 全部修复 |
| 10 | "10个互相审查结果出来了" + "分配几个互审agent修复" | P1-P6 能力审查 10 Agent 1:3 互审汇总 + 5 Agent 修复 10 个问题 |

---

## 11. 子 Agent 提示词模式

### 实现类
任务标题 → 硬性约束（LSP/行数/CC） → 前置读取 → 修复方案（代码示例） → 验证命令

### 审查类
审查范围 → 审查维度（10-18个） → 产出格式（Blocker/Warning/OK） → LSP 强制指令

### 互辩类
角色：批判者 → 对方报告路径 → 每份至少 3 个挑剔点 → 必须 LSP 验证 → 追加到报告末尾

### LSP 强制指令（所有 Agent 追加）
```
一、语义检索：ast_search / text_search(glob+regex) / workspace_symbol
二、关系导航：definition / implementation / references(compact) / call_hierarchy / type_hierarchy
三、精确读取：func_start/func_end → read_file / document_symbol / 批量 file_paths
四、编辑：rename / replace_range(patch) / edits / diagnostics
五、组合技：A(ast→read) B(symbol→impl→read) C(ref→call_hierarchy) D(doc_symbol V2/V3 对比)
严禁 grep/find/cat/sed/awk
```
