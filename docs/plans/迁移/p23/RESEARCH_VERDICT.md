# P23 五路 Gap 调研裁决（2026-04-25）

> 创建时间：2026-04-25 | 状态：**裁决已写入 README，arbiter 报告待补**
> authoritative：本文件 + [`README.md`](README.md)
> 输入：5 个 codex 调研 agent 的事实层报告
> 决策权：用户 2026-04-25 当面拍板（DAG 自驱 + 三触发源 + 三能力扩展 + verdict 走方案 A）

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
4. **verdict 实现位置（用户偏好）**：方案 A — DAG runtime 内嵌 LLM 调用做仲裁，不让 verdict 暴露成又一个 agent，降低主 agent / 用户认知负担

---

## 五路调研 agent 与产出

| Agent ID | 调研角度 | 主要结论摘要 |
|---|---|---|
| `gap-liveness` | 要求 1 vs README | P23 完全不覆盖活性探针；建议新增 `dagActivityActor`（第 5 actor）+ `last_activity_at` 字段 + 长工具调用反误杀策略 |
| `gap-verify` | 要求 2 vs README | state machine 不能直接容纳 verify gate；建议 `running → pending_verify → verifying → done | repairing`；引入 `nodes[].verify` schema + sibling group 概念；打回路径复用 retry 但配独立 `max_rounds` |
| `gap-scale` | 要求 3 vs README | DAG 创建 1000 node 单事务、ready 计算 O(N²)、launcher 固定 10 并发、hook 同步 dispatch、wakeup 表无 GC、result jsonb 膨胀——共 7 大瓶颈 |
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
- hook 拦截能力已就位：`cmd/mcp-orch/orchestration/hook_consumer.go:148-151,260-275`，但 README 现要求直接映射 `CompleteNode`（`README.md:71-72`）
- `TurnCompleted` 已有 `Success/Result/Summary/Error`：`internal/dto/turn/event.go:10-21`
- 现有 schema 只有 `DependsOn []string`：`cmd/mcp-orch/orchestration/dag.go:41-49`，**无 sibling group 概念**
- 共享 launcher 已支持 launch 后自动投 prompt：`service_launcher_bridge.go:89-119`
- 同 agent 再投 turn 接口：`service.go:323-325`、`service_launcher_bridge.go:277-290`

**核心建议**：
- verify 子状态走独立列（不与 `status` 共枚举），避免状态爆炸
- `rejected` 当 verdict / result 字段，不当长期 status
- 路径 A（runtime actor 拉 verifier）优于路径 B（hook callback 内派生 turn），符合 P22 archtest "不在 callback 内长跑"

### 3. `gap-scale`

**结论**：P23 离千 node 百 agent 还有 7 大规模化缺口。

**瓶颈热点表**：

| 瓶颈点 | N=1000 影响 | file:line | 建议方向 |
|---|---|---|---|
| DAG 创建单事务 | 1000 次 UpsertNode 串行；事务长锁强 | `dag.go:109-126,202-208,211-220`、SQL `task_dag_node_write.sql:1-12` | 批量 insert / 拆批 / async / streaming |
| ready 计算 | JSONB depends_on 扫描，最坏 O(N²) | `0004_ack_dag.sql:58-62,70-71` | partial index `(dag_key, id) WHERE status='pending'` + 依赖计数列 |
| wakeup 表 | 5000 行/DAG，无 GC | `0023_dag_watcher_phase1.sql:9-36`、`task_dag_wakeup_query.sql:1-16` | TTL / 按 DAG archive / 分区 |
| launcher 并发 | 固定 10，百 agent 至少 10 波；活性 + verifier relaunch 雪崩 | `service_launcher_bridge.go:22-30,54-63` | 配置化 + 全局 token bucket |
| hook 风暴 | 同步 dispatch，百 agent progress 阻塞 core | `hook_consumer.go:105-116,260-275,285-294` | non-blocking enqueue + worker pool + bounded queue |
| 状态存储 | result jsonb 承载 verifier/tool log，行膨胀 | `0004_ack_dag.sql:62`、`task_dag_node_read.sql:1-18` | result 只存摘要，日志 spillover |
| 全量锁读 | `GetNodesForUpdate` 锁整 DAG | `task_dag_node_read.sql:13-18`、`store.go:100-103` | 只 claim 小批 ready，`SKIP LOCKED` |

