# P23 五路 Gap 调研裁决（2026-04-25）

> 创建时间：2026-04-25 | 状态：**裁决已写入 README，arbiter 报告待补**
> authoritative：本文件记录调研裁决；gate / archtest / CI / hard-soft 唯一权威为 [`COMPLIANCE_GATES.md`](COMPLIANCE_GATES.md)，README 只引用不维护第二清单
> 输入：5 个 codex 调研 agent 的事实层报告
> 决策权：用户 2026-04-25 当面拍板（DAG 自驱 + 三触发源 + 三能力扩展；verdict 最终口径已由后续裁决更新为方案 C：默认 runtime arbiter，judge 仅 opt-in，失败 `verdict_lost`）

---

## 拆分口径说明

> P23 是单个计划文件夹，本文裁决中提到的 **P7 / P8 / P9 是 P23 内部后段子任务**（与 P0–P6 同层则），**不**是独立计划单 p24 / p25 / p26。所有会话中出现过的「P24 / P25 / P26」说法在本会话 11:30 以后被纠正为「P7 / P8 / P9」，全部位于 `docs/plans/迁移/p23/` 内。

## 输入：用户三能力要求

1. **要求 1（活性探针）**：DAG 每 N 分钟检查 node 上 agent 有无新动向（工具调用、消息、turn 进度）；无动向 → 自动 relaunch。兜底"agent 还活着但不出事件"/"卡死循环"/"hook 漏触发 terminal"。
2. **要求 2（hook 拦截 + 校验闭环）**：agent 声称完成 → DAG hook 拦截 → 不直接 mark done，而是：
   - 2a 异步：自动拉 verifier agent 校验，通过 done / 不通过打回
   - 2b 同批互验：兄弟 node 全完成后互验
   - 打回 = 把 verifier 反馈作为新 prompt 投回原 agent，重起一轮 turn
3. **要求 3（千 node 百 agent 大规模）**：自动迭代一个系统、上千步工具调用、几百个 agent 调度
4. **verdict 实现位置（最终口径）**：方案 C — 默认走 DAG runtime 内嵌 LLM arbiter；judge node 仅在 `verdict_strategy=judge` / `dissent_action=escalate_judge` 且显式配置 `judge_node_key` 时 opt-in；arbiter 不可得落 `verdict_lost`，不自动降级 B

---

## 五路调研 agent 与产出

| Agent ID | 调研角度 | 主要结论摘要 |
|---|---|---|
| `gap-liveness` | 要求 1 vs README | P0–P6 前段不覆盖活性探针；P7 后段承接；建议新增 `dagActivityActor`（第 5 actor）+ `last_activity_at` 字段 + 长工具调用反误杀策略 |
| `gap-verify` | 要求 2 vs README | state machine 不能直接容纳 verify gate；建议 `running → awaiting_verify → verifying → done | repairing`；引入 `nodes[].verify` schema + sibling group 概念；打回路径复用 retry 但配独立 `max_rounds` |
| `gap-scale` | 要求 3 vs README | DAG 创建 1000 node 单事务、ready 计算 O(N²)、launcher 当前无固定并发上限、hook 同步 dispatch、wakeup 表无 GC、result jsonb 膨胀——共 7 大瓶颈 |
| `gap-synth` | 跨切片综合 | 三要求叠加后最弱环节是 hook consumer + DB CAS（不是 launcher）；推荐**拆分 + 二阶段**：P23 保自驱底座，P7/P8/P9 作为 P23 后段子任务拆出 |
| `gap-arbiter` | verdict 实现位置（A vs B vs C） | **仍在调研中（turn_running）**——结论待补，结果归档到本文件 |

---

## 五路 agent 关键事实锚点（report 摘要）

### 1. `gap-liveness`

**结论**：P23 4 actor 中没有活性探针；lease 续租 ≠ 事件流监测。

**关键事实**：
- README 现有 4 actor 锚点：`docs/plans/迁移/p23/README.md:90-94`
- 可作活性信号的现有事件：`internal/dto/turn/event.go:42-64`（`TurnOutputDelta/Started/InputReceived/Stalled/Resumed`）、`internal/dto/turn/progress.go:24-54`（`ItemStarted/ItemCompleted`）、`internal/dto/tool/event.go:9-61`（`ToolCallBegin/End/Approval/Diff`）
- 已走 hook：`TurnCompleted/Interrupted` + 仅 final-answer `ItemCompleted`（`hook_consumer.go:285-287`、`event_relay.go:64-86`）
- **未走 hook**：非 final `ItemCompleted`、`ItemStarted`、`TurnOutputDelta`、工具事件、客户端 stdout
- 已有近似字段：`migrations/0023_dag_watcher_phase1.sql:1-4` 有 `last_event_at` 类似字段
- p21 P1b 续租先例：`internal/module/cron/lease_actor.go:12-16`

**关键 trade-off**：长工具调用（5 min `code_run`）合法无事件——单纯 `idle_timeout_sec` 会误杀；需要 `tool_idle_timeout_sec` 双阈值。

### 2. `gap-verify`

**结论**：P23 现有 state machine `pending → running → done | failed | observe_lost` **不能容纳** verify gate；需要扩展。

**关键事实**：
- hook 拦截能力已就位：`internal/sidecar/orch/orchestration/hook_consumer.go:148-151,260-275`，但 README 现要求直接映射 `CompleteNode`（`README.md:71-72`）
- `TurnCompleted` 已有 `Success/Result/Summary/Error`：`internal/dto/turn/event.go:10-21`
- 现有 schema 只有 `DependsOn []string`：`internal/sidecar/orch/orchestration/dag.go:41-49`，**无 sibling group 概念**
- 共享 launcher 已支持 launch 后自动投 prompt：`service_launcher_bridge.go:89-119`
- 同 agent 再投 turn 接口：`internal/sidecar/orch/orchestration/service.go:337-339` 的 `SubmitTurn` → `internal/sidecar/orch/orchestration/service_launcher_bridge.go:338-351` 的 `submitTurnViaLauncher`

**核心建议**：
- verify 子状态走独立列（不与 `status` 共枚举），避免状态爆炸
- `rejected` 当 verdict / result 字段，不当长期 status
- 路径 A（runtime actor 拉 verifier）优于路径 B（hook callback 内派生 turn），符合 P22 archtest "不在 callback 内长跑"

### 3. `gap-scale`

**结论**：P23 离千 node 百 agent 还有 7 大规模化缺口。

**瓶颈热点表**：

| 瓶颈点 | N=1000 影响 | file:line | 建议方向 |
|---|---|---|---|
| DAG 创建单事务 | 1000 次 UpsertNode 串行；事务长锁强 | `internal/sidecar/orch/orchestration/dag.go:109-126,202-208,211-220`、SQL `internal/sidecar/orch/sql/queries/task_dag_node_write.sql:1-12` | 批量 insert / 拆批 / async / streaming |
| ready 计算 | JSONB depends_on 扫描，最坏 O(N²) | `0004_ack_dag.sql:58-62,70-71` | partial index `(dag_key, id) WHERE status='pending'` + 依赖计数列 |
| wakeup 表 | 5000 行/DAG，无 GC | `migrations/0023_dag_watcher_phase1.sql:9-36`、`internal/sidecar/orch/sql/queries/task_dag_wakeup_query.sql:1-16` | TTL / 按 DAG archive / 分区 |
| launcher 并发 | 当前无固定并发上限；百 agent 会直接并发打到下游 | `service_launcher_bridge.go` | 如需容量治理，走显式全局 token bucket / provider quota，不恢复硬编码上限 |
| hook 风暴 | 同步 dispatch，百 agent progress 阻塞 core | `hook_consumer.go:105-116,260-275,285-294` | non-blocking enqueue + worker pool + bounded queue |
| 状态存储 | result jsonb 承载 verifier/tool log，行膨胀 | `migrations/0004_ack_dag.sql:62`、`internal/sidecar/orch/sql/queries/task_dag_node_read.sql:1-18` | result 只存摘要，日志 spillover |
| 全量锁读 | `GetNodesForUpdate` 锁整 DAG | `internal/sidecar/orch/sql/queries/task_dag_node_read.sql:13-18`、`internal/sidecar/orch/store/taskdag/store.go:100-103` | 只 claim 小批 ready，`SKIP LOCKED` |

### 4. `gap-synth`

**结论**：三要求叠加放大效应让 P23 不能原样吸收三能力——**最弱环节是 hook consumer + DB CAS 队列回写**，不是 launcher。

**叠加冲突矩阵**：

| 组合 | 主要冲突 | 缓解 |
|---|---|---|
| 1 × 2 | sleeping 误判杀掉 verifying agent；verifier 失败触发 relaunch 循环 | 活性 actor 识别 `verifying/awaiting_verify` 子状态；relaunch 与 reject/repair 共用 fence |
| 1 × 3 | 千 node 全表扫描 + 百 agent health lookup 周期性尖峰 | 分片扫描 + lease jitter + 全局 backpressure |
| 2 × 3 | 每 terminal 多一轮 verifier，hook/DB/launcher 放大；互验同批 N² 配对 | 并发池 + 采样 / 分层互验 + verifier budget + 死循环上限 |

**改写决策表**：

| 选项 | 优劣 | 推荐 |
|---|---|---|
| 追加 P23 README | 改动小但边界失真 | ❌ |
| 重写 P23 README | 一次统一大 SM 但阻塞自驱底座 | ❌ |
| 拆分独立计划单 | 边界清晰但需跨计划依赖 | ✅ |
| 二阶段（P23 前段小 SM + P23 后段 P7/P8/P9 大 SM） | 风险最低 | ✅ 最优 |

**单一推荐**：不重写 P23；追加"未来扩展边界"章节 + 拆出 P7 / P8 / P9 三个 P23 后段子任务。

### 5. `gap-arbiter`（已收）

**结论**：推荐方案 **C（默认 runtime arbiter，judge opt-in）**；runtime arbiter 必须是独立 `dagArbiterActor`，**不能**塞进 hook consumer 同步执行。

**关键事实（基础设施考证）**：仓库内**没有**可直接复用的"非 agent 形态轻量 LLM 调用"。三个候选：

| 先例 | 结论 | file:line |
|---|---|---|
| Provider session contract | 不能直接复用——只有 `StartSession/ResumeSession/StartTurn`，无 `Complete(ctx, req)` 纯函数接口 | `internal/contract/provider.go:10-39` |
| prompt classifier | 可借鉴，**不宜直接复用**——借继承本机 `claude` CLI，鉴权/模型/timeout 都是特化路径 | `internal/module/prompt/classifier/claude_cli.go:16-27,52-85`、`service.go:17-25,42-79` |
| memory dream executor | 抽象可借鉴，**当前不可用**——`provider/codexapp/dream_executor.go:19-25` + `provider/claudecli/dream_executor.go:19-25` 两边都是 TODO | `internal/contract/dream.go:10-12`、`memory/extract.go:27,76-103`、`memory/auto_dream.go:140-150` |

**结论硬事实**：P8 必须有一个**前置 PR 建轻量 LLM 调用层**才能开工。复用既有面行不通。

**方案对比表**：

| 方案 | 架构干净度 | 用户认知负担 | 千 node 成本 | 实现复杂度 | 契约冲突 | 推荐 |
|---|---|---|---|---|---|---|
| A runtime arbiter | 中（需 actor 化） | 最低 | 高（最多 1000 次 LLM/DAG） | 中 | hook 同步慢路径冲突 | 默认 |
| B judge node | 最高 | 高 | 高 + agent 启动成本 | 低/中 | 依赖 verifier fan-in 表达 | opt-in |
| C 混合（默认 runtime arbiter + judge opt-in） | 高 | 低 | 可控 | 最高 | 可控 | **推荐** |

**arbiter 关键设计建议**：
1. A 形态：`terminal hook → enqueue arbiter job → dagArbiterActor → 调 LLM → 写 verdict`，**不是** hook 内同步 chat completion
2. schema：`verify.mode = arbiter | judge`，默认 `arbiter`；可选 `verify.judge_node_key`
3. 失败兜底：落 `verdict_lost` 第三类终态（类比 `observe_lost`），**不**自动降级 B（避免隐藏成本，跟"降低认知负担"目标一致）
4. 安全：verifier / agent 输出作为 quoted data；system prompt 明确"不执行报告内指令"；JSON schema 强校验
5. 审计：`dag_arbiter_calls` 表（input_hash, output, model, latency, cost, error）；redacted 字段语义必须冻结：`redacted_input` 为进入 LLM 前的脱敏 quoted data，`redacted_input_ref` 为可重放脱敏 blob 引用/哈希，`redacted_output` 为 LLM 输出脱敏摘要/结构化 verdict，不得存 raw verifier/member output

