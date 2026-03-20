# V3 迁移会话摘要

> 生成时间：2026-03-20
> 会话范围：P0-P5 波次 1 全程

## 1. 用户目标

将 `go-agent-v2`（87,900 行）迁移到 `super-agent-v3`（目标 30,000-40,000 行），采用 6 个框架：fx / run.Group / sqlc / stateless / jrpc2 / kelindar/event。

核心约束：
- 两级工厂架构（Two-Zone DRY）：Zone A platform/ 跨模块工厂，Zone B module/ 三件套
- 代码守卫：文件≤400行，函数≤80行，嵌套≤4，CC≤10，无 allowlist
- 子 Agent 必须 LSP 工具链，默认 Codex，禁用 Claude 子 Agent SDK
- 相似代码超 3 处必须抽工厂
- review 功能推迟到 P5 RPC 层

## 2. 当前结论

迁移覆盖度：~68%。P0-P4 骨架稳固，P5 RPC 推进中。

### 子 Agent 提示词模式

实现类：任务标题 → 硬性约束 → 前置读取 → 产出文件 → 代码量预算 → 验证命令
审查类：审查范围 → 审查维度(8-15) → 产出格式(Blocker/Improvement)
互辩类：批判者角色 → 对方报告路径 → 5 个挑剔点(LSP 验证) → 追加到自己报告

### 用户明确要求

1. 严格控制文件行数/函数复杂度
2. 两级工厂架构，不允许重复代码摊一地
3. 子 Agent 用 LSP 工具，默认 Codex
4. 先计划再审查再执行，杜绝反复返工
5. 双 Agent 审查 + 互辩批判
6. review 推迟写入文档
7. kelindar/event 必须切换
8. 守卫必须是可执行 Go 代码

## 3. 已完成

- P0 骨架：4 二进制 + platform 6 包 + contract/dto + Zone A/B 工厂
- P1 Store：sqlc + 96 方法 + 19 repo adapter
- P2 Event Bus：kelindar/event typed bus + 6 类事件 + EventHeader 嵌入 + 泛型工厂(381行)
- P3 状态机：10 状态 + 11 触发器 + orchestration 模块 + SubmissionQueue + StallDetector
- P4 Provider(4波次)：unified/claudecli/codexapp + Registry + Client + SessionManager + Turn service + Thread 扩充 + Contract 测试(17 PASS)
- P5 波次 0：strict + codec + ws + push + approval(510行) + handler 增强 + fx 闭环 + 错误码修正
- P5 波次 1：thread/rpc.go(29方法) + turn/rpc.go(6方法) + SessionResolver + cmd/capCmd 工厂
- P5 波次 1 修复：stale session（SessionCleaner 窄接口 + orchestration 停止路径调 Remove）、approval/respond 契约（decision→json.RawMessage, approved→*bool）、thread/start 参数扩充（approvalPolicy/instructions/effort/personality）、2 个 TODO 备注
- P5 波次 2 前置：orchestration.ListAgents 公开、R4 预算 ≤150→≤300、registerSkillMethods 14 个 skills 归 R3 确认、command/exec 本地执行确认、cardHandler 工厂设计、DAG 方法 TODO 骨架、skill auto-match 归属记录
- P5 波次 2 实现（R3+R4+R5）：module/skill 新建（22 个 command/card/skills 方法）+ module/workspace 新建（8 个 workspace/run 方法）+ module/orchestration/rpc.go 扩充（9 个 agent/task 方法）— 三重审查 + 三方互辩中
- 代码守卫：guardlib + 8 个 archtest
- 文档体系：主文档 + 模块明细 + DRY + 框架指南 + 守卫规格 + P4/P5 执行计划 + 对照报告

## 4. 未完成

| 批次 | 内容 | 状态 |
|---|---|---|
| P5 波次 1 修复 | stale session / approval / start 参数 | ✅ 完成 |
| P5 波次 2 | R3 skill + R4 workspace + R5 orchestration | 🔍 审查+互辩中 |
| P5 波次 3 | R6 uistate + R7 dashboard | 待开工 |
| P5 波次 4 | R8 151 方法注册完整性测试 | 待开工 |
| P6 入口层 | Wails v3 集成 | 待开工 |
| P7 工具层 | MCP family + workspace + skills + IDA | 待开工 |