### 4. `gap-synth`

**结论**：三要求叠加放大效应让 P23 不能原样吸收三能力——**最弱环节是 hook consumer + DB CAS 队列回写**，不是 launcher。

**叠加冲突矩阵**：

| 组合 | 主要冲突 | 缓解 |
|---|---|---|
| 1 × 2 | sleeping 误判杀掉 verifying agent；verifier 失败触发 relaunch 循环 | 活性 actor 识别 `verifying/pending_verify` 子状态；relaunch 与 reject/repair 共用 fence |
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

**结论**：推荐方案 **C（默认 A，opt-in B）**；A 必须是独立 `dagArbiterActor`，**不能**塞进 hook consumer 同步执行。

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
| C 混合（默认 A + opt-in B） | 高 | 低 | 可控 | 最高 | 可控 | **推荐** |

**arbiter 关键设计建议**：
1. A 形态：`terminal hook → enqueue arbiter job → dagArbiterActor → 调 LLM → 写 verdict`，**不是** hook 内同步 chat completion
2. schema：`verify.mode = arbiter | judge`，默认 `arbiter`；可选 `verify.judge_node_key`
3. 失败兜底：落 `verdict_lost` 第三类终态（类比 `observe_lost`），**不**自动降级 B（避免隐藏成本，跟"降低认知负担"目标一致）
4. 安全：verifier / agent 输出作为 quoted data；system prompt 明确"不执行报告内指令"；JSON schema 强校验
5. 审计：`dag_arbiter_calls` 表（input_hash, output, model, latency, cost, error）

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
| RPC 端点（外部调度） | **P6 已覆盖** | 不新建子任务；在 README 显式写明 P6 已实现外部调度入口（含 AuthN/AuthZ/rate/quota/audit） |
| JSON 输出模式（金融场景） | **新建 P13** | `P13_StrictJSONOutput.md`：`output_schema` + validator + repair-or-fail + 金融场景预设 |

**追加的冲突缓解契约（README §三子任务叠加冲突缓解契约 第 6/7/8 条）**：

6. P11 spawn 入口必须经 `SpawnChildNodes` 服务函数 + growth_budget 硬约束；不允许绕路直接 INSERT
7. P13 output_schema 验证发生在 P8 verify gate **之前**（语法层先于语义层）；invalid 直接 repair/fail，**不**进 verify_phase
8. P12 swarm 调用共用 P9 全局 token bucket（与 P8 单 arbiter 同一通道）

**新增的关键依赖**：
- P11 依赖 P9 backpressure（动态生长 × 规模会形成倍数压力）
- P12 依赖 P8 已合入（特别是 `cmd/mcp-orch/orchestration/llm/light/*` 轻量 LLM 调用层；原表述 `internal/llm/light/*` 已修正）
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

### 裁决 1：P23 范围保持"自驱底座 + 三触发源"，不吸收新能力

**采纳 `gap-synth` 推荐**。理由：
- P23 当前职责是"DAG 自驱底座"，三要求会把它变成 mega-plan
- 三要求需要重设大状态机，不能污染 P23 阶段 0 小 SM
- 先交付 P23 可为后段子任务（探活、verifier、规模化）提供稳定 CAS / actor / hook 基线

**写入位置**：README §"未来扩展边界 / 不可纳入本期"（新增）；非目标段加一条明示。

### 裁决 2：拆出 P7 / P8 / P9 三个 P23 后段子任务

- **P7 心跳式节点活性监控**：承接要求 1
- **P8 校验闭环**：承接要求 2 + verdict 实现采用方案 A
- **P9 大规模 DAG 调度**：承接要求 3