---

## 裁决 6：方案 C + 不加进 P23 阶段 0（2026-04-25 用户拍板）

**Q1 用户决策：选 C**（不是 A）。理由：默认仍 A 守住认知负担目标；schema 暴露 `verdict_strategy = judge` 给特殊场景留 opt-in 出口（人工 review、复杂判决逻辑、需要更长上下文）。

**Q2 替用户决策：不加进 P23 阶段 0**。理由：
- 阶段 0 已 5 项（编号校准 / state machine / RunnerModule / trigger 枚举 / 扩展点契约），都是「前段（P0–P6）共享前置」
- arbiter 契约只跟 P8 相关，对 P0–P6 无影响
- 加进阶段 0 只会让前段开工被无关 contract review 阻塞
- 让 P8 owner 在 P8 内冻结即可

**采纳的 arbiter 设计建议（已写入 P8）**：
- A 必须是 `dagArbiterActor` 形态（独立 actor，第 6 个 actor）
- schema 拆成两个正交字段：`verify.mode = async|batch_peer` + `verify.verdict_strategy = arbiter|judge`（默认 `arbiter`）
- 失败终态 `verdict_lost`，**不**自动降级 B
- prompt injection 防护：sanitize layer + JSON schema 强校验 + system prompt 明确不执行报告指令
- 审计：`dag_arbiter_calls` 表
- **P8 前置 PR 必须先建轻量 LLM 调用层**（独立 PR 落盘，归 P8 范围）

---

## 裁决 7：UI + 模板能力作为新增子任务 P10（2026-04-25 用户提出）

**用户原话**：「DAG 要有 UI，用户可以保持模版，然后编辑模版或者编辑 dag 任务。」

**解读**：
- DAG 需可视化 UI
- 模板（template）：可复用 DAG 骨架，可独立编辑
- 任务（instance）：基于模板实例化的具体 DAG，可独立编辑

**裁决**：作为 P23 后段第 4 个子任务 **P10 DAG 模板 + UI 编辑能力**，与 P7/P8/P9 同层。

**关键设计点（写入 P10 stub）**：
- `dag_templates` 表 + `dag/template/*` RPC
- `dag/instantiate(template_key, params)` RPC：基于模板实例化 DAG，并把模板 snapshot 拷贝进 task（解耦后续模板修改影响）
- UI：模板库 tab + 任务列表 tab；模板编辑器（拓扑可视化 + 节点表单）
- 编辑权限：未推进节点可编辑；已推进节点只读（CAS fence）
- 依赖：P3（owner_id/tenant_id）+ P6（外部 RPC AuthN，UI 调用方需鉴权）

---

## 裁决 8：5 项进阶要求拆分（2026-04-25 用户提出）

**用户原话**：「UI（一眼可以看懂怎么设置，可视化、配置化，可由 agent 创建 DAG 用户手动微调，用户使用模板功能创建 DAG，可以添加模板），自动节点伸缩（无限迭代），LLM 裁决（蜂群涌现），RPC 端点（外部调度），JSON 输出模式（金融场景）。」

**逐项映射**：

| 用户要求 | 归属 | 处理 |
|---|---|---|
| UI（一眼看懂、可视化、配置化、agent 创建 + 用户微调、模板创建、添加模板） | **P10 扩展** | 在 P10 stub「目标」段后追加「UX 设计原则」+「三个用户故事」段（authoritative） |
| 自动节点伸缩（无限迭代） | **新建 P11** | `P11_DynamicNodeGrowth.md`：`task_spawn_child` 工具 + `growth_budget` 硬约束 + `convergence` 终止条件 |
| LLM 裁决（蜂群涌现） | **新建 P12（P8 升级）** | `P12_SwarmArbiter.md`：N 个 LLM 并行 + consensus + dissent_action；P8 单 arbiter 的 ensemble 升级 |
| RPC 端点（外部调度） | **P6 归属 / 未实现** | 不新建子任务；P6 是外部调度入口的归属切片，但当前仍为 stub，必须补 endpoint matrix、三入口 identity、安全与幂等闭环后才能开放 |
| JSON 输出模式（金融场景） | **新建 P13** | `P13_StrictJSONOutput.md`：`output_schema` + validator + repair-or-fail + 金融场景预设 |

**追加的冲突缓解契约（README §三子任务叠加冲突缓解契约 第 6/7/8 条）**：

6. P11 spawn 入口必须经 `SpawnChildNodes` 服务函数 + growth_budget 硬约束；不允许绕路直接 INSERT
7. P13 output_schema 验证发生在 P8 verify gate **之前**（语法层先于语义层）；invalid 直接 repair/fail，**不**进 verify_phase
8. P12 swarm 调用共用 P9 全局 token bucket（与 P8 单 arbiter 同一通道）

**新增的关键依赖**：
- P11 依赖 P9 backpressure（动态生长 × 规模会形成倍数压力）
- P12 依赖 P8 已合入（特别是 `internal/sidecar/orch/orchestration/llm/light/*` 轻量 LLM 调用层；原写 `internal/llm/light/*` ✅ 已修正）
- P13 依赖 P8 sanitize layer（agent 输出在 validate 前的安全处理）

**金融场景的"复合预设"（写入 P10 模板库 + P13 默认）**：
- `output_validation.on_invalid = repair`
- `verify.verdict_strategy = arbiter` + `arbiter_swarm.consensus.strategy = unanimous` + `dissent_action = verdict_lost`
- `output_schema.additionalProperties = false`
- `dag.audit_log = true`
- 这是 P12 + P13 + P10 三者协同的实例

---

---

## 终极裁决（authoritative，已写入 README）

### 裁决 1：P0–P6 前段保持"自驱底座 + 三触发源"，后段能力留在 P23 P7–P13

**采纳 `gap-synth` 推荐**。理由：
- P0–P6 前段职责是"DAG 自驱底座"，三要求若塞进前段会把它变成 mega-plan
- 三要求需要重设大状态机，不能污染 P23 阶段 0 / P0–P6 小 SM
- 先交付 P23 前段可为后段子任务（探活、verifier、规模化）提供稳定 CAS / actor / hook 基线

**写入位置**：README §"后段扩展边界"；非目标段只说明 P0–P6 不实现，不能写成 P23 外事项。

### 裁决 2：拆出 P7 / P8 / P9 三个 P23 后段子任务

- **P7 心跳式节点活性监控**：承接要求 1
- **P8 校验闭环**：承接要求 2 + verdict 实现采用方案 C（默认 runtime arbiter，judge 仅 opt-in）
- **P9 大规模 DAG 调度**：承接要求 3

**写入位置**：README §"后段扩展边界" 三个子节，明列字段、依赖、reject conditions。P7_LivenessProbe.md / P8_VerificationGate.md / P9_ScaleScheduling.md 三个子任务文件留待后续会话编写，都在 `docs/plans/迁移/p23/` 内。

### 裁决 3：P23 阶段 0 追加"扩展点契约"作为第 5 件冻结

避免 P7 / P8 / P9 上线时返工，本期必须先冻结：

1. `task_dag_nodes` 预留 `last_activity_at TIMESTAMPTZ` 列（在 `0065_dag_state_machine.sql` 一并加，本期不消费但 P2 hook tap 必须回写）
2. P2 reconcile hook tap 必须 enqueue-only（P8 verifier gate 硬前置）
3. P23 主 `status` 枚举固定，未来子状态走独立列（如 `verify_phase` / `activity_state`），保持 CAS 形状不变
4. launcher 当前无固定并发上限；如需容量治理，后续 PR 必须显式引入 quota / token bucket，不能恢复硬编码上限
5. launcher 全局 quota 占用方在 P8 引入 verifier 后必须包含 verifier launch（不允许双队列）

**写入位置**：README §"阶段 0：前置冻结" 新增第 5 项。

### 裁决 4：verdict 仲裁器走方案 A（已 superseded；最终以裁决 14 / P8 方案 C 为准）

**历史裁决，已被后续交叉验证裁决 superseded。** 保留原文用于追溯；实现不得按“失败自动降级 B”落地，最终以 P8 方案 C：默认 runtime arbiter、judge 显式 opt-in、失败 `verdict_lost` 为准。

**关键约束（写入 README §未来扩展边界 P8 段）**：
- DAG runtime 在 verifier terminal 后直接发起一次 LLM chat completion（structured output），输入 verifier 报告集合，输出 `{verdict, reasons, repair_prompt?}`
- **superseded：不得自动降级 B**。arbiter LLM 调用失败（服务挂、超时、JSON parse 失败）时落 `verdict_lost`；只有显式 `judge_node_key` 的 opt-in 路径才能拉 judge node
- **prompt injection 防护**：verifier 报告进 arbiter 前必须经 sanitize layer；禁止 verifier 输出直接当 prompt 传入
- **审计**：每次 arbiter 调用落一行 `dag_arbiter_calls` 表（输入 hash / 输出 / model / latency / cost）
- **batch 聚合**：千 node × 每 node 一次 = 千次 LLM 调用 / DAG，必须配 batch；P9 规模下不允许走开环
- **现有基础设施考证**：等 `gap-arbiter` 报告补充

### 裁决 5：三要求叠加冲突缓解契约由 P23 锚定（authoritative）

写入 README §"三要求叠加冲突缓解契约"：

1. 活性 actor 与 verifier gate 共用 CAS fence，禁止双推进（P7 + P8）
2. 活性扫描分片 + lease jitter（P7 + P9）
3. verifier launch 共用 launcher 全局 quota（P8 + P9）
4. arbiter 调用 batch 聚合 + 失败 `verdict_lost`，judge node 仅显式 opt-in（P8 + P9）
5. P7 / P8 / P9 不允许修改 P23 主 `status` 枚举；新状态走独立列（保 CAS 形状）

---

## 写入清单（已落盘）

| 文件 | 改动 | 状态 |
|---|---|---|
| `docs/plans/迁移/p23/README.md` | 阶段 0 加第 5 项扩展点契约（裁决 3） | ✅ 已写 |
| `docs/plans/迁移/p23/README.md` | 风险段加"与未来扩展耦合风险"子节（裁决 5） | ✅ 已写 |
| `docs/plans/迁移/p23/README.md` | 非目标加一条明示 P0–P6 不实现三能力（裁决 1） | ✅ 已写 |
| `docs/plans/迁移/p23/README.md` | 新增"后段扩展边界"章节（裁决 1 + 2 + 4 + 5） | ✅ 已写 |
| `docs/plans/迁移/p23/RESEARCH_VERDICT.md` | 本文件 | ✅ 已写 |
| `docs/plans/迁移/p23/P7_LivenessProbe.md` | P7 子任务单（活性探针） | 🔄 stub 已建 / 已补活性硬契约 |
| `docs/plans/迁移/p23/P8_VerificationGate.md` | P8 子任务单（校验闭环 + arbiter，方案 C） | 🔄 stub 已更新（采纳 arbiter 设计建议） |
| `docs/plans/迁移/p23/P9_ScaleScheduling.md` | P9 子任务单（大规模调度） | 🔄 stub 已建 / 已补容量硬契约 |
| `docs/plans/迁移/p23/P10_TemplateAndUI.md` | P10 子任务单（模板 + UI + UX 原则 + 三用户故事） | 🔄 stub 已扩展 |
| `docs/plans/迁移/p23/P11_DynamicNodeGrowth.md` | P11 子任务单（动态节点生长） | 🔄 stub 已建 |
| `docs/plans/迁移/p23/P12_SwarmArbiter.md` | P12 子任务单（蜂群涌现仲裁） | 🔄 stub 已建 |
| `docs/plans/迁移/p23/P13_StrictJSONOutput.md` | P13 子任务单（JSON 严格输出 / 金融合规） | 🔄 stub 已建 |
| 本文件 §5 gap-arbiter 完整结论 | 报告补充 | ✅ 已写 |