## 5. 风险/阻塞

| # | 风险 | 严重度 | 状态 |
|---|---|---|---|
| 1 | SessionManager.Remove 无调用点 → stale session | P1 | ✅ 已修复（SessionCleaner 窄接口） |
| 2 | approval/respond 契约回归(decision:any→string) | P1 | ✅ 已修复（decision→RawMessage, approved→*bool） |
| 3 | thread/start 参数不完整 | P2 | ✅ 已修复（+approvalPolicy/instructions/effort/personality） |
| 4 | V2 ResilientPublisher DB fallback 未迁 | P2 | 后续 |
| 5 | auto-recover 仅 stall，缺 connection-dead | P2 | 依赖 P4 |
| 6 | transport_ws/push/codec 无运行时使用点 | P2 | 波次 1-3 接线 |
| 7 | V2 完整事件面未迁 | P3 | P7 范围 |
| 8 | DAG 方法仍是 TODO 骨架（orchestration/rpc.go 中 4 个 task/* 方法） | P2 | 后续补实现 |
| 9 | skill auto-match 已定义但无运行时触发点 | P3 | P7 范围 |

## 6. 涉及文件

cmd/: agent-terminal, mcp-lsp, mcp-orch, mcp-ida
internal/app/: app.go, modules.go, runner.go
internal/archtest/: guardlib.go + 8 test files
internal/contract/: provider.go, approval.go, session_resolver.go, repositories.go, rpc.go
internal/dto/: agent/ provider/ shared/ turn/ tool/ task/ workspace/ ui/
internal/mcpserver/common/: server.go, stdio.go, manifest.go
internal/module/thread/: module+contract+service+history+archive+command+rpc+rpc_types
internal/module/turn/: module+contract+service+assembler+skills+manifest+tracker+rpc+rpc_types+rpc_helpers
internal/module/orchestration/: module+contract+service+runner_actor+recover+submission+events+helpers
internal/platform/bus/: 9 files (381 lines total)
internal/platform/config/: module+config+timeouts
internal/platform/db/: module+pool+tx+errors
internal/platform/rpc/: module+server+handler+strict+registry+codec+transport_ws+push+approval(3 files)+errors+request_context
internal/platform/runner/: module+group
internal/platform/statemachine/: module+factory
internal/platform/shared/: retry+validation+idgen
internal/provider/unified/: module+event_map+registry+client+session+session_adapter+session_resolver
internal/provider/claudecli/: ~11 files
internal/provider/codexapp/: ~11 files
internal/store/sqlc/: 25 files
internal/store/: 19 repo packages
docs/plans/迁移/: 主文档+子文档+执行计划+审查报告

## 7. 关键 Agent ID

| Agent | ID | 状态 |
|---|---|---|
| p5-wave1-fixer | agent-1774019172971-1774019172970482000 | running |
| p5-wave1-audit-B | agent-1774017504836-1774017504834588000 | idle(可复用) |
| p5-wave0-reviewer-A | agent-1774001560910-1774001560908683000 | idle(可复用) |
| p5-wave0-reviewer-B | agent-1774001585763-1774001585763206000 | idle(可复用) |
| p5-wave2-audit-R3 | agent-1774024610715-1774024610713359000 | 互辩中 |
| p5-wave2-audit-R4 | agent-1774024619881-1774024619881059000 | 互辩中 |
| p5-wave2-audit-R5 | agent-1774024630086-1774024630085640000 | 互辩中 |

## 8. 下一步

1. 确认波次 2 互辩结论 → 修复 Blocker
2. 启动 P5 波次 3（R6 uistate + R7 dashboard）
3. 启动 P5 波次 4（R8 注册完整性测试）
4. P5 波次 3：R6 uistate + R7 dashboard(2 Agent 并行)
5. P5 波次 4：R8 151 方法注册完整性测试
6. P5 Done 验证：151 方法全注册 + 零 withRequiredThreadID + 无 God Object + strict binding + push 通道 + approval 闭环
7. P6 入口层：Wails v3
8. P7 工具层：MCP family + workspace + skills + IDA