**写入位置**：README §"未来扩展边界" 三个子节，明列字段、依赖、reject conditions。P7_LivenessProbe.md / P8_VerificationGate.md / P9_ScaleScheduling.md 三个子任务文件留待后续会话编写，都在 `docs/plans/迁移/p23/` 内。

### 裁决 3：P23 阶段 0 追加"扩展点契约"作为第 5 件冻结

避免 P7 / P8 / P9 上线时返工，本期必须先冻结：

1. `task_dag_node` 预留 `last_activity_at TIMESTAMPTZ` 列（在 `0063_dag_state_machine.sql` 一并加，本期不消费但 P2 hook tap 必须回写）
2. P2 reconcile hook tap 必须 enqueue-only（P8 verifier gate 硬前置）
3. P23 主 `status` 枚举固定，未来子状态走独立列（如 `verify_phase` / `activity_state`），保持 CAS 形状不变
4. `maxConcurrentLaunches` 配置化（提取成 config 参数，P23 不改默认值，P9 升级为 token bucket 时不需要二次迁移）
5. launcher 全局 quota 占用方在 P8 引入 verifier 后必须包含 verifier launch（不允许双队列）

**写入位置**：README §"阶段 0：前置冻结" 新增第 5 项。

### 裁决 4：verdict 仲裁器走方案 A（P8 实现）

**采纳用户决策**。理由（用户已述）：降低主 agent 和用户的认知负担——DAG runtime 内部解决 verdict，不暴露成又一个需要主 agent / 用户配置 / 监控的 agent 实例。

**关键约束（写入 README §未来扩展边界 P8 段）**：
- DAG runtime 在 verifier terminal 后直接发起一次 LLM chat completion（structured output），输入 verifier 报告集合，输出 `{verdict, reasons, repair_prompt?}`
- **失败兜底降级方案 B**：arbiter LLM 调用失败（服务挂、超时、JSON parse 失败）时降级为拉一个 judge node 走常规 launcher 路径
- **prompt injection 防护**：verifier 报告进 arbiter 前必须经 sanitize layer；禁止 verifier 输出直接当 prompt 传入
- **审计**：每次 arbiter 调用落一行 `dag_arbiter_calls` 表（输入 hash / 输出 / model / latency / cost）
- **batch 聚合**：千 node × 每 node 一次 = 千次 LLM 调用 / DAG，必须配 batch；P9 规模下不允许走开环
- **现有基础设施考证**：等 `gap-arbiter` 报告补充

### 裁决 5：三要求叠加冲突缓解契约由 P23 锚定（authoritative）

写入 README §"三要求叠加冲突缓解契约"：

1. 活性 actor 与 verifier gate 共用 CAS fence，禁止双推进（P7 + P8）
2. 活性扫描分片 + lease jitter（P7 + P9）
3. verifier launch 共用 launcher 全局 quota（P8 + P9）
4. arbiter 调用 batch 聚合 + 失败兜底降级 B（P8 + P9）
5. P7 / P8 / P9 不允许修改 P23 主 `status` 枚举；新状态走独立列（保 CAS 形状）

---

## 写入清单（已落盘）

| 文件 | 改动 | 状态 |
|---|---|---|
| `docs/plans/迁移/p23/README.md` | 阶段 0 加第 5 项扩展点契约（裁决 3） | ✅ 已写 |
| `docs/plans/迁移/p23/README.md` | 风险段加"与未来扩展耦合风险"子节（裁决 5） | ✅ 已写 |
| `docs/plans/迁移/p23/README.md` | 非目标加一条明示不实现三能力（裁决 1） | ✅ 已写 |
| `docs/plans/迁移/p23/README.md` | 新增"未来扩展边界 / 不可纳入本期"章节（裁决 1 + 2 + 4 + 5） | ✅ 已写 |
| `docs/plans/迁移/p23/RESEARCH_VERDICT.md` | 本文件 | ✅ 已写 |
| `docs/plans/迁移/p23/P7_LivenessProbe.md` | P7 子任务单（活性探针） | 🔲 待编写 |
| `docs/plans/迁移/p23/P8_VerificationGate.md` | P8 子任务单（校验闭环 + arbiter，方案 C） | 🔄 stub 已更新（采纳 arbiter 设计建议） |
| `docs/plans/迁移/p23/P9_ScaleScheduling.md` | P9 子任务单（大规模调度） | 🔲 待编写 |
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