---

## 待办

1. ✅ gap-arbiter 报告归档（§5）
2. ✅ P8 现状段补 LLM 基础设施考证锚点（已确认三处都不可直接复用）
3. ✅ P8 stub 写明"先建轻量 LLM 调用层"作为前置 PR
4. ✅ P8 schema 升级为 `verify.mode + verify.verdict_strategy` 两维度
5. ✅ P8 失败终态加 `verdict_lost`
6. ✅ P10 子任务单 stub 新建
7. 🔲 P0–P9 各 stub 在 owner 启动前需深入补完「推荐架构」「改动清单」段
8. 🔲 P10 owner 需调研 Wails / 现有 UI 框架对应接入点（暂未做）

---

## 决策签名

- **裁决人**：用户（2026-04-25）
- **执行人**：本会话 Claude（基于 5 路 codex 调研报告）
- **可追溯性**：5 路 codex agent 的 thread_id 在 `mcp__orch__orchestration_list_agents` 输出中有完整记录；本文件第 1-4 节摘要已固化关键 file:line 锚点


---

## 第二轮调研（2026-04-25）：6 路 impl/compliance 调研

派 6 个 agent 并行调研「P23 14 子任务怎么实现 / 是否符合契约 / 怎么确保符合」。报告全部落 RESEARCH_VERDICT 和裁决 9。

### 6. `impl-front-P0-P3` 摘要

**结论**：P0–P3 可行，但 **P3 落点必须修正**——stub 写 `internal/orchestration/dag.Start` 与 README §"当前基线约束"（DAG runtime 默认归 `cmd/mcp-orch`）冲突。建议改为 `internal/sidecar/orch/orchestration/dag_start.go` 的 `StartDAG(ctx, dagKey, triggerMeta)`，**不**膨胀 `dag.go`。

**关键设计建议**：
- P0 状态机 SQL：把现有 `task_dag_node_runtime.sql:24-42` 加参数化 `WHERE current_status=$expected` CAS；继续复用 `migrations/0023_dag_watcher_phase1.sql:9-43` 的 wakeup / lease 表，不另建 fence 表
- P1 双触发 CAS：反向 `dag_ref` bind 先按 `(dag_key, node_key, status='running')` 查；如果 `assigned_agent_id != ''` 直接 noop
- P2 队列选 **内存 bounded channel + reconcile recovery scan**：callback 只 enqueue（满足 P22 callback 轻量规则），持久化靠 node runtime 状态恢复，不混用 wakeup 表承载 terminal 队列
- P3 idempotency：`dag/start` 用 `(dag_key, trigger, idempotency_key)` 或 DAG 级 `last_trigger_*` CAS 防重

**契约符合性**：除 P3 落点外，其它项已对齐 `modularity-convention §4.4 §7` / `fx-convention §2 §3` / `rungroup-convention §2 §4`。

### 7. `impl-trigger-P4-P6` 摘要

**结论**：P4–P6 可做，但**必须串行**（P3 → P4/P5/P6）。最大风险是 P5 跨 root：

- **P5 跨 root 违规**：`internal/module/cron/scheduler.go:288-305` 当前直接调 `StartTurn`；扩展时如直接 import `internal/sidecar/orch/orchestration` 会被 `internal/archtest/dependency_direction_mcp_orch_test.go:49-53` 拦截。✅ 修正：cron 模块定义 `TriggerSink` interface，mcp-orch 装配实现 bridge
- **P5 idempotency 缺失**：cron run 当前 idempotency 是 UUID（`internal/module/cron/scheduler.go:318-326`），不是 `hash(cron_job_id, scheduled_at)`。✅ 修正：DAG 路径用 deterministic key + 唯一约束 `(job_id, scheduled_at, target_dag_key)`
- **P5 已落地依据**：P21 P1b 已实际合入（`internal/app/modules.go:58-60`、`internal/module/cron/module.go:31-32`、`internal/module/cron/tick_actor.go:41-60`）
- **P6 AuthN middleware**：必须分两层——transport 提取 identity 到 ctx + service 层统一 AuthN/AuthZ/rate/quota/audit；不能只挂 `internal/sidecar/orch/orchestration/rpc.go`，因为 TCP / Wails / MCP HTTP 三种入口形态不同
- **跨 root 边界**：cron→DAG 不能 core 直接 import orch concrete；DAG runtime 调用面必须经 hook / interface

### 8. `impl-quality-P7-P8-P12-P13` 摘要

**结论**：可行，但 **P8 前置 PR 是关键阻塞**——必须先合 `internal/sidecar/orch/orchestration/llm/light/*`。

- **`internal/llm/light/*` 落点违反 allowlist**：`docs/契约/modularity-convention.md:336-377` 的 cmd/mcp-orch 允许 import 清单不含此包；模块名录也不含。✅ 已修正：改为 `internal/sidecar/orch/orchestration/llm/light/*`
- **不复用 `dream.go`**：`internal/contract/dream.go:10-12` + `provider/codexapp/dream_executor.go:19-25` + `provider/claudecli/dream_executor.go:19-25` 当前 TODO 不可用；且缺 role/message、schema、timeout、usage、审计字段
- **codex / claude structured output 锚点**：codex 仓库未见 OpenAI JSON mode / function calling 锚点（不能宣称 hard guarantee）；claude 有 `tool_use` 事件解析（`internal/provider/claudecli/factory.go:148-168`）但 CLI 只通过 `--mcp-config` 接工具（`transport_config.go:185-203`），未见"output_tool schema 强制返回"入口
- **fallback 代价**：只能 prompt 要求 JSON + runtime validate；无 hard guarantee
- **verdict CHECK**：P8 arbiter 表/列必须对 `parsed_verdict` 加 DB CHECK；P12 `dag_swarm_consensus.final_verdict` 必须加 DB CHECK，避免自由文本 verdict 污染状态机
- **archtest 例外**：P13 hook 内同步 schema validate 需 archtest 例外（**已写入 P23 阶段 0 ⑤**）：只允许 parse + validate + enqueue / 轻量 CAS，禁网络 / LLM / 阻塞循环
- **共享 sanitize**：抽 `internal/sidecar/orch/orchestration/runtime/arbiter_sanitize.go` 给 P8 arbiter / P12 swarm / P13 repair_prompt 共用
- **合入关系**：P12 不能与 P8 独立并行，必须 P8 + 轻量 LLM 后；P13 可在 P8 sanitize 合入后独立

### 9. `impl-scale-P9-P10-P11` 摘要

**结论**：三线**不能并行**，先 P9 背压契约，再 P11 spawn，最后 P10 消费稳定 schema / 状态流。

- **P9 批量 create**：建议 service 层 async job + 分批事务（不新增 streaming RPC）；partial failure 标记 `incomplete`，cleanup job 处理
- **P9 partial index 迁移成本**：百万级 reindex 必须 `CREATE INDEX CONCURRENTLY`，迁移分两步（先列回填，再建索引）；`remaining_deps` 只能在 CreateDAG / Spawn / Edit 事务内维护，禁异步补偿
- **P9 hook worker pool / tap registry**：当前 hook callback 同步 dispatch（`hook_consumer.go:105-116,260-275,285-294`）；改成 `dag_terminal_tap` / `hook_tap_registry.go` bounded enqueue + Runner worker，初值 workers=4 / queue=1000 / drop 拒非关键 progress、terminal 不丢只反压；这是 P21 Observation Contract consumer 级重构
- **P10 UI 现状**：Wails runner / WS 已就位（`internal/ui/wails/http_server.go:39-46`），真实前端是 `cmd/agent-terminal/frontend/` Vue/Vite（`package.json:5,39-42`）；DAG 详情 modal 仍是 stub（`cmd/agent-terminal/frontend/vue-app/components/DagDetailModal.js:1-5,52-54`）；mermaid 依赖已存在（`package.json:33`，`mermaid-renderer.js:22-26`）
- **P10 编辑 CAS**：新建 `task_dag_node_edit.sql`，与 dispatcher CAS 竞争（谁先提交谁赢）
- **P11 spawn 入口**：放 `internal/sidecar/orch/orchestration/dag_spawn.go`；archtest grep `INSERT INTO task_dag_nodes`，只允许 `UpsertTaskDagNode` / `CreateDAG` / `SpawnChildNodes` 出现
- **P11 收敛 evaluator**：必须是 `internal/sidecar/orch/orchestration/runtime/convergence_actor.go` Runner actor（不是 cron），遵守 `rungroup-convention §2 §4`

**资源峰值估算**：base=1000 + P11 放大到 N=10000 + P12 swarm 3 verdict/节点 = 最坏 30000 verifier/LLM job；缓解依赖 P11 budget cap + 80% backpressure + P8/P12 共用 P9 token bucket。

### 10. `contract-compliance-master` 摘要

**整体合规风险评级：高**。原因：P3 / P5 / P8 / P13 stub 已经出现模块落点 / fx/run.Group / hook callback 特批的潜在漂移；后段 P11 / P12 / P13 又会把 launcher / LLM / hook / DB CAS 压力叠加。

**14 子任务契约扫描结果**（`✅` 已对齐 / `⚠️` 风险点 / `❌` 违规）：

- P0: ✅✅⚠️（actor 必须 `group:"runners"` 不 fire-and-forget）
- P1: ✅✅✅
- P2: ✅⚠️⚠️（hook tap enqueue-only；timeout 扫描必须 actor）
- P3: ⚠️✅✅（落点漂移 → ✅ 已修正）
- P4: ✅✅✅
- **P5: ❌⚠️⚠️**（cron import orch 违规 → ✅ 已修正）
- P6: ✅✅✅
- P7: ✅✅⚠️
- P8: ⚠️✅⚠️（`internal/llm/light/*` 不在 allowlist → ✅ 已修正）
- P9: ✅⚠️⚠️
- P10: ⚠️✅✅
- P11: ✅✅⚠️
- P12: ✅✅⚠️
- P13: ⚠️⚠️⚠️（hook 同步 validate 需例外 → 已写入阶段 0 ⑤）

**migration 编号冲突**：旧草案假设 P23 可复用已占编号并预留一个中间缓冲号，但 HEAD 已有 `0063_agent_thread_name.sql` 与 `0064_skill_candidates.sql`。✅ 已修正：P23 暂从当前 HEAD 下一个可用编号 0065 起排；每个 migration PR 前必须重新校准。

**Phase0 hard blocker**：`go test ./internal/archtest/... -count=1` 当前因 `internal/module/uistate/timeline/projector_parity.go:12:2` unused import `pkglogger` build failed；`internal/archtest/dependency_direction_mcp_orch_test.go` 仍宽泛放行 `internal/store` / `internal/module`。Phase0 PR 必须真实修复 build 与 allowlist，否则 P0 runtime blocked。

**整体 stop conditions**：5 类信号触发停工 / 重构（详见 [`COMPLIANCE_GATES.md`](COMPLIANCE_GATES.md) §"触发 stop / 重构条件"）。

### 11. `compliance-gate-design` 摘要

**七层 gate 体系**（详见 [`COMPLIANCE_GATES.md`](COMPLIANCE_GATES.md)）：L1 schema / IDE → L2 pre-commit → L3 archtest → L4 CI → L5 merge → L6 runtime alert → L7 scheduled audit。

**关键缺口**：
- 仓库**无** `.github/workflows/`，且仓库内尚无 PR template：CI gate 当前不存在；Phase0 gate PR 必须新增 CI workflow，或新增 PR template / 等价可核验机制承载 commit SHA、命令完整输出、reviewer `P23-manual-gate: verified` 签收
- `internal/platform/metrics/metrics.go` 只有 counter 声明，**无** promhttp exporter / `/metrics` 暴露面，且无 executable scheduled-audit artifact：P7 前必须补 promhttp，或落地脚本/命令卡 artifact
- Scheduled audit 设计完整，但实施依赖 L4/L6 先就位

**实施成本** + **防御能力**评估：L1 + L3 + L4 必须做（合规底座）；L2 + L5 推荐；L6 + L7 可选。

---

## 裁决 9：6 路调研结论合并（2026-04-25）

### 9.1 已修正的关键漂移（已写入 README + stub）

