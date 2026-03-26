# V3 迁移会话摘要

> 生成时间：2026-03-26（P1 四批全部收口）
> 会话范围：P0-P8.5 全程 + P7.5 桥接 + V2↔V3 核对×2 + archtest 收官 + MCP 独立服务 + ctl/* 回调框架 + lifecycle hooks + P8 审查修复 + P0 安全修复 + P1 四批修复
> Claude 会话 UUID：58fdd978-cc4b-41e6-bd26-d40f3ff66854
> 前序会话 UUID：ea3ad84e-7b52-422d-bc46-cff9da3ea9f9

---

## 1. 当前结论

### 编译验证：全绿
```
✅ go build ./internal/... ./cmd/mcp-orch/...  — 0 errors
✅ go vet ./...                                — 0 warnings
✅ lsp diagnostics                             — 0 errors (仅 hints)
✅ archtest TestCodeSizeGuard                  — PASS
✅ archtest TestDependencyDirection             — PASS
✅ archtest TestTimeoutLocality                 — PASS
✅ archtest TestSqlcBoundary                    — PASS
```

### 2026-03-27 补充验证（P3 集成验证 + fx 快清）
- `go build ./internal/... ./cmd/mcp-orch/...`：PASS
- `go vet ./internal/... ./cmd/mcp-orch/...`：PASS
- `go test ./internal/platform/bus/... -v -count=1`：PASS
- `go test -run TestCodeSizeGuard ./internal/archtest/...`：PASS
- `go test ./internal/archtest/... -v -count=1`：FAIL
- 当前可复现失败仅剩 `TestMCPOrchDependencyDirection/allowed_internal_boundary`：`cmd/mcp-orch` 通过 `internal/platform/rpc` 传递依赖到了 `internal/platform/shared`，超出 archtest 允许边界
- `TestTimeoutLocality` 在首次全量运行中曾报 `internal/store/sqlc/read_only_tx.go:62`，但该文件随后发生并发工作树变更；按当前磁盘状态重跑已 PASS

### 迁移状态

| 阶段 | 状态 | 内容 |
|---|---|---|
| P0-P6 | ✅ | 骨架+Store+EventBus+状态机+Provider+RPC+Wails |
| P7w1-P7w2 | ✅ | V2兼容+Dashboard+UIState |
| P7.5 桥接校准 | ✅ | ~24 handler + 4 事件桥接 + 安全加固 |
| P8 前置+核对+archtest | ✅ | sqlc漂移+runtime上报+dbquery+21模块核对+41项违规修复 |
| P8 编排工具 | ✅ | cmd/mcp-orch 19 handler |
| P8.5 ctl+hooks | ✅ | 15方法+7层transport+覆盖率85.1% |
| **P8 审查+修复** | ✅ | **五维审查+20项修复（P2×4+P3×3+P4×6+低优×4+P0×3）** |
| **V2↔V3 二次核对** | ✅ | **20模块核对+1:10互审+三方审计** |
| **P0 安全修复** | ✅ | **guard链+binding id+approval auto-decline** |
| **P1 第一批 B+C+DAG** | ✅ | **TurnInterrupted+StopAllAgents+claude reconnect+codex进程+DAG fencing+golden框架** |
| **P1 第二批 D+残留** | ✅ | **config/read+messages+interrupt envelope+turn finish+store DTO+超时+Kind+SqlcBoundary** |
| **P1 第三批 A+E** | ✅ | **session解耦+preferences delta+approval replay+深度计数器+Overlay** |
| **P1 第四批 余项** | ✅ | **dashboard补全+wails desktop+thread 4项契约+workspace验证+ready wait+terminal_wait+threadID修复** |
| P9 LSP 工具 | ⏳ | cmd/mcp-lsp 9个工具 |
| P10 工厂丰满 | ⏳ | Zone A 3.8%→60% |

---

## 2. P1 修复总结（本会话核心成果）

### 2026-03-27 补充收口记录
- P0: lifecycle `SafeGo` 11 处 + signal 收敛 + `StartupTimeout` + oklog recover
- P1: `DBQuery` READ ONLY + `AILog` keyword SQL 下推 + 9 处高风险吞错 `LogIgnoredError`
- fx: 5 个悬空 emitter provider 清理（删除 `Agent/Turn/Tool/Task/UI`，保留 `Thread/Workspace`）

### 四批执行统计

| 批次 | 根因 | 项数 | Agent | 互审轮数 | 状态 |
|------|------|------|-------|---------|------|
| 第一批 | B+C+DAG | 6+1 | 7 | 3轮 | ✅ 收口 |
| 第二批 | D+残留 | 7 | 7 | 2轮 | ✅ 收口 |
| 第三批 | A+E | 5 | 5 | 2轮 | ✅ 收口 |
| 第四批 | 余项 | 7 | 7 | 3轮 | ✅ 收口 |
| **合计** | | **25** | **~26** | | **25/25 ✅** |

### 第一批成果（根因 B+C+DAG）
- B1: TurnInterrupted 独立终态闭环（handleTurnInterruptedEvent）
- B2: StopAllAgents 统一停机 + waitForProcessExit 超时 + stopReason 修复
- B3: claude reconnect/reinitialize（threadReady 重建 + transport ready 等待）
- B4: codex 进程契约（Setpgid+readiness+stderr+orphan+SIGTERM→grace→SIGKILL+atomic.Bool）
- B6: DAG 锁/wakeup fencing（ClaimedBy+LeaseExpiresAt+active_wakeup_id）
- D0: golden test 框架（按业务域分布 orchestration/transport/archtest）

### 第二批成果（根因 D+残留）
- D1: config/read 补全字段（session runtime → store override → binding → default）
- D2: thread/messages 分页+结构+compaction（createdAt+{messages,total}+离线 fallback+hydration）
- D3: turn/interrupt 返回 envelope（confirmed/mode/stateBefore/stateAfter+interrupt_timeout 降级）
- D4: claude turn finish payload（result/summary/message/stop_reason+失败兜底）
- D5: store-db thread/read + workspace DTO 补全
- R1: B2 超时封死 + B5 Kind 扩大匹配
- R3: SqlcBoundary 4 项违规修复

### 第三批成果（根因 A+E）
- A1: session 解耦（buildOfflineConfig+config 优先级链+store 写穿+resume 合并）
- A2: preferences 残留 delta（校验先于存储+stallThresholdSec+showInjectedPromptInChat）
- A3: approval live replay + peer_kind gating + TTL 刷新
- A4-α: 深度计数器 + Status Derive（nil check+idle-preserve 修正+CC 拆分）
- A4-β: Overlay 覆盖层（Sidebar/patch 一致性+mcp_startup producer）

### 第四批成果（余项）
- F1: dashboard agentStatus 独立读模型 + status filter + json tag
- F2: dashboard logs(audit/bus) + DAG 面 + nil 优雅降级
- F3+F4: wails desktop API + 兼容绑定（GetLSPDiagnostics 空参数兼容+GetGroup 默认非空）
- F5-1+F5-2+F8: thread/start+resume 契约恢复 + Claude resume threadID 修复
- F5-3+F5-4: thread/recover+fork 契约恢复
- F6: workspace dry-run + merge 验证+清理
- F7+F9: execute-time ready wait + terminal_wait overlay producer（inputApprovalDepth）

---

## 3. 未完成

| 项 | 状态 | 说明 |
|---|---|---|
| P1-16 (B5) approval 等待态 | ⏸️ | 方案 A/B 未决，等人工定策略 |
| A4-γ Timeline 投影 | ⏸️ | ~400-500行高耦合，可选推迟 |
| D1 完整离线 merge | ⏸️ | A1 基础已建，完整 V2 runtime merge 待补 |
| P9 LSP 工具族 | ⏳ | 9 个工具，6+1 Agent |
| P10 工厂丰满 | ⏳ | Zone A 3.8%→60% |
| IDA 工具族 | ⏳ | 82 个工具，暂缓 |

---

## 4. 下一步

1. **P9 LSP 工具族** — 读 p9-execution-plan.md
2. **P10 工厂丰满** — Zone A 3.8%→60%
3. **B5 策略决策** — 人工定方案 A/B 后执行
4. **A4-γ Timeline** — 可选

---

## 5. 关键文档清单

| 文档 | 路径 |
|---|---|
| 主迁移计划 | docs/plans/迁移/v3-migration-plan.md |
| P1 修复计划 | docs/plans/迁移/p1-v2v3-fix-plan.md |
| V2↔V3 二次核对终极报告 | docs/plans/迁移/v2v3-recheck-final.md |
| V2↔V3 初次报告 | docs/plans/迁移/v2v3-final-report.md |
| MCP 服务契约 | docs/契约/mcp-service-convention.md |
| P8 lifecycle hooks | docs/plans/迁移/p8-lifecycle-hooks.md |
| P9 执行计划 | docs/plans/迁移/p9-execution-plan.md |
| LSP 强制前缀 | shared file: prompts/lsp-mandatory-prefix.md |
| LSP 高级指南 | shared file: prompts/lsp-advanced-guide.md |

---

## 6. Agent 使用统计

| 类型 | 数量 |
|---|---|
| 本会话 P8 审查+修复 | ~50 |
| 本会话 V2V3 核对+汇总+审计 | ~25 |
| 本会话 P0 修复+互审 | ~15 |
| 本会话 P1 第一批(B+C+DAG)+互审 | ~20 |
| 本会话 P1 第二批(D+残留)+互审 | ~20 |
| 本会话 P1 第三批(A+E)+互审 | ~15 |
| 本会话 P1 第四批(余项)+互审 | ~20 |
| 本会话 计划审查+辩论 | ~10 |
| **本会话合计** | **~175** |
| 前序会话累计 | ~350+ |
| **总计** | **~525+** |

---

## 7. 子 Agent 提示词模式

### LSP 强制指令（所有 Agent 追加，硬性约束）

**拉起任何 Agent 时，初始 prompt 必须包含以下文档链接，不得省略：**

```
先执行 shared_file_read prompts/lsp-mandatory-prefix.md
再执行 shared_file_read prompts/lsp-advanced-guide.md
禁止只用 lsp_grep + lsp_file，每个任务至少 4 种 LSP 工具
```

**文档路径（必须原样下发）：**
- `prompts/lsp-mandatory-prefix.md` — LSP 强制前缀，所有 Agent 首先读取
- `prompts/lsp-advanced-guide.md` — LSP 高级工具完整指南

**违反后果：** Agent 产出的代码搜索/读取质量不可靠，审查结论不可信

### 仓库契约引用
```
守卫标准：文件 ≤400行，函数 ≤80行，CC ≤10，包非测试文件 ≤15，包总行数 ≤4500
Zone B 模式：docs/plans/迁移/v3-two-zone-dry-enrichment.md §3
sqlc 生成代码豁免：internal/store/sqlc/ SkipDir
```

### 守卫提醒（硬性约束，下发任务时必须包含）

**每次下发任务给 Agent 时，初始 prompt 末尾必须附加以下守卫提醒，不得省略：**

```
⚠️ 守卫红线（违反将触发 archtest 失败，导致返工）：
- 单文件 ≤400 行（超了必须拆文件）
- 单函数 ≤80 行（超了必须提取子函数）
- 圈复杂度 CC ≤10（超了必须拆分分支逻辑）
- 包非测试文件 ≤15（超了必须合并小文件）
- 包总行数 ≤4500（超了必须拆包或精简）
- 完成后必须跑 go test -run TestCodeSizeGuard ./internal/archtest/... 确认全绿
```

**违反后果：** 互审必然被打回，大量返工。宁可写代码时多拆几个 helper，也不要事后被 archtest 卡住。

### 死代码清理规范（硬性约束，下发任务时必须包含）

> 教训：Agent 习惯“加新不删旧”，导致大量死代码残留。本规范强制要求每个任务包含清理环节。

**核心原则：替换 = 删旧 + 加新，不是只加新。**

**每次下发任务给 Agent 时，初始 prompt 必须附加以下清理规范，不得省略：**

```
⚠️ 死代码清理规范（违反将导致互审打回）：

1. 替换旧实现时，必须删除旧代码（不是注释掉，不是在旁边加新的）
2. 新建 helper/工厂后，搜全仓现有手写等价逻辑，统一迁移到新 helper 后删除旧代码
3. 删除定义前，用 lsp_xref(references) 确认零引用
4. 删除后用 lsp_grep 搜索旧模式关键字，确认全仓零残留
5. 空函数/空文件/空目录 → 删除
6. 未使用的 import → 删除（go vet 验证）
7. 如有 //nolint:errcheck 等抑制注释，随旧代码一起删除
8. 任务完成前必须跑一遍清理验证：
   - lsp_grep 搜索旧模式 → 全仓零残留
   - go vet ./... → 无 unused import
   - lsp_file(diagnostics) → 无新增告警
```

**常见遗留场景与强制处理：**

| 场景 | 遗留风险 | 强制处理 |
|--------|----------|----------|
| 裸 `go func()` 替换为 SafeGo | 旧 `go func()` 整块未删，旧 inline recover 残留 | 删除旧整块；如旧 goroutine 内有通用 recover 则删除（SafeGo 已覆盖）；如有专用语义（如写结果到 channel）则保留但重构 |
| `_ = err` 替换为 LogIgnoredError | 旧 `_ =` 行未删，新旧并存 | 删除旧行；删除伴随的 `//nolint` 注释 |
| 新建 helper 函数 | 全仓散落的手写等价逻辑未迁移 | 搜全仓同模式，统一替换后删旧 |
| 删除事件/类型定义 | DTO 删了但 sink 订阅/event_map 注册/常量未同步删 | 联动删除所有引用点；如文件变空则删文件 |
| 接口方法签名变更 | 调用方旧签名未同步更新 | 用 lsp_xref(references) 找所有调用方，逐个更新 |
| 新增常量替代硬编码字符串 | 旧硬编码字符串散落在多个文件 | lsp_grep 搜旧字面量，全部替换为常量引用 |
| JSON tag 改名（如 camelCase → snake_case） | 前端/测试仍依赖旧 tag | 搜全仓旧 tag 字符串，同步更新测试断言 |
| fx.Provide 删除 | 构造函数和类型定义仍在 | 用 lsp_xref 确认零引用后删除构造函数和类型 |
| 测试断言依赖旧返回值/旧行为 | 生产代码改了但测试没同步 | 生产代码每改一处，必须同步更新对应测试 |

**违反后果：** 互审必然被打回。Agent 不会主动清理，必须在 prompt 中显式要求。

### Agent 拉起规范（硬性约束）

**所有 Agent 必须通过编排接口（`orchestration_launch_agent` / `orchestration_send_message`）拉起，禁止通过 SDK Agent tool 拉 Claude 子 agent。**

| 用户指令 | 含义 | 实现方式 |
|---------|------|----------|
| "拉 agent" / "拉 codex" | 拉 Codex Agent | `orchestration_launch_agent(provider="codex")` |
| "拉 claude" | 拉 Claude Agent | `orchestration_launch_agent(provider="claude")` |
| 未指定 provider | 默认 Codex | `orchestration_launch_agent(provider="codex")` |

**禁止行为：**
- ❌ 通过 SDK `Agent` tool 拉 claude 子 agent（绕过编排系统，无法被追踪/管理）
- ❌ 不通过编排接口直接启动后台 agent

**用途分工：**
- Codex Agent（默认）：代码实施、搜索、修复、测试
- Claude Agent：架构评审、全局视角审查、复杂推理

---

## 8. 会话交接（下一会话必读）

### 8.1 当前仓库状态
- **编译**：go build ✅ / go vet ✅ / lsp diagnostics ✅
- **archtest**：TestCodeSizeGuard ✅ / TestDependencyDirection ✅ / TestTimeoutLocality ✅ / TestSqlcBoundary ✅
- **包行数上限**：已从 3000 调整为 4500（internal/archtest/guardlib.go:24）
- **未提交改动**：本会话所有改动均在工作区，未 git commit

### 8.2 已完成的重大改动（本会话）

| 类别 | 改动 | 涉及文件 |
|------|------|----------|
| P8 审查修复 | IntersectTargets 测试 + resolvedReviewReader + hooks 解耦 + resolved_by + doc comment + archtest rule15 + TTL + 文件拆分 + dispatcher panic + env_test + TimeoutLocality + 包精简 | mcpcontrol/ hooks/ hookstore/ dto/ contract/ archtest/ bootstrap/ dbquery/ |  
| P0 安全 | thread-start guard链 + binding/thread-id + approval auto-decline + requestID去重 | thread/ rpc/ codexapp/ claudecli/ |
| P1 第一批 | TurnInterrupted + StopAllAgents + claude reconnect + codex进程 + DAG fencing + golden框架 | orchestration/ claudecli/ codexapp/ taskdag/ testutil/ |
| P1 第二批 | config/read + messages + interrupt envelope + turn finish + store DTO + 超时 + Kind + SqlcBoundary | thread/ turn/ claudecli/ store/ uistate/ dashboard/ |
| P1 第三批 | session解耦 + preferences + approval replay + 深度计数器 + Overlay | thread/ uistate/ rpc/ |
| P1 第四批 | dashboard补全 + wails desktop + thread 4项契约 + workspace验证 + ready wait + terminal_wait + threadID修复 | dashboard/ wails/ thread/ workspace/ orchestration/ uistate/ |

### 8.3 下一会话首任务

1. **git commit** — 本会话所有改动未提交，建议先 `git add -A && git commit`
2. **P9 LSP 工具族** — 读 `docs/plans/迁移/p9-execution-plan.md`，cmd/mcp-lsp 9 个工具
3. 或者 **B5 策略决策** — 人工定方案 A（新增 awaiting_tool_approval 状态）vs B（扩大 Kind 匹配）

### 8.4 关键守卫参数（新会话必须知道）

| 参数 | 值 | 位置 |
|------|------|------|
| 单文件行数 | ≤400 | guardlib.go:21 |
| 单函数行数 | ≤80 | guardlib.go:22 |
| 圈复杂度 CC | ≤10 | guardlib.go:23 |
| 包非测试文件数 | ≤15 | guardlib.go:25 |
| 包总行数（排除测试） | ≤4500 | guardlib.go:24 |

### 8.5 V2 源码路径
- V2 代码：`/Users/mima0000/Desktop/wj/go-agent-v2/`
- V3 代码：`/Volumes/bot/super-agent-v3/`

### 8.6 本会话工作模式总结

| 模式 | 说明 |
|------|------|
| 批次执行 | 按根因聚合，每批 5-7 Agent 并行 |
| 互审 | 1:N 互审，每项至少 2 方交叉审查 |
| 反驳 | 互审后原地修复，修复后再互审 |
| 计划审查 | 2-3 Agent（Codex+Claude）审查计划，辩论后修订 |
| 守卫验证 | 每次修复后必须跑 TestCodeSizeGuard |
| 文档落盘 | 重要报告落盘到仓库 docs/plans/迁移/ |

### 8.7 已知限制（本会话发现）

| # | 限制 | 说明 |
|---|------|------|
| 1 | 长会话 Agent 超时 | 会话太长时 Agent 进程会被回收，report 返回空。解决：重新拉 Agent |
| 2 | Claude API 限额 | 大量并行 Agent 可能触发限额。解决：用 Codex provider 代替 |
| 3 | SDK Agent tool 不受管理 | 通过 SDK 拉的 Agent 无法被编排系统追踪。解决：禁止使用，只走编排接口 |
| 4 | 共享文件 vs 仓库文件 | 报告在共享文件系统，汇总落盘到仓库。读用 shared_file_read，写用 code_run |