**结论**：P0–P3 可行，但 **P3 落点必须修正**——stub 写 `internal/orchestration/dag.Start` 与 README §"当前基线约束"（DAG runtime 默认归 `cmd/mcp-orch`）冲突。建议改为 `cmd/mcp-orch/orchestration/dag_start.go` 的 `StartDAG(ctx, dagKey, triggerMeta)`，**不**膨胀 `dag.go`。

**关键设计建议**：
- P0 状态机 SQL：把现有 `task_dag_node_runtime.sql:24-42` 加参数化 `WHERE current_status=$expected` CAS；继续复用 `migrations/0023_dag_watcher_phase1.sql:9-43` 的 wakeup / lease 表，不另建 fence 表
- P1 双触发 CAS：反向 `dag_ref` bind 先按 `(dag_key, node_key, status='running')` 查；如果 `assigned_agent_id != ''` 直接 noop
- P2 队列选 **内存 bounded channel + reconcile recovery scan**：callback 只 enqueue（满足 P22 callback 轻量规则），持久化靠 node runtime 状态恢复，不混用 wakeup 表承载 terminal 队列
- P3 idempotency：`dag/start` 用 `(dag_key, trigger, idempotency_key)` 或 DAG 级 `last_trigger_*` CAS 防重

**契约符合性**：除 P3 落点外，其它项已对齐 `modularity-convention §4.4 §7` / `fx-convention §2 §3` / `rungroup-convention §2 §4`。

### 7. `impl-trigger-P4-P6` 摘要

**结论**：P4–P6 可做，但**必须串行**（P3 → P4/P5/P6）。最大风险是 P5 跨 root：

- **P5 跨 root 违规**：`internal/module/cron/scheduler.go:288-305` 当前直接调 `StartTurn`；扩展时如直接 import `cmd/mcp-orch/orchestration` 会被 `internal/archtest/dependency_direction_mcp_orch_test.go:49-53` 拦截。✅ 修正：cron 模块定义 `TriggerSink` interface，mcp-orch 装配实现 bridge
- **P5 idempotency 缺失**：cron run 当前 idempotency 是 UUID（`internal/module/cron/scheduler.go:318-326`），不是 `hash(cron_job_id, scheduled_at)`。✅ 修正：DAG 路径用 deterministic key + 唯一约束 `(job_id, scheduled_at, target_dag_key)`
- **P5 已落地依据**：P21 P1b 已实际合入（`internal/app/modules.go:58-60`、`internal/module/cron/module.go:31-32`、`internal/module/cron/tick_actor.go:41-60`）
- **P6 AuthN middleware**：必须分两层——transport 提取 identity 到 ctx + service 层统一 AuthN/AuthZ/rate/quota/audit；不能只挂 `cmd/mcp-orch/orchestration/rpc.go`，因为 TCP / Wails / MCP HTTP 三种入口形态不同
- **跨 root 边界**：cron→DAG 不能 core 直接 import orch concrete；DAG runtime 调用面必须经 hook / interface

### 8. `impl-quality-P7-P8-P12-P13` 摘要

**结论**：可行，但 **P8 前置 PR 是关键阻塞**——必须先合 `cmd/mcp-orch/orchestration/llm/light/*`。