1. **P3 落点**：`internal/orchestration/dag.Start` → `internal/sidecar/orch/orchestration/dag_start.go` 的 `StartDAG(ctx, dagKey, triggerMeta)`
2. **P5 跨 root**：`internal/module/cron` 不直接 import `cmd/mcp-orch`；改为 cron 定义 `TriggerSink` interface，mcp-orch 装配实现 bridge
3. **P5 idempotency**：UUID 改为 deterministic `hash(cron_job_id, scheduled_at, target_dag_key)` + `cron_job_runs` 唯一约束 `(job_id, scheduled_at, target_dag_key) WHERE target_dag_key <> ''`
4. **migration 编号**：HEAD 已占 0063/0064；P23 暂从 0065 起排，并要求每个 migration PR 前重新校准
5. **`internal/llm/light/*` 落点**：改为 `internal/sidecar/orch/orchestration/llm/light/*`（只服务 DAG arbiter，未来其它模块需要再升级到 `internal/platform/llm/light`）
6. **P13 hook 同步 schema validate 例外**：写入 P23 阶段 0 ⑤（archtest `dag_hook_tap_enqueue_only` 白名单：parse + validate + enqueue，禁网络 / LLM / 阻塞循环）
7. **archtest 清单**：执行权威迁入 COMPLIANCE_GATES；README 只引用 gate key，不维护第二清单
8. **共享 sanitize layer**：抽 `internal/sidecar/orch/orchestration/runtime/arbiter_sanitize.go` 给 P8 / P12 / P13 共用

### 9.2 未修正的关键缺口（必须在 P0 启动前补 / 或在 README 风险段标记）

9. **CI/manual fallback 载体不存在**：仓库无 `.github/workflows/`，仓库内尚无 PR template。L4 gate 需要先建 workflow；未建前 Phase0 gate PR 必须新增 PR template 或等价可核验机制（commit SHA + 完整输出 + reviewer 签收）。已写入 COMPLIANCE_GATES。
10. **runtime metrics / alert 缺口**：仓库只有 counter 声明，无 promhttp exporter / alert 链路，也无 executable scheduled-audit artifact。P9 SLO + P11 budget alert + P6 audit fail alert 都依赖此能力——P7 前必须补 promhttp exporter，或落地脚本/命令卡 artifact。已写入 COMPLIANCE_GATES / README 风险段。
11. **P9 hook worker pool 是 P21 Observation Contract 级重构**：terminal precedence / 归因不能变。owner 启动前必须读 P21 Canonical Turn Observation Contract。已写入 README 风险段。
- **旁支 Low：根 README conflict marker**：如根 `README.md` 存在 conflict marker，只记录为 P23 外旁支低风险；本修订任务不改根 README，也不作为 P23 gate blocker。

### 9.3 整体合规风险评级：**高**

理由：14 子任务跨切片复杂度 + 现有合规基础设施缺口（无 CI / 无 PR template / 无 alert artifact / archtest 当前 build failed / P23 21 项 archtest skeleton 需 Phase0 新建）。

**三条最关键守门动作**：
1. 先修复 archtest build failed + 落 P23 21 项 archtest skeleton + migration sequence guard（L3）—— 当前 `go test ./internal/archtest/...` 因 `projector_parity.go` unused import 失败；Phase0 不绿则 P0 runtime blocked
2. 已落 P3 / P5 / P8 落点修正（README + stub 已修）
3. P8 / P9 / P12 / P13 共用 quota + sanitize + output-before-verify 三联门（L4 merge hard gate）

### 9.4 写入清单（已落盘）

| 文件 | 改动 | 状态 |
|---|---|---|
| `README.md` | migration 编号修正（P5 → 0066）；archtest 清单扩 14 项；阶段 0 ⑤ 加 P13 schema validate 例外；风险段加合规风险评级 + 缺口 | ✅ 已写 |
| `P3_ExplicitDAGStartAndOwnership.md` | `StartDAG` 落点修正 | ✅ 已写 |
| `P5_CronTriggerSurface.md` | 跨 root TriggerSink + deterministic idempotency + 编号 0066 + archtest 守 | ✅ 已写 |
| `P8_VerificationGate.md` | LLM light 落点修正 + 共享 sanitize | ✅ 已写 |
| `P12_SwarmArbiter.md` | LLM light 锚点同步 | ✅ 已写 |
| `P13_StrictJSONOutput.md` | hook 同步 validate 例外说明 + 共享 sanitize 锚点 | ✅ 已写 |
| `COMPLIANCE_GATES.md` | 新建 — 七层 gate 体系 + 14 archtest 详情 + CI workflow 设计 + runtime alert + scheduled audit + owner 自查清单 | ✅ 已写 |
| `RESEARCH_VERDICT.md` | 本文件 §6–§11 + §裁决 9 | ✅ 已写 |

### 9.5 待办

- 🔲 阶段 0 之前补 CI workflow（或定 fallback 方案）
- 🔲 阶段 0 之前补 promhttp + alert 基础设施（或定 scheduled audit + log only fallback）
- 🔲 P9 owner 启动前必须读 P21 Canonical Turn Observation Contract
- 🔲 P0–P13 各 stub 在 owner 启动前需深入补完「推荐架构」「改动清单」段（基于本轮 6 路调研已经具体化的方向）

---

## 12. `a7-user-ux` 摘要（2026-04-25）

P10 方向正确，但仍是“规划型 UX”：现 UI 只有 DAG 列表与占位详情（`DagDetailModal.js:44-54`），关键可用性未闭环。十维表明：拓扑与状态色卡未营建；P11 `growth_budget` / P13 表单未覆盖；`ErrCallerIdentityRequired` / `verdict_lost` 无人话翻译；`task_create_dag` 后不自跑且无 Start UI（`app.js:526-535`）；模板库/fork preview/lineage 均未交付；WS 推 DAG/verify/repair/budget 事件未决；只读原因/锁图标未规范；金融预设“发现→预览→应用→审计说明”断链；UI 内无字段帮助 / 示例导入。三个用户故事身上都会袭障。**最严重 3 项**：(1) 真实 DAG UI 是占位、P10 拓扑/表单/Start 均未落地；(2) 错误与状态只停在内部枚举/终态，缺人话解释与修复入口；(3) 模板 / 金融预设 “发现→预览→应用→审计” 断链，合规能力存在但不可用。

## 13. `a8-ecosystem-deps` 摘要

总体可控，阅断项是 provider 强结构化能力未接入 / Wails v3 alpha / d3 UI 复杂度。十维中：OpenAI structured outputs / Claude tool use 能力“存在但未接入”（provider 抽象未暴露）；P13 不能宣称 hard guarantee；Mermaid 已于前端依赖中，d3 是 UI 趋势项；go.mod 需加 `github.com/santhosh-tekuri/jsonschema/v6`（Apache-2.0）；promhttp 已随 client_golang 内含；`pgcrypto` 禁用 / SKIP LOCKED 要求 PG≥9.5；`robfig/cron/v3` 在用但 DST 语义未明；Wails v3 alpha API 不稳；archtest 是自研，不需三方库。**最危险 3 项**：(1) Codex/Claude structured output 断层，runtime validate 必须兑现保证；(2) Wails v3 alpha 升级风险高；(3) d3 编辑器 UI 工程量 / 可维护性。

## 14. `a9-migration-rollout` 摘要

rollout 面包含足，但可执行度不够：migration 编号/拓扑仍不一致、兼容期无明确移除窗口、CI/metrics fallback 无硬截止。十维：README 拓扑残留 P5=0064；P9 无编号；P10 占 0068；旧路径兼容期无截止；partial index 未带 `CONCURRENTLY`；`created_by→owner_id` 双写无终点；cron 双轨未定 release 窗口；schema 冻结未定时长；archtest 一次全开贵；L4/L6 在无 CI/promhttp 场景下却当硬门。建议：P9 scale migration 插 0068，P10–P13 顺延到 0069–0072；deprecation 窗口表化 + 1 release 评估 hard fail；P0 初 5 archtest hard，P6 10 hard，P9 后 14 hard。**最严重 3 项**：(1) 编号/依赖不一致；(2) 大表迁移阻塞写（partial index 无 CONCURRENTLY）；(3) 合规基础设施缺位但 L4/L6 被当硬门。

## 15. `a10-economics` 摘要

金融场景月度成本估算（1000 DAG × 1000 node × swarm 3 member × 3k input + 2k output）：GPT-5.4 ≈ \$112,500/月；Claude Sonnet 4.6 ≈ \$117,000/月。存储粗估 13MB/DAG，活表 13GB/月，DB 膨胀 3×≈40GB。**最严重 3 项成本爆炸**：(1) P12 swarm × P11 growth 叠乘（1000 → 10000 node × 3 = 30000 LLM job 峰值）；(2) 无全局 token bucket / subscription 映射，verifier+swarm+launcher 分队列会雪崩；(3) 热表膨胀（`result jsonb` + arbiter / validation 多表写盘叠加） + UI 全量渲染。P0–P13 优先级口径：P0/P1/P2/P3/P6/P9 是必做，P8/P13/P7/P11 推荐，P12/P5/P4 可选，P10 推荐但需控制 UI scope。

---

## 裁决 10：10 路全量调研合并裁决（2026-04-25）

### 10.1 10 个 agent 核心结论（一句话）

| agent | 一句话结论 |
|---|---|
| a1-arch-conformance | P22 allowlist + modularity 名录与 P23 stub 还有 3 项 high 漂移（原写 `internal/llm/light` 残留 / archtest allowlist 宽 / `dag.Start` 原表述）。 |
| a2-failure-mode | 复合故障三主釴：hook overflow + CAS 竞争 + verifier lost 双反状态；cron 重 claim + 旧 trigger fallback + idempotency 漏；growth + swarm + wakeup GC 打爆 DB/LLM。 |
| a3-perf-scale | N=10000 击穿于 DB ready/lock 全量读 / hook+launcher 背压缺失 / LLM verdict 开环；P9 必上 SKIP LOCKED + bounded queue + token bucket。 |
| a4-security | Critical 三项：跨租户 / 鉴权绕过 / 特权升级；金融场景必加 append-only + hash chain audit + PII redaction。 |
| a5-data-integrity | `task_dag_node_write.sql:14-20` 裸 status 写 / `CompleteTaskDagNode` 不看 active_turn_id / 多表无 FK 闭环。 |
| a6-operability | 无 promhttp / 5 SRE 指标未冻结 / on-call runbook 五类未写 / 七 actor graceful shutdown 顺序未定。 |
| a7-user-ux | DAG UI 是占位，错误与金融预设 “发现→预览→应用→审计” 断链，三个用户故事都袭障。 |
| a8-ecosystem-deps | provider 强结构化能力未接入；Wails v3 alpha；d3 UI 工程量是三大依赖面风险。 |
| a9-migration-rollout | migration 编号/依赖还乱（P9 无号 / P10 占 0068）；兼容期无截止；L4/L6 被当硬门但基础设施不存在。 |
| a10-economics | 金融默认 swarm 3 × 1000 DAG ≋ \$112k–117k/月；swarm×growth 叠乘 30000 LLM job 峰值；无全局 token bucket 会雪崩。 |

### 10.2 跨 agent 共识发现（按严重度）

| 项 | 点名者 | 严重度 |
|---|---|---|
| sanitize layer 必须先于 arbiter/swarm/P13 合入 | a4 + a2 + a8 + a10（间接） | **P0 / S0** |
| 租户 filter / RPC AuthN 是鉴权闭环不可发布项 | a4 + a5 + a9（CI 提示） | **P0 / S0** |
| audit append-only + hash chain | a4 + a5 + a6 | **P0 / S0** |
| `internal/llm/light` 残留引用 | a1 后被 a8/a9 重点名 | **P0 / S0**（本 PR ✅ 已修正） |
| promhttp + alert 链路 | a6 + a9 + a10 | **P0 / S1** |
| token bucket / subscription 映射 | a3 + a8 + a10 + a2 | **P0 / S1** |
| `task_dag_nodes` 裸 status 写 + active_turn_id fence | a5 + a2 | **P0 / S1** |
| CI workflow / archtest 逐层转 hard | a1 + a9 | **P0 / S2** |
| UI 占位 → 实拓扑 / 实表单 / Start CTA | a7主 + a8/a10 | **P1（不阻 P0 代码）** |
| Wails v3 alpha / d3 依赖架构选型 | a8 + a7 | **P2 / 调研** |

### 10.3 立即修正项（必改文件清单，本 PR 已含）

- `README.md`：`internal/orchestration/dag.Start` → `internal/sidecar/orch/orchestration/dag_start.go:StartDAG`（L196）、P5 表述 `trigger=external` → `trigger=cron`（L174）、补 a3/a4/a5 风险段、补 5 项 archtest。
- `P5_CronTriggerSurface.md`：目标句 `trigger=external` → `trigger=cron`；补待办（DST/UTC、双轨窗口、append-only）。
- `P8_VerificationGate.md`：改动清单 `internal/llm/light` 改为 `internal/sidecar/orch/orchestration/llm/light`；依赖/待办补 sanitize / audit / cost。
- `P12_SwarmArbiter.md`：`internal/llm/light` 改为 `internal/sidecar/orch/orchestration/llm/light`；swarm actor 明示 `group:"runners"` + Runner.Run + interrupt/drain。
- `P13_StrictJSONOutput.md`：`internal/llm/light/codex_json_mode.go` / `claude_tool_mode.go` 改为合规路径；补 PII redaction / append-only / cache cap / archive TTL。
- `P9_ScaleScheduling.md`：N=10000 击穿补三项 + 补 SQL `FOR UPDATE SKIP LOCKED LIMIT K` / result 拆摘要 / actor buffer / CONCURRENTLY / subscription 映射 / turn fence。
- `RESEARCH_VERDICT.md`：本节 §12–§15 + §裁决 10。
- `COMPLIANCE_GATES.md`：14 archtest 补五项到 19；5 SRE metric；on-call runbook 5 类。

### 10.4 推迟项（写进相关 P stub 待办，不今天进 README/COMPLIANCE）

- a7 UX：错误 catalog，模板 preview / lineage / fork，WS 事件更新 → P10 owner 启动前冻。
- a8：Wails v3 alpha 升级 / d3 选型；vue 运行时双轨 → P10 owner。
- a8：claude tool use / codex JSON mode 在 provider 抽象上裁决 → P8 LLM light 前置 PR owner。
- a9：migration 编号纪律于 P9 插号 0068、P10–P13 顺延 → 阶段 0 依赖冻结 PR 主同调。
- a10：subscription × token bucket 映射表 / dry-run cost preview API 主体 → P9 owner 启动前交付。
- a2：terminal event 唯一键 `(dag,node,turn,event)` / `strict_state_machine` 默认 true / wakeup TTL DDL 先于 P11 上线 → P0 owner。

### 10.5 写入清单（本轮梳理，2026-04-25）

| 文件 | 改动概述 | 状态 |
|---|---|---|
| `README.md` | StartDAG 表述修正、P5 trigger=cron 修正、a3/a4/a5/共识风险 4 条、archtest 表补 5 项 | ✅ |
| `P5_CronTriggerSurface.md` | trigger 枚举修正 + 待办三项 | ✅ |
| `P8_VerificationGate.md` | LLM light 路径修正 + 待办四项 | ✅ |
| `P12_SwarmArbiter.md` | LLM light 路径修正 + group:"runners" 明示 + 待办四项 | ✅ |
| `P13_StrictJSONOutput.md` | LLM light 路径修正 + 待办五项 | ✅ |
| `P9_ScaleScheduling.md` | N=10000 三瓶颈风险段 + 待办六项 | ✅ |
| `RESEARCH_VERDICT.md` | §12–§15 + §裁决 10 | ✅ |
| `COMPLIANCE_GATES.md` | archtest 扩 19 项 + 5 SRE metric + 5 类 runbook | ✅ |
---

## 16. `a1-arch-conformance` 交叉验证摘要

✅ StartDAG 与 LLM light 落点已修；⚠️ P5 文案大多已对齐；❌ 真实 archtest allowlist 仍过宽，`dependency_direction_mcp_orch_test.go:23-29` 继续放行 `internal/store` / `internal/module`。新增裁决：P0 前必须收紧 allowlist，并在 README/COMPLIANCE 加具体测试名。

## 17. `a2-failure-mode` 交叉验证摘要

复合故障已有 enqueue-only、turn fence、裸写 gate、P9 quota 方向，但 terminal 唯一键、`strict_state_machine` 默认 true、wakeup TTL DDL 仍未硬化。裁决：写入 P0/P9 必修待办；P8 `failed`/`verdict_lost` 语义保持区分。

## 18. `a3-perf-scale` 交叉验证摘要

P9 覆盖 SKIP LOCKED、bounded queue、token bucket，但多在待办；P13 同步 validate 会压回 hook 热路径。裁决：P9 DDL 正文提升 `CREATE INDEX CONCURRENTLY` + 复合索引；P13 完整 validate 移到 worker，hook 只 bounded parse/enqueue。

## 19. `a4-security` 交叉验证摘要

AuthN、tenant filter、audit hash chain、redactor/sanitize 仍是文档 gate，未成可执行入口。裁决：安全项优先于 UX/成本；tenant/redaction/audit 维持 P0/S0，但 audit WORM 不能只靠 archtest 宣称闭环。

## 20. `a5-data-integrity` 交叉验证摘要

裸 status 写和 terminal turn fence 已进入 gate，但 SQL 草案未同步；P10 缺 `schema_hash`，预算 ledger 和 repair 链路 ID 缺。裁决：P10/P13 补 hash 与 `repair_chain_id`；统一 budget ledger 推迟到 P11/P9 owner。

## 21. `a6-operability` 交叉验证摘要

5 指标和 runbook 已落，但 metric 名称、stop 条件、graceful shutdown、trace propagation、sanitize fail 诊断仍不足。裁决：修正 `dag_*` 指标名和 runbook 文案；shutdown/trace/sanitize fail 写入后续 owner 待办。

## 22. `a7-user-ux` 交叉验证摘要

P10 保留三故事与 UX 原则，但错误 catalog、preview/diff/lineage、WS 实时事件、金融预设发现路径未形成可照办待办。裁决：P10 追加待办；UI 不阻 P0，但阻 P10 开工。

## 23. `a8-ecosystem-deps` 交叉验证摘要

依赖清单可控：jsonschema v6、promhttp 随 client_golang、hash 用 stdlib；风险是 provider structured output 不能 hard guarantee、metrics fallback 无硬截止、Wails/d3 推迟。裁决：P13 继续 runtime validate；COMPLIANCE 加 P7 前 exporter 截止。

## 24. `a9-migration-rollout` 交叉验证摘要

编号是阶段 0 阻塞项：HEAD 已占 0063/0064，P23 暂从 0065 起排；每个 migration PR 前必须重新校准。partial index 需 no-transaction `CREATE INDEX CONCURRENTLY` 正文；compat/CI/metrics cutoff 写入裁决。

## 25. `a10-economics` 交叉验证摘要

成本风险已进 VERDICT，但 P10 缺 cost preview。最大风险为 P11×P12 30000 LLM job、token bucket 故障、append-only 多表写盘。裁决：P10 加 Start 前 cost preview/二次确认；token bucket 故障进入 COMPLIANCE stop/runbook。

## 裁决 11 - 交叉验证仲裁（2026-04-25）

1. **a1 ❌ 优先级最高**：真实 archtest allowlist 未收紧，不能用新增文档 gate 冒充闭环；README/COMPLIANCE/P0 已写为 P0 前必修。
2. **migration 编号不静态冻结**：当前 HEAD 校准下 P23 暂排 P0=0065、P3/P6=0066、P5=0067、P8=0068、P9=0069、P10=0070、P11=0071、P12=0072、P13=0073；每个 migration PR 前必须重新检查 `migrations/` 并以当时 HEAD 下一个可用编号为准。
3. **P13 hook 热路径裁决**：a3 性能优先于原“同步完整 validate”写法；但 a4/a5 的 terminal 前校验不降级。最终方案：hook bounded parse/enqueue，`outputValidationActor` worker 在 terminal/verify 前完成 validate。
4. **安全与成本冲突**：a4 append-only/hash-chain/redaction 优先，a10 成本通过 P9 archive、P10 cost preview、token bucket hard stop 缓解，不取消审计。
5. **UX 与安全冲突**：P10 cost/quota preview 只展示预算、预计 token、RPM/TPM 是否足够，不暴露内部 quota 策略细节；AuthN/tenant filter 优先。
6. **operability hard gate 截止**：Phase0 gate PR 必须新增 CI 或 PR template/等价可核验 fallback 载体；P0 前必须有 promhttp/scheduled-audit artifact 明确方案；P7 前 exporter 或脚本/命令卡 artifact 不就位则 P7–P13 runtime alert 依赖项阻塞。

## 跨切片冲突仲裁矩阵

| 冲突 | 仲裁 | 同步影响 |
|---|---|---|
| a3 P13 validate 热路径 vs a4/a5 terminal 前强校验 | a3 热路径优先，a4/a5 顺序要求保留 | P13 改 worker；README archtest 例外改 bounded parse/enqueue |
| a4 audit hash chain vs a10 存储成本 | a4 优先 | P9 archive/TTL 与 P10 cost preview 必须覆盖审计膨胀 |
| a5 tenant/turn fence 复合条件 vs a3 SQL 热点 | 一致性优先，性能用复合索引抵消 | P9 DDL 补 `(tenant_id, dag_key, status, remaining_deps)` 与 `(dag_key,node_key,active_turn_id)` |
| a7 cost preview vs a4 quota 策略暴露 | 安全优先 | UI 只显示预算与是否足够，不显示内部限流算法 |
| a9 渐进 archtest vs a1 allowlist ❌ | a1 优先 | allowlist 收紧列 P0 前 hard；其它 19 项可渐进 hard |
---

## 26. `a1-arch-conformance` 全量复审摘要

a1 确认架构最大缺口仍在实现：`cmd/mcp-orch` dependency allowlist 仍过宽且直连 `internal/module/notify`；naked status write / terminal turn fence / task RPC identity / P23 archtest / StartDAG+4 actor+trigger enum 都未落地。裁决：实现缺口列为 P0/P6 hard gate，文档同步只修误导项。

## 27. `a2-failure-mode` 全量复审摘要

a2 指出 DAG 仍“只存不跑”、hook 同步、wakeup reclaim/TTL/FK 缺口、`EnqueueWakeup` 返回 execrows 易误用为 id。裁决：P0 待办补 StartDAG/4 actor 明示、wakeup id 返回、terminal event 去重和 hook bounded queue。

## 28. `a3-perf-scale` 全量复审摘要

a3 确认 N=10000 下首要瓶颈是 CreateDAG 单事务串行 upsert + 全量回读、整 DAG `FOR UPDATE`、LLM light/token bucket 未实现、hook 同步和 JSONB/UI payload 膨胀。裁决：P9 补 no-transaction `CREATE INDEX CONCURRENTLY`、分页/`include_result=false`、wakeup payload 限制。

## 29. `a4-security` 全量复审摘要

a4 复核 Critical 为 RPC/WS/MCP 无 AuthN、无 tenant/owner filter、`task_update_node` 裸写、DAG upsert 覆盖。High 为 audit/redaction/sanitize 未实现、RPC `params_preview` 泄露、rate/quota 缺失、cron replay、模板参数注入。裁决：P6 前安全 gate hard，不因 UI/成本让步。

## 30. `a5-data-integrity` 全量复审摘要

a5 确认裸 status、terminal turn fence、DB status CHECK、Create/Upsert 拓扑覆盖、FK/GC/archive 是一致性 Top 5；并发现 P10/P11 migration 编号冲突和 P10 缺 schema_version。裁决：P11/P12 编号立即同步，P10 补 schema_version，P0 补 DB CHECK 与 last_activity/last_event 语义。

## 31. `a6-operability` 全量复审摘要

a6 指出 runtime metrics/exporter、trace propagation、graceful shutdown/drain 顺序仍完全未定义；旧 `_p99` metric 名、`dag_actor_heartbeat` 类型也会误导实现。裁决：COMPLIANCE 统一 metric 命名，新增 trace/drain 为 P0 前冻结项。

## 32. `a7-user-ux` 全量复审摘要

a7 发现当前 DAG 列表无法进入详情、详情仍是 stub、无 `dag/start`、无模板库/实例化/保存模板，错误 catalog 和 cost preview 均无实现入口。裁决：P10 待办补 `DataPage` select、`dashboard/dagDetail` 接通、Start CTA 依赖 P3/P9/P6。