- **`internal/llm/light/*` 落点违反 allowlist**：`docs/契约/modularity-convention.md:336-377` 的 cmd/mcp-orch 允许 import 清单不含此包；模块名录也不含。✅ 修正：改 `cmd/mcp-orch/orchestration/llm/light/*`
- **不复用 `dream.go`**：`internal/contract/dream.go:10-12` + `provider/codexapp/dream_executor.go:19-25` + `provider/claudecli/dream_executor.go:19-25` 当前 TODO 不可用；且缺 role/message、schema、timeout、usage、审计字段
- **codex / claude structured output 锚点**：codex 仓库未见 OpenAI JSON mode / function calling 锚点（不能宣称 hard guarantee）；claude 有 `tool_use` 事件解析（`internal/provider/claudecli/factory.go:148-168`）但 CLI 只通过 `--mcp-config` 接工具（`transport_config.go:185-203`），未见"output_tool schema 强制返回"入口
- **fallback 代价**：只能 prompt 要求 JSON + runtime validate；无 hard guarantee
- **archtest 例外**：P13 hook 内同步 schema validate 需 archtest 例外（**已写入 P23 阶段 0 ⑤**）：只允许 parse + validate + enqueue / 轻量 CAS，禁网络 / LLM / 阻塞循环
- **共享 sanitize**：抽 `cmd/mcp-orch/orchestration/llm/light/sanitize.go` 给 P8 arbiter / P12 swarm / P13 repair_prompt 共用
- **合入关系**：P12 不能与 P8 独立并行，必须 P8 + 轻量 LLM 后；P13 可在 P8 sanitize 合入后独立

### 9. `impl-scale-P9-P10-P11` 摘要

**结论**：三线**不能并行**，先 P9 背压契约，再 P11 spawn，最后 P10 消费稳定 schema / 状态流。

- **P9 批量 create**：建议 service 层 async job + 分批事务（不新增 streaming RPC）；partial failure 标记 `incomplete`，cleanup job 处理
- **P9 partial index 迁移成本**：百万级 reindex 必须 `CREATE INDEX CONCURRENTLY`，迁移分两步（先列回填，再建索引）；`remaining_deps` 只能在 CreateDAG / Spawn / Edit 事务内维护，禁异步补偿
- **P9 hook worker pool**：当前 hook callback 同步 dispatch（`hook_consumer.go:105-116,260-275,285-294`）；改成 bounded enqueue + Runner worker，初值 workers=4 / queue=1000 / drop 拒非关键 progress、terminal 不丢只反压；这是 P21 Observation Contract consumer 级重构
- **P10 UI 现状**：Wails runner / WS 已就位（`internal/ui/wails/http_server.go:39-46`），但 `index.html` 是占位（`index.html:48-50`）；真实前端是 `cmd/agent-terminal/frontend/` Vue/Vite（`package.json:5,39-42`）；DAG 详情 modal 是 stub（`DagDetailModal.js:1-5,52-54`）；mermaid 依赖已存在（`package.json:33`，`mermaid-renderer.js:22-26`）
- **P10 编辑 CAS**：新建 `task_dag_node_edit.sql`，与 dispatcher CAS 竞争（谁先提交谁赢）
- **P11 spawn 入口**：放 `cmd/mcp-orch/orchestration/dag_spawn.go`；archtest grep `INSERT INTO task_dag_nodes`，只允许 `UpsertTaskDagNode` / `CreateDAG` / `SpawnChildNodes` 出现
- **P11 收敛 evaluator**：必须是 Runner actor（不是 cron），遵守 `rungroup-convention §2 §4`

**资源峰值估算**：base=1000 + P11 放大到 N=10000 + P12 swarm 3 verdict/节点 = 最坏 30000 verifier/LLM job；缓解依赖 P11 budget cap + 80% backpressure + P8/P12 共用 P9 token bucket。

### 10. `contract-compliance-master` 摘要

**整体合规风险评级：高**。原因：P3 / P5 / P8 / P13 stub 已经出现模块落点 / fx/run.Group / hook callback 特批的潜在漂移；后段 P11 / P12 / P13 又会把 launcher / LLM / hook / DB CAS 压力叠加。

**14 子任务契约扫描结果**（`✅` 已对齐 / `⚠️` 风险点 / `❌` 违规）：