## 33. `a8-ecosystem-deps` 全量复审摘要

a8 确认 P13 strict JSON 不能依赖 provider native；promhttp/exporter 缺失；Wails v3 alpha、cron timezone fallback、PG version gate 是主要依赖风险。裁决：P13 明确 runtime validate 是唯一 hard guarantee，PG>=9.5 preflight 和 Wails smoke test 列后续 gate。

## 34. `a9-migration-rollout` 全量复审摘要

a9 新发现最高优先级 rollout bug：P23 DDL 草案使用 `task_dag` / `task_dag_node` 单数表，而当前真实表是 `task_dags` / `task_dag_nodes`；另有 P11=0069、P12=0070、P5 输入旧 0064 残留和 `cron_job_runs.target_dag_key` 未先加列。裁决：立即修全部 stub 表名/编号/P5 DDL。

## 35. `a10-economics` 全量复审摘要

a10 确认 cost preview、token bucket/subscription、growth budget、LLM light gateway 均未实现；audit/storage 分层与 DB/query 热点会把成本风险放大。裁决：P12/P11/P13 不得早于 P9 cost/token bucket gate；P10 Start 前 cost preview 是 hard UX gate。

## 裁决 12 - 全量复审仲裁（2026-04-25）

1. **立即修文档误导项**：P23 DDL 表名统一到真实 `task_dags` / `task_dag_nodes`；P11=0070、P12=0071、P5 输入=0066；P5 必先给 `cron_job_runs` 加 `target_dag_key`。
2. **实现缺口不冒充已完成**：StartDAG、4 actor、trigger enum、naked status write、turn fence、AuthN/tenant、metrics/exporter、LLM light、cost preview 全部仍是“文档承诺/实现缺失”，在 P0/P6/P9/P10 gate 中 hard 标记。
3. **安全/一致性优先于 UX 与成本**：`task_update_node` 裸写、upsert 覆盖、tenant filter、audit/redaction/sanitize 是 P0/P6 blocker；UI 只能在这些入口 fail-closed 后开放。
4. **性能优先修热路径**：CreateDAG batch/async、ready `SKIP LOCKED LIMIT K`、hook bounded queue、result spillover、detail pagination 是 P9 hard scope；P8/P12 不得绕过 P9 token bucket。
5. **可运维性 gate 前移**：trace_id、drain 顺序、DAG metrics manifest、promhttp/exporter fallback 必须在 P0/P7 截止点前冻结，不能只留 runbook。

## 跨切片冲突仲裁矩阵 2

| 冲突 | 仲裁 | 同步影响 |
|---|---|---|
| a9 rollout 表名/编号 vs 既有 stub | a9 优先 | 立即替换单数表名、P11/P12/P5 编号；RESEARCH 旧结论后续视为 superseded。 |
| a4 安全关闭裸写 vs a7 Start/编辑 UX | a4 优先 | P10 Start/Edit CTA 必须依赖 P3/P6 identity + P0 CAS，不允许直接调旧 `task_update_node`。 |
| a3 性能分页 vs a7 详情拓扑 | a3 优先 | P10 详情默认 summary + cursor；拓扑懒加载节点 detail，禁止一次拉全量 result。 |
| a6 metrics hard gate vs a8 promhttp 缺实现 | a6 优先 | P0 明确 exporter PR/fallback；P7 前无 exporter 则后段 runtime-alert 依赖项阻塞。 |
| a10 cost preview vs a4 quota 策略保密 | a4 优先 | UI 展示预算/是否足够/风险档位，不展示内部限流算法或敏感配额策略。 |



## §36 交叉验证报告摘要 - p23-doc-a1-arch

架构侧判定仍有 3 个 critical：Runner inventory 漏 P11/P13，P5 cron bridge 只禁 cron→cmd 未禁反向 concrete 依赖，单数表名残留。仲裁：全部立即修；archtest 总数裁定为 21，P8 `verdict_lost` 是唯一 status 白名单扩展。

## §37 交叉验证报告摘要 - p23-doc-a2-failure

故障模式侧认为 P1 launch crash-window、P2 active_turn_id terminal fence、durable terminal queue、wakeup TTL/reclaim、shutdown drain 是最高风险。仲裁：P1/P2/P0/COMPLIANCE 立即补硬契约；不能推给 P9 owner。

## §38 交叉验证报告摘要 - p23-doc-a3-perf

性能侧指出 N=10000 下全量 ready/lock、LLM fanout、普通建索引、UI 一次性详情、hook queue drop 都会返工或击穿。仲裁：`remaining_deps/SKIP LOCKED LIMIT K` 前移 P0；P9 提供 token bucket+budget ledger+batch 聚合。

## §39 交叉验证报告摘要 - p23-doc-a4-security

安全侧要求 P6 AuthN/AuthZ/tenant/rate/quota fail-closed，禁外部裸 status 写；audit append-only/hash-chain、PII redaction、sanitize 前置。仲裁：P6 audit fail 不再 blanket 非阻断；严格/金融写操作 fail-closed 或 durable spool。

## §40 交叉验证报告摘要 - p23-doc-a5-data

数据一致性侧再次确认单数表名、terminal fence、P13 validate-before-terminal 与 P2 直接 CompleteNode 竞态、terminal event 去重 DDL 缺失。仲裁：P0/P2/P13 立即补 durable inbox、phase 列、turn fence。

## §41 交叉验证报告摘要 - p23-doc-a6-ops

运维侧认为 promhttp/exporter fallback、graceful shutdown、trace_id、metrics manifest、L4 CI 是闭环缺口。仲裁：COMPLIANCE 新增 metrics manifest 和 drain protocol；CI 仍保持高风险硬前置。

## §42 交叉验证报告摘要 - p23-doc-a7-ux

该 agent 初次运行失败且报告为空；二次拉取返回 `agent not found`，当前 orchestration agent 列表为空，标记二次未到。本轮不采纳其新增 finding；P10 既有 UX/cost/error catalog 待办保留，后续若重跑需独立补审。

## §43 交叉验证报告摘要 - p23-doc-a8-deps

该 agent 初次运行失败且报告为空；二次拉取返回 `agent not found`，当前 orchestration agent 列表为空，标记二次未到。本轮依赖/生态风险只采用其它 agent 对 provider schema、promhttp、CONCURRENTLY、LLM light 边界的交叉发现。

## §44 交叉验证报告摘要 - p23-doc-a9-rollout

Rollout 侧确认单数表名、sqlc schema checklist、P9 CONCURRENTLY 编号冲突、0064 规则、CI hard/soft 口径、rollback 模板缺失。仲裁：表名立即修；P9 `remaining_deps` 前移 P0，0068 留给 no-transaction index/archive。

## §45 交叉验证报告摘要 - p23-doc-a10-cost

成本侧指出 P11 `{}` budget 可变无限、P11×P12 叠乘、P10 preview 只有展示无 hard gate、P8/P12 成本字段不足。仲裁：P11 空 budget 对 spawn invalid；P9/P10/P8/P12 补 budget ledger 与 token/currency/price attribution。

## 裁决 13 - 文档审查仲裁

1. ❌ 单数表名残留全部立即修：DDL/摘要统一 `task_dags/task_dag_nodes`；archive 表命名用 `task_dags_archive/task_dag_nodes_archive`。
2. ❌ RunnerModule inventory 以“所有长跑/周期 worker”口径冻结：P0 四 actor + P7/P8/P11/P12/P13，archtest 总数裁定为 21。
3. ❌ P2 terminal fence 不得等 P9：`CompleteNode/MarkFailed/Retry` 从 P2 起必须带 `active_turn_id`/attempt fence；stale terminal 返回 0 rows。
4. ❌ hook terminal 不许纯内存队列：terminal/reconcile/validation 进 durable inbox/outbox，唯一键 `(dag_key,node_key,turn_id,event_type)`；progress/delta 才可 coalesce/drop。
5. ❌ P1 launch 采用三阶段可恢复协议：persist intent + deterministic idempotency key → idempotent launcher → bind agent/turn CAS；禁止把外部 launcher 包进 DB 长事务。
6. ❌ P5 cron bridge 按双向边界裁定：既禁 `internal/module/cron → cmd/mcp-orch`，也禁 `cmd/mcp-orch → internal/module/cron` concrete；只能经登记 interface/platform sink。
7. 🆕 P9 编号冲突按 HEAD 重排裁定：HEAD 已占用 `0063/0064`；`remaining_deps` 前移 P0 `0065_dag_state_machine.sql`，P9 `0069` 只承载 no-transaction concurrent index/archive/scale policy，P10–P13 顺延为 `0070–0073`；每个 migration PR 前必须重新校准 `migrations/`。
8. 🆕 P8 status 规则：P0 五值是执行基线；P8 `verdict_lost` 是唯一白名单 terminal 扩展；其它后段状态必须独立 phase 列。
9. 🆕 P11 growth budget：空 `{}` 对 spawn-enabled DAG 无效；必须有 conservative cap、budget ledger/reservation CAS、cost/storage/runtime 多维预算。
10. 🆕 P6 audit failure：严格/金融/外部写操作 fail-closed 或 durable spool；只读/dev 可降级但必须告警和重放。

## 跨切片冲突仲裁矩阵

| 冲突 | 仲裁 | 影响同步 |
|---|---|---|
| a5 要 P2 `active_turn_id` fence vs a3 担心 SQL 热点 | 数据正确性优先；P2 必上 fence，P9 补 `(dag_key,node_key,active_turn_id)` 索引 | P2 hard scope + P9 hotspot index |
| a3 要 P9 拆 `CONCURRENTLY` migration vs a9 指出编号冻结冲突 | 保持 P10–P13 编号不动；把 `remaining_deps` 前移 P0，P9 0068 留给 no-transaction index/archive | README/P0/P9 |
| a4 要 audit fail-closed/hash-chain vs a10 担心存储成本 | 高风险/金融写操作 fail-closed；成本通过 P9 archive/TTL/redaction/spillover 控制，不牺牲审计证据 | P6/COMPLIANCE/P9/P13 |
| a7/P10 cost preview vs a4 quota 策略泄露 | 只展示足够/不足/风险档位、预算估计与审批状态，不暴露 bucket 余量/内部窗口/provider 原始限流 | P10 待办保留 |
| a1 P5 bridge 复用 cron module vs P22 modularity | P22 优先；禁止双向 concrete import，复用只能经 interface/platform sink | P5 + archtest |


## §46 交叉验证收集状态表（二次确认落盘）

> 2026-04-25 二次拉取结果：`p23-doc-a7-ux` / `p23-doc-a8-deps` 均返回 `agent not found`，`orchestration_list_agents` 为空；因此二者继续标记“二次未到”，不参与本轮仲裁新增项。

| Agent | 角度 | ✅ | ⚠️ | ❌ | 🆕 | 收集状态 |
|---|---|---:|---:|---:|---:|---|
| `p23-doc-a1-arch` | 架构契约符合性 | 0 | 5 | 3 | 3 | 已收 |
| `p23-doc-a2-failure` | 风险与故障模式 | 0 | 6 | 5 | 4 | 已收 |
| `p23-doc-a3-perf` | 性能与扩展性 | 0 | 7 | 2 | 5 | 已收 |
| `p23-doc-a4-security` | 安全与攻击面 | 0 | 7 | 4 | 4 | 已收 |
| `p23-doc-a5-data` | 数据一致性与状态机 | 0 | 7 | 4 | 4 | 已收 |
| `p23-doc-a6-ops` | 可运维 / 可观测 | 0 | 7 | 2 | 3 | 已收 |
| `p23-doc-a7-ux` | 用户体验 | 未到 | 未到 | 未到 | 未到 | failed 空报告；二次 `agent not found` |
| `p23-doc-a8-deps` | 生态依赖 | 未到 | 未到 | 未到 | 未到 | failed 空报告；二次 `agent not found` |
| `p23-doc-a9-rollout` | 迁移 / rollout | 0 | 9 | 2 | 4 | 已收 |
| `p23-doc-a10-cost` | 成本 / ROI | 0 | 10 | 4 | 5 | 已收 |