- P0: ✅✅⚠️（actor 必须 `group:"runners"` 不 fire-and-forget）
- P1: ✅✅✅
- P2: ✅⚠️⚠️（hook tap enqueue-only；timeout 扫描必须 actor）
- P3: ⚠️✅✅（落点漂移 → 已修正）
- P4: ✅✅✅
- **P5: ❌⚠️⚠️**（cron import orch 违规 → 已修正）
- P6: ✅✅✅
- P7: ✅✅⚠️
- P8: ⚠️✅⚠️（`internal/llm/light/*` 不在 allowlist → 已修正）
- P9: ✅⚠️⚠️
- P10: ⚠️✅✅
- P11: ✅✅⚠️
- P12: ✅✅⚠️
- P13: ⚠️⚠️⚠️（hook 同步 validate 需例外 → 已写入阶段 0 ⑤）

**migration 编号顺序冲突**：P5 (0064) 早于 P3 (0065) 但依赖 P3。✅ 已修正：P5 改 0066。

**整体 stop conditions**：5 类信号触发停工 / 重构（详见 [`COMPLIANCE_GATES.md`](COMPLIANCE_GATES.md) §"触发 stop / 重构条件"）。

### 11. `compliance-gate-design` 摘要

**七层 gate 体系**（详见 [`COMPLIANCE_GATES.md`](COMPLIANCE_GATES.md)）：L1 schema / IDE → L2 pre-commit → L3 archtest → L4 CI → L5 merge → L6 runtime alert → L7 scheduled audit。

**关键缺口**：
- 仓库**无** `.github/workflows/`：CI gate 当前不存在，必须先建或 fallback 到 PR 模板贴本地输出
- `internal/platform/metrics/metrics.go` 只有 counter 声明，**无** promhttp exporter / alert 链路：L6 实施前必须补
- Scheduled audit 设计完整，但实施依赖 L4/L6 先就位

**实施成本** + **防御能力**评估：L1 + L3 + L4 必须做（合规底座）；L2 + L5 推荐；L6 + L7 可选。

---

## 裁决 9：6 路调研结论合并（2026-04-25）

### 9.1 已修正的关键漂移（已写入 README + stub）

1. **P3 落点**：`internal/orchestration/dag.Start` → `cmd/mcp-orch/orchestration/dag_start.go` 的 `StartDAG(ctx, dagKey, triggerMeta)`
2. **P5 跨 root**：`internal/module/cron` 不直接 import `cmd/mcp-orch`；改为 cron 定义 `TriggerSink` interface，mcp-orch 装配实现 bridge
3. **P5 idempotency**：UUID 改为 deterministic `hash(cron_job_id, scheduled_at, target_dag_key)` + `cron_job_runs` 唯一约束 `(job_id, scheduled_at, target_dag_key) WHERE target_dag_key <> ''`
4. **migration 编号**：P5 从 0064 改 0066（晚于 P3 的 0065）；0064 保留 unused 作缓冲
5. **`internal/llm/light/*` 落点**：改 `cmd/mcp-orch/orchestration/llm/light/*`（只服务 DAG arbiter，未来其它模块需要再升级到 `internal/platform/llm/light`）
6. **P13 hook 同步 schema validate 例外**：写入 P23 阶段 0 ⑤（archtest `dag_hook_tap_enqueue_only` 白名单：parse + validate + enqueue，禁网络 / LLM / 阻塞循环）
7. **archtest 清单**：从 5 项扩到 14 项，全部列入 README §"守卫与 archtest"
8. **共享 sanitize layer**：抽 `cmd/mcp-orch/orchestration/llm/light/sanitize.go` 给 P8 / P12 / P13 共用

### 9.2 未修正的关键缺口（必须在 P0 启动前补 / 或在 README 风险段标记）