仲裁口径：未到 agent 不产生新必修项；已由其它 agent 交叉覆盖的 UX/依赖相关问题（P10 cost preview、provider schema、CONCURRENTLY、LLM light 边界、promhttp/exporter）按已收报告裁决执行。


## §47 ⚠️ 部分修正项仲裁去向表

> 本节补齐 10 路交叉验证中所有 ⚠️ 类问题的落盘去向。原则：能本轮用精确替换修的已修；仍需 owner 设计/实现的写入对应 stub 待办或 COMPLIANCE gate；未到 agent 不产生新增 ⚠️。

| 来源 | ⚠️ 问题 | 仲裁 | 落盘位置 |
|---|---|---|---|
| a1 | archtest 20/21 口径不一致 | 本轮修正为 21 项；`cron_*` 也计入总数 | `README.md` §守卫与 archtest；`COMPLIANCE_GATES.md` §21 项 archtest |
| a1 | P8 status 枚举扩展语义不清 | 明确 `verdict_lost` 是 P8 唯一白名单 terminal 扩展，其它后段状态走独立列 | `README.md` 三子任务冲突契约；`P8_VerificationGate.md` |
| a1 | README 非目标与 P10 UI 冲突 | 改为 P0–P6 不做 UI，P10 后段承接 | `README.md` §非目标 |
| a1 | P13 validate 时机易被读成 hook 同步 validate | 改为 hook bounded parse + durable enqueue，worker terminal 前 validate | `README.md` §P13；`P13_StrictJSONOutput.md` |
| a1/a9 | P5 partial unique index 注释指向错表 | 改为 `cron_job_runs` partial unique index；热表用 CONCURRENTLY | `P5_CronTriggerSurface.md` |
| a1/a2/a3 | P9 错字与 batch 聚合点不清 | 修为 batch 聚合点，并纳入 token bucket/budget ledger | `P9_ScaleScheduling.md` |
| a2 | retry attempt 幂等与 attempt ownership 不完整 | 写入 P2 active_turn/attempt fence；P1 idempotency 协议覆盖 launch 侧 | `P1_WakeupDispatcherAndLaunchBinding.md`；`P2_NodeTerminalReconcile.md` |
| a2 | `observe_lost` deadline/证据标准待细化 | 不在本轮硬写具体阈值；保留为 P0/P2 owner 实现项，需在状态机 PR 冻结 | `P0_DAGRuntimeSkeleton.md` 风险/待办；`COMPLIANCE_GATES.md` stop 条件 |
| a2/a5 | StartDAG idempotency key scope 未定义 | 作为 P3 owner 待补；本轮未硬塞表结构，避免越过 P22/P3 设计 | `P3_ExplicitDAGStartAndOwnership.md` 风险/必测仍保留 caller/idempotency 要求 |
| a3 | P10 UI/API pagination、字段投影、拓扑虚拟化 | 写入 P9/P10 待办；P10 owner 必须默认 `include_result=false`、分页/虚拟化 | `P9_ScaleScheduling.md`；`P10_TemplateAndUI.md` |
| a3/a9 | 活表索引普通 `CREATE INDEX` | 本轮把关键活表索引改为 `CREATE INDEX CONCURRENTLY` / no-transaction policy | `P9/P10/P12/P13` DDL 草案 |
| a3 | metrics/exporter 可选口径与后段依赖冲突 | 新增 metrics manifest / exporter fallback；P7 前无 `/metrics` 或 fallback 则冻结 | `COMPLIANCE_GATES.md` |
| a4 | PII redaction / sanitize 仍是待办 | 作为 P8/P12/P13 前置保留；不在本轮伪造实现细节 | `P8_VerificationGate.md`；`P12_SwarmArbiter.md`；`P13_StrictJSONOutput.md` |
| a4 | quota/rate/token bucket 先后顺序风险 | P9/P10/P11/P12/P13 统一补 cost preview + budget ledger hard gate | `P9_ScaleScheduling.md`；`P10_TemplateAndUI.md`；`P11/P12` |
| a4/a6/a10 | audit 写失败语义冲突 | 分级裁决：strict/financial/write fail-closed 或 durable spool；dev/read 可降级 | `P6_ExternalRPCTriggerSurface.md`；`COMPLIANCE_GATES.md` |
| a5 | FK/GC/archive 闭环不完整 | 本轮只冻结 archive 统一命名和级联校验要求；具体 FK/TTL SQL 留 P9 owner | `P9_ScaleScheduling.md`；`P0_DAGRuntimeSkeleton.md` 待办 |
| a5 | P11 budget 缺 ledger/reservation CAS | 本轮补为必修；并发 spawn 不得超额 | `P11_DynamicNodeGrowth.md` |
| a5 | P13 `output_validation_phase` 声称有但 DDL 缺列 | 本轮补 DDL 草案列 | `P13_StrictJSONOutput.md` |
| a5 | schema_hash/schema_version 不能证明 validation schema | 本轮补 node schema hash/version 列；schema revision FK 细节留 owner | `P13_StrictJSONOutput.md` |
| a6 | trace_id 只在 README 风险中出现 | 保持为跨 actor correlation contract；具体 schema 字段不硬塞所有表，留 owner 在 P0/P6/P8/P13 DDL 中落实 | `README.md` 风险；`COMPLIANCE_GATES.md` metrics/drain gate |
| a6 | runbook 只有首动作 | 本轮未展开完整 runbook，新增 metrics manifest/fallback；详细 PromQL/DB 查询留 SRE owner | `COMPLIANCE_GATES.md` |
| a6 | P7 relaunch kill switch / 防抖 | 留 P7 owner；本轮不改 runtime 语义 | `P7_LivenessProbe.md` 既有风险/必测承接 |
| a9 | sqlc schema checklist 未逐 stub 展开 | 本轮不重复写入每个 P 文件；以 COMPLIANCE daily checklist 统一要求 `make sqlc-verify` | `COMPLIANCE_GATES.md` Daily checklist |
| a9 | migration 编号规则歧义 | 本轮修为禁止复用 HEAD 已占编号；P23 PR 必须按当时 HEAD 下一个可用编号重排并贴校准结果 | `README.md`；`COMPLIANCE_GATES.md` |
| a9 | rollback / roll-forward 模板缺失 | 作为每 migration owner 待补；本轮不为所有暂定 migration 编号伪造 preflight SQL | 本节记录 + `COMPLIANCE_GATES.md` owner checklist |
| a10 | P10 cost preview 仅展示不硬拦 | 本轮改为 approval/hard block，二次确认不能替代授权 | `P10_TemplateAndUI.md` |
| a10 | P13 repair/audit/swarm 成本乘数 | 本轮写入 cost preview 必含 repair rounds、swarm members、base/max/p95 | `P10_TemplateAndUI.md`；`P13_StrictJSONOutput.md` |
| a10 | storage/spillover 生命周期成本 | P9 archive/TTL 覆盖 audit/arbiter/validation/result blob；具体冷存储策略留 P9 owner | `P9_ScaleScheduling.md`；`P13_StrictJSONOutput.md` |

未落具体实现的 ⚠️ 不代表忽略：凡需要 owner 选型、容量数字、preflight SQL、PromQL、CI/branch protection 或 provider 实测的项目，均作为 stub 待办/gate 输入保留，不能在文档仲裁阶段伪造确定实现。


## §48 需求补全调研摘要 - p23-need-a1-architecture

整体需求已映射到 P7–P13，但报告指出 7 个 critical：实现仍“只存不跑”、P22 allowlist 过宽、P2 durable terminal、P1 crash-window、P6 auth、P9 budget、CI/metrics 缺位。新增要求：P7 relaunch 防抖、P10 draft 派生态、大规模 UI 虚拟化、旧 P8 A/B 裁决 superseded。已裁决修 README/RESEARCH/P0/P7/P10。

## §49 需求补全调研摘要 - p23-need-a2-ui-template

P10 已覆盖 UI/模板主线，但缺页面级状态字典、字段注册表、Start/Edit 禁用原因、cost preview 保密边界、模板 preview/diff/lineage、大规模拓扑懒加载。裁决：P10 新增 UI 状态字典、表单字段注册表、cost preview 保密边界和 N=10000 UI 策略。

## §50 需求补全调研摘要 - p23-need-a3-growth-iteration

P11 已覆盖 spawn/growth budget/convergence，但 ledger/reservation CAS 未 DDL 化，`max_depth` 缺 node-level depth，80% backpressure 公式错误，convergence DSL 不可验收，用户调预算缺 RPC/UI/审计。裁决：P11 补 growth_reservations、node depth、budget 分层、convergence DSL v1、`dag/update_budget` 口径。

## §51 需求补全调研摘要 - p23-need-a4-swarm-verdict

P8/P12 已覆盖 arbiter/swarm，但 RESEARCH 旧“方案 A + 自动降级 B”与方案 C 冲突；P12 timeout/quorum 未 schema 化；`escalate_judge` 未强制显式 judge；`dag_swarm_consensus` 未纳入 append-only；dissent summary 注入边界不足。裁决：supersede 旧裁决，P12 补 quorum/成本/hash-chain字段和 judge opt-in 硬规则。

## §52 需求补全调研摘要 - p23-need-a5-rpc-external

P6 归属正确但仍是 stub，不得写“已实现”。缺统一 endpoint matrix、三入口 identity + service enforcement、外部 RPC idempotency、JSON-RPC rate-limit 错误语义、audit fail-closed/spool DDL、method-level AuthZ。裁决：P6 补 endpoint matrix、两层安全模型、幂等与错误语义。

## §53 需求补全调研摘要 - p23-need-a6-json-finance

P13 覆盖 output_schema/runtime validate/金融预设，但 README provider hard guarantee 口气过强，P13 phase 文案与 README 冲突，audit 表缺 hash-chain/schema_version/canonical hash，PII/raw blob/repair_prompt 顺序未硬化。裁决：README 改 provider structured output 为优化；P13 补金融审计硬化契约。

## §54 需求补全调研摘要 - p23-need-a7-auto-progress-liveness

P7 方向覆盖，但仍缺 activity 状态机、kill switch、防抖/cooldown、observe_lost owner/deadline、progress/tool activity ingestion、idle 默认值和 P2 retry budget 关系。裁决：P7 补活性判定硬契约，`observe_lost` 只由 `dagReconcileActor` 写。

## §55 需求补全调研摘要 - p23-need-a8-verification-flow

P2/P8/P13/P12 已覆盖校验流，但 verifier agent/turn binding 缺字段、batch_peer 只有 schema 没算法、P8/P13 repair 复合循环无全局上限、verify/arbiter/swarm durable job queue 未落表。裁决：P8 补 verifier binding、durable job queue、batch_peer 算法和 combined repair budget。

## §56 需求补全调研摘要 - p23-need-a9-scale-scheduling

P9 覆盖瓶颈方向，但缺容量默认值、quota fairness、batch create API 语义、spillover/archive/pagination 契约、backpressure 动作矩阵和 P9 对 COMPLIANCE drain 的反链。裁决：P9 补容量与公平调度硬契约、batch create async job、spillover/archive/pagination 三联契约。

## §57 需求补全调研摘要 - p23-need-a10-rollout-compliance

Rollout 侧指出 README 依赖拓扑漏边（P12 依赖 P9、P10 依赖 P7–P9 schema、P13 依赖 P10 preset）、P8 A/C 口径冲突、CONCURRENTLY 分拆易误导、migration checklist 仍未 gate 化、阶段0/P0 边界不清。裁决：README 修拓扑，COMPLIANCE 增 migration owner checklist 与阶段0/P0 checklist。

## 裁决 14 - 用户综合需求补全仲裁

1. ❌ **P8 verdict 最终口径**：旧方案 A / 自动降级 B 全部 superseded；最终为方案 C：默认 runtime arbiter，judge 仅显式 opt-in，失败 `verdict_lost`。
2. ❌ **P6 外部 RPC 不得称已实现**：P6 是归属切片但仍是 stub；开工前必须有 endpoint matrix、三入口 identity、service enforcement、idempotency、audit fail-closed/spool。
3. ❌ **P7 自动重拉必须有 kill switch + debounce**：没有连续 K 轮证据、cooldown、launcher backlog gate、tool-running 保护，不得上线 relaunch。
4. ❌ **P8/P12 校验与蜂群必须 durable + 可审计**：verifier binding、verify jobs、swarm quorum/timeout、consensus hash-chain 都是必修。
5. ❌ **P11 无限迭代必须 ledger 化**：growth reservation、node depth、budget 分层、deterministic convergence DSL 必须先于 spawn 开放。
6. ❌ **P13 金融 JSON 必须 runtime validate + redaction + hash-chain**：provider native structured output 只能是优化，不是 hard guarantee。
7. 🆕 **P10 UI 从故事升级为页面契约**：状态 legend、字段注册表、CTA 禁用原因、cost preview 保密、大规模虚拟化为 P10 必修。
8. 🆕 **P9 大规模能力从方向升级为容量契约**：claim K、batch size、queue、fairness、spillover/archive/pagination、backpressure 动作矩阵要写入 stub。
9. 🆕 **依赖拓扑修正**：P12= P8+P9；P10= P3+P6+P7/P8/P9 schema freeze；P13 strict JSON= P0/P1/P2+P8 sanitize+P10 preset；仅 financial swarm preset 额外依赖 P12（P12 -> P13 financial preset）。
10. ⚠️ **CI/metrics 仍高风险**：本轮只补 gate/checklist；没有实现前风险评级不降。

## §58 需求补全 ⚠️ 去向表

| 来源 | ⚠️ / 🆕 问题 | 仲裁去向 | 落盘位置 |
|---|---|---|---|
| a1 | P10 `draft` 派生态、旧 P8 裁决冲突 | 本轮修正文案 | `P10_TemplateAndUI.md`、`RESEARCH_VERDICT.md` |
| a2 | 状态 legend / 字段帮助 / cost preview 边界 | 升为 P10 页面契约 | `P10_TemplateAndUI.md` |
| a3 | convergence DSL、用户调预算、budget 分层 | 升为 P11 硬契约 | `P11_DynamicNodeGrowth.md` |
| a4 | quorum/timeout、judge opt-in、swarm audit | 升为 P12 DDL/规则必修 | `P12_SwarmArbiter.md`、`COMPLIANCE_GATES.md` |
| a5 | RPC endpoint matrix / 幂等 / rate error | 升为 P6 必修 | `P6_ExternalRPCTriggerSurface.md` |
| a6 | canonical schema hash、strict finance、redaction 顺序 | 升为 P13 必修 | `P13_StrictJSONOutput.md` |
| a7 | kill switch、防抖、observe_lost owner | 升为 P7 必修 | `P7_LivenessProbe.md` |
| a8 | verifier binding、batch_peer 算法、durable jobs | 升为 P8 必修 | `P8_VerificationGate.md` |
| a9 | quota fairness、batch create、spillover/archive | 升为 P9 必修 | `P9_ScaleScheduling.md` |
| a10 | dependency topology、migration checklist、阶段0/P0 | 本轮修拓扑并补 gate | `README.md`、`COMPLIANCE_GATES.md` |

未直接写成代码级实现的项（容量数字最终值、PromQL、provider 实测、CI workflow、具体 SQL preflight）仍为 owner 开工前 gate，不在文档仲裁阶段伪造实现结果。

## §59 第三轮交叉审查摘要 - a1 architecture

指出 P8 方案 C 曾残留旧 A/B 表述、`verdict_lost` status 白名单不清、COMPLIANCE L6 可选与后段 hard 依赖冲突。裁决：统一方案 C 文案；P8 是唯一 status 扩展；L6 改为 P7 前 hard。

## §60 第三轮交叉审查摘要 - a2 UI/template

指出 P10 DDL 混用 `CREATE INDEX CONCURRENTLY`、cost preview 暴露 provider 原始 quota、legend 混淆 phase/status。裁决：拆 no-transaction 索引；UI 只显 capacity verdict；新增 `display_state` 合成规则。

## §61 第三轮交叉审查摘要 - a3 growth/iteration

指出 P11 旧 80% 公式、`fixed_point` 默认化、ledger/depth 未同步 DDL。裁决：预算按 total/ledger charged count；v1 convergence 只保确定性条件；补 reservation table 与 node `growth_depth`。

## §62 第三轮交叉审查摘要 - a4 swarm/verdict

确认 P12 quorum/audit 已有方向，但索引 migration 仍混事务；P8/P12 不得隐式 judge fallback。裁决：P12 索引拆 no-transaction；judge 仅 schema opt-in。

## §63 第三轮交叉审查摘要 - a5 RPC/external

指出 P6 endpoint matrix 漏 `task/dag/create`，archtest 只盯 middleware，audit fail-closed/spool DDL 不足。裁决：补 compat method matrix、三入口 identity + service guard archtest、audit spool。

## §64 第三轮交叉审查摘要 - a6 JSON/finance

指出 P13 DDL 未同步 hash-chain/schema draft，redaction-before-validation 语义错误，sanitize 路径冲突。裁决：补审计字段；拆 validation/audit 两路径；统一 runtime sanitize 路径。

## §65 第三轮交叉审查摘要 - a7 liveness

指出 README 仍像 watcher 写 `observe_lost`，P7 字段需求与“无 migration”冲突。裁决：watcher 只产 candidate，`dagReconcileActor` 写终态；P7 若字段不能承载必须独立 migration。

## §66 第三轮交叉审查摘要 - a8 verification flow

指出 P8 verifier binding/durable job queue 未同步 DDL，P13 valid path 应进入 P8 verify。裁决：P8 DDL 补 verify fields/job queue 要求；P13 valid 后按 verify.enabled 分流。

## §67 第三轮交叉审查摘要 - a9 scale scheduling

指出 P9 缺 backpressure/shutdown action matrix 与容量默认。裁决：补 hook/launcher/DB/queue/budget/SIGTERM 矩阵和默认 queue/page/archive/drain 参数。

## §68 第三轮交叉审查摘要 - a10 rollout/compliance

指出 P0/P9 SKIP LOCKED ownership 冲突、阶段 0“三件冻结”旧口径、CONCURRENTLY 分拆仍易误导。裁决：P0/P1 owns first claim；P9 只调优；阶段 0 改最小 checklist。

## 裁决 15 - 第三轮交叉审查仲裁

1. ❌ P8 方案口径：最终只称“方案 C：默认 runtime arbiter + judge opt-in + verdict_lost 不降级”。旧 A/B 名称仅可出现在历史修正说明。
2. ❌ status 枚举：P0 五态为基础；P8 是唯一可扩 `verdict_lost` 的 forward-only PR；其它后段状态全部走独立列。
3. ❌ migration：活表 `CREATE INDEX CONCURRENTLY` 必须拆 no-transaction migration，不能与 ALTER/CREATE TABLE 同文件事务混写。
4. ❌ P7：`observe_lost` 只由 `dagReconcileActor` 写；watcher/recovery scan 只产候选。
5. ❌ P11：无限迭代必须 ledger/reservation + node depth；预算 80% 按 total/ledger charged count，不按 pending+running。
6. ❌ P13：金融 JSON validation 先对内存 parsed raw 校验，再将错误/摘要 redacted 后审计/repair；不持久化未脱敏 raw。
7. 🆕 P6/P10/P9 的 UI/RPC/规模默认升为必修；具体实现参数仍由 owner 在 PR 中实测校准。

## §69 第三轮跨切片冲突仲裁矩阵

| 冲突 | 仲裁 | 影响落盘 |
|---|---|---|
| P8 `verdict_lost` vs 主 status 冻结 | P8 优先，但白名单唯一；其余切片不得扩 status | README / P0 / P8 |
| P7 liveness watcher vs P0 watcher 职责 | P0 watcher 只 claim/scan；P7/P2 reconcile 负责证据与终态 | README / P7 |
| P11 growth backpressure vs P9 fairness | P11 用 ledger 硬预算，P9 用 token bucket/queue 控速率 | README / P9 / P11 |
| P13 redaction vs schema validation 正确性 | 校验内存原值，审计/repair 只用 redacted 派生物 | P13 |
| P6 cost/security vs P10 可视化 | 安全优先；UI 展 verdict/风险档，不暴露 provider 原始 quota | P10 |
| P0/P1 ready claim vs P9 scale | P0/P1 先实现 SKIP LOCKED；P9 不重复造 runtime claim | README / P9 |

## §70 第四轮复审摘要（原 10 agent）

第四轮 10 份报告全部到达。共识：P8/P12/P13 审计 hash-chain 必须直接并入 DDL；COMPLIANCE 21 archtest 必须变成单一 authoritative 表；P13 provider structured output 要拆 agent turn path 与 arbiter light path；P8/P12/P13 repair 预算必须共用 node 级 combined chain；P10/P13/P12 依赖与金融 preset 需显式 feature gate。

## 裁决 16 - 第四轮复审仲裁

1. ❌ **archtest 单一权威表**：COMPLIANCE 改为 21 行 authoritative 表，README 只摘要；P6 external guard 保持一个 archtest key、两个 Go test 函数。
2. ❌ **P5 入口名**：全部统一为 `internal/sidecar/orch/orchestration/dag_start.go:StartDAG`，禁止 `dag.Start` 旧名。
3. ❌ **P13 provider path**：agent final answer 的 output schema 适配走 provider/launcher turn 请求路径；`llm/light/*` 只服务 P8/P12 arbiter verdict。
4. ❌ **审计 hash-chain 入 DDL**：`dag_arbiter_calls`、`dag_swarm_consensus`、`dag_output_validations` 的 hash-chain 字段必须写进草案而非只留待办。
5. ❌ **combined repair budget**：P8/P13/P12 repair 共用 node 级 `repair_chain_id + combined_repair_round/combined_repair_max`，swarm dissent repair 也扣同一链。
6. ⚠️ **可执行细节不硬塞**：LLM token bucket 算法、PromQL/fallback 脚本、provider 文件精确落点、100k partition 方案留 owner PR；本文只冻结边界与阻塞条件。

## §71 第四轮跨切片冲突仲裁矩阵

| 冲突 | 仲裁 | 落盘 |
|---|---|---|
| P6 archtest 拆两个函数 vs 21 项总数 | 保持一个 key `dag_external_rpc_guard`，映射两个 test funcs | README / COMPLIANCE |
| P13 agent output vs P8/P12 arbiter light JSON mode | 路径分离；runtime validate 是 hard guarantee | P13 |
| P12 swarm 金融 preset vs P13 依赖 | 金融 swarm preset 依赖 P12；未合入时 feature-gate 隐藏 | P13 / P10 / P12 |
| P11 runtime budget vs structural growth budget | `max_runtime_sec` 移到 convergence/execution budget；growth_budget 只控结构 | P11 |
| CI 缺失 vs P0 前 hard gate | 允许 manual hard fallback，但缺命令输出/reviewer 签收禁止 merge | COMPLIANCE |

## 裁决 17 - 第五轮复审仲裁

1. ❌ **archtest 双权威**：README 不再维护完整 21 行 archtest 表；执行清单唯一来源是 COMPLIANCE 的 21 项 authoritative 表。
2. ❌ **P13/P12 依赖**：非 swarm JSON validation 不依赖 P12；金融 `unanimous swarm` preset hard depends on P12，未合入时 UI/API/template 必须隐藏 swarm 字段。
3. ❌ **P13 parse mode**：解析流程必须按 `output_validation.parse_mode`，金融 strict 禁 json5/jsonpath/markdown wrapper 容错。
4. ❌ **审计 hash-chain 形状**：P12 `dag_swarm_consensus` 补 `chain_scope`，与 P8/P13 hash-chain 字段对齐。
5. ⚠️ **verify job archtest**：不扩 21 项总数；将 `TestDAGVerifyJobsDurable` / `TestDAGVerifyJobClaimRetryDeadLetter` 并入 shared launcher key，将 `TestDAGVerifierTerminalUsesVerifyTurnFence` 并入 terminal fence key。
6. ⚠️ **P6 rate/tenant**：冻结 JSON-RPC rate-limit code `-32029`；create tenant 来源必须来自 authenticated caller authorized tenant。
7. ⚠️ **P9 durable outbox**：terminal/reconcile/validation outbox 必须有 retention、replay batch、watermark、dead-letter、dedup key；100k 明确非 v1 目标。
8. ⚠️ **cost approval**：COMPLIANCE 增中央 cost approval gate，service 层 hard block 优先于 UI 二次确认。