9. **CI workflow 不存在**：仓库无 `.github/workflows/`。L4 gate 需要先建 workflow，或 fallback 到 pre-commit + 本地强制 + PR 模板贴本地输出。已写入 README §"整体合规风险评级"风险段。
10. **runtime metrics / alert 缺口**：仓库只有 counter 声明，无 promhttp exporter / alert 链路。P9 SLO + P11 budget alert + P6 audit fail alert 都依赖此能力——必须先补，或这些 alert 暂降级为 scheduled audit + log only。已写入 README 风险段。
11. **P9 hook worker pool 是 P21 Observation Contract 级重构**：terminal precedence / 归因不能变。owner 启动前必须读 P21 Canonical Turn Observation Contract。已写入 README 风险段。

### 9.3 整体合规风险评级：**高**

理由：14 子任务跨切片复杂度 + 现有合规基础设施缺口（无 CI / 无 alert / archtest 14 项需新建）。

**三条最关键守门动作**：
1. 先落 P23 archtest 骨架 + migration sequence guard（L3）—— 不让 P0 之后再补规则
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

金融场景月度成本估算（1000 DAG × 1000 node × swarm 3 member × 3k input + 2k output）：GPT-5.4 ≈ \$112,500/月；Claude Sonnet 4.6 ≈ \$117,000/月。存储粗估 13MB/DAG，活表 13GB/月，DB 膨胀 3×≈40GB。**最严重 3 项成本爆炸**：(1) P12 swarm × P11 growth 叠乘（1000 → 10000 node × 3 = 30000 LLM job 峰值）；(2) 无全局 token bucket / subscription 映射，verifier+swarm+launcher 分队列会雪崩；(3) 热表膨胀（`result jsonb` + arbiter / validation 多表写盘叠加） + UI 全量渲染。P0–P13 工程日 ROI 排名：P0/P1/P2/P3/P6/P9 是必做，P8/P13/P7/P11 推荐，P12/P5/P4 可选，P10 推荐但贵（3–5d）。

---

## 裁决 10：10 路全量调研合并裁决（2026-04-25）

### 10.1 10 个 agent 核心结论（一句话）

| agent | 一句话结论 |
|---|---|
| a1-arch-conformance | P22 allowlist + modularity 名录与 P23 stub 还有 3 项 high 漂移（`internal/llm/light` 残留 / archtest allowlist 宽 / `dag.Start` 原表述）。 |
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
| `internal/llm/light` 残留引用 | a1 后被 a8/a9 重点名 | **P0 / S0**（本 PR 已全面修正） |
| promhttp + alert 链路 | a6 + a9 + a10 | **P0 / S1** |
| token bucket / subscription 映射 | a3 + a8 + a10 + a2 | **P0 / S1** |
| `task_dag_node` 裸 status 写 + active_turn_id fence | a5 + a2 | **P0 / S1** |
| CI workflow / archtest 逐层转 hard | a1 + a9 | **P0 / S2** |
| UI 占位 → 实拓扑 / 实表单 / Start CTA | a7主 + a8/a10 | **P1（不阻 P0 代码）** |
| Wails v3 alpha / d3 依赖架构选型 | a8 + a7 | **P2 / 调研** |

### 10.3 立即修正项（必改文件清单，本 PR 已含）

- `README.md`：`internal/orchestration/dag.Start` → `cmd/mcp-orch/orchestration/dag_start.go:StartDAG`（L196）、P5 表述 `trigger=external` → `trigger=cron`（L174）、补 a3/a4/a5 风险段、补 5 项 archtest。
- `P5_CronTriggerSurface.md`：目标句 `trigger=external` → `trigger=cron`；补待办（DST/UTC、双轨窗口、append-only）。
- `P8_VerificationGate.md`：改动清单 `internal/llm/light` → `cmd/mcp-orch/orchestration/llm/light`；依赖/待办补 sanitize / audit / cost。
- `P12_SwarmArbiter.md`：`internal/llm/light` → `cmd/mcp-orch/orchestration/llm/light`；swarm actor 明示 `group:"runners"` + Runner.Run + interrupt/drain。
- `P13_StrictJSONOutput.md`：`internal/llm/light/codex_json_mode.go` / `claude_tool_mode.go` 路径修正；补 PII redaction / append-only / cache cap / archive TTL。
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

