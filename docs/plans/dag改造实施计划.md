# Super-Dolphin DAG 改造实施计划

> 配套文档：`docs/plans/dag改造蓝图v2.md`（决策与设计）
> 本文是执行级清单：每行一个可独立提交的任务，含触动文件、验收、依赖、size、可并行性
> Size 直觉：S = 半天以内 / M = 1-2 天 / L = 3 天以上（仅相对量级，非时间承诺）
> 修订历史：2026-05-10 初稿；2026-05-10 补 4 处小字段位 / 策略（isolation / outputs.schema / replan / polling）

---

## 0. 总览

| 阶段 | 任务数 | 总 size | 关键产出 |
|---|---|---|---|
| S 骨架 | 22 | ~25% | 14 处补丁 + ADR + 删死代码；行为完全不变 |
| T 工具 | 18 | ~35% | MCP 工具就位 + UI 接通真实数据 + AI 按钮可点 |
| F 功能 | 26 | ~30% | 行为兑现：cron 真跑、AI 设计师上岗、智能重试落地 |
| H 加固 | 按需 | ~10% | 生产问题驱动，不预排 |

里程碑：
- **M1（S 完成）**：删除 `auto_handoff_phase1` 全代码 0 命中；旧 DAG 100% 兼容
- **M2（T 完成）**：UI 上能看到 DAG → 点 Start → 节点状态变化
- **M3（F 完成）**：每日 cron + AI 帮你设计流程 + 智能重试 三大需求端到端通

---

## 1. 阶段 S 骨架（22 任务）

| ID | 标题 | 主要触动文件 | 验收 | 依赖 | Size | 并行 |
|---|---|---|---|---|---|---|
| **S1.1** | 定义 `NodeExecutor` interface + `NodeOutcome` / `RetryHint` 类型 | `cmd/mcp-orch/orchestration/node_executor.go` 新建 | 接口编译过；godoc 注释完整 | — | S | Y |
| **S1.2** | `FailureClass` 7 类常量 + `OnFailureStrategy` 7 项常量（含 `replan`）+ `HookPoint` enum | `cmd/mcp-orch/orchestration/node_executor.go` | 常量定义 + godoc；`grep -c FailureClass` ≥ 7；`grep -c OnFailureReplan` ≥ 1 | S1.1 | S | Y |
| **S1.3** | `AgentExecutor` stub（包现有 wakeup 拉子 agent 逻辑） | `cmd/mcp-orch/orchestration/executor_agent.go` 新建 | 单测 stub 调用返回 `NodeOutcome{Status: Done}` | S1.1 | M | N |
| **S1.4** | `AutomationExecutor` stub | `cmd/mcp-orch/orchestration/executor_automation.go` 新建 | 单测 stub 返回 NodeOutcome | S1.1 | S | Y（与 S1.3 并行） |
| **S1.5** | `HybridExecutor` stub | `cmd/mcp-orch/orchestration/executor_hybrid.go` 新建 | 单测 stub 返回 NodeOutcome | S1.1 | S | Y |
| **S2.1** | service 层 `StartDAG` / `TerminateDAG` 方法签名 + stub | `cmd/mcp-orch/orchestration/service.go` 增 / `dag_lifecycle.go` 新建 | 单测：调用返回 NotImplemented；接口稳定 | — | S | Y |
| **S2.2** | service 层 `ApplyOps(dag_key, base_version, ops[])` 接口 + stub | `cmd/mcp-orch/orchestration/dag_ops.go` 新建 | 单测：base_version 不匹配返回 ConflictError | S2.1 | M | N |
| **S2.3** | `Scheduler` interface (`Tick` / `Schedule`) + stub | `cmd/mcp-orch/orchestration/scheduler.go` 新建 | 接口编译过；stub panic("not implemented") | — | S | Y |
| **S3.1** | migration: `task_dags` 加 `trigger / owner_id / cron_expr / next_run_at / version` | `migrations/0017_dag_v2_dag_columns.sql` 新建 | `make migrate` 通过；旧 row trigger 默认 'manual' | — | M | N |
| **S3.2** | migration: `task_dag_nodes` 加 `run_id / reads / writes` | `migrations/0018_dag_v2_node_columns.sql` 新建 | migrate 通过；reads/writes 默认 `[]` | S3.1 | S | N |
| **S3.3** | migration: 新建 `task_dag_runs` 表（含 events / budget_used / budget_limit） | `migrations/0019_dag_v2_runs.sql` 新建 | migrate 通过；run_key UNIQUE | S3.1 | M | N |
| **S3.4** | migration: 旧 `metadata.auto_handoff_phase1=true` → `trigger='auto'` 一次性映射 | `migrations/0020_dag_v2_compat.sql` 新建 | migrate 通过；DB 上 grep 不到该 key | S3.1 | S | N |
| **S3.5** | store contract 新方法签名（不实现）：`CreateRun / GetRun / ListRuns / ApplyOps` | `cmd/mcp-orch/store/taskdag/contract.go` | 接口编译过；mock 实现 stub | S3.1-3.3 | M | N |
| **S4.1** | typed ops payload Go struct: `OpUpdateDAG / OpAddNode / OpUpdateNode / OpRemoveNode` + `OpKind` enum | `cmd/mcp-orch/orchestration/dag_ops_types.go` 新建 | 单测：JSON marshal/unmarshal 双向 | S2.2 | S | Y |
| **S4.2** | `OpsRequest{DagKey, BaseVersion, Ops[]}` + `OpsResponse{NewVersion}` | 同上 | 单测 | S4.1 | S | Y |
| **S5.1** | typed `node.config` schema Go struct（agent / automation / hybrid 三套），含 `exec.isolation` / `outputs.schema` 字段位 | `cmd/mcp-orch/orchestration/node_config_types.go` 新建 | 单测：三种 node_type 各跑一份 fixture marshal/unmarshal；`isolation` / `outputs.schema` 字段读写正确 | S1.2 | M | Y |
| **S5.2** | `ParseNodeConfig(node_type, raw)` 解码器 | 同上 | 单测：错误 node_type / 缺字段 返回明确错误 | S5.1 | S | N |
| **S6.1** | UI `DagDetailModal` 真实组件结构（不接数据） | `cmd/agent-terminal/frontend/vue-app/components/DagDetailModal.js` 重写 | 组件渲染不黑屏；占位文案显示；e2e snapshot 通过 | — | M | Y（前端独立） |
| **S6.2** | UI 节点 9 态状态色 token + 7 类失败色 token | `cmd/agent-terminal/frontend/vue-app/styles/dag-tokens.css` 新建 | 视觉 review 通过；色弱友好 | S6.1 | S | Y |
| **S7.1** | 9 态 `NodeStatus` 常量 + `ValidateTransition(from, to)` 函数 | `cmd/mcp-orch/orchestration/node_status.go` 新建 | 单测：所有合法/非法转移；非法返回 error | S1.2 | M | Y |
| **S7.2** | `service.UpdateNodeStatus` 用 `ValidateTransition` 校验 | `cmd/mcp-orch/orchestration/dag.go` 修改 | 单测：非法转移被拒；现有测试不破 | S7.1 | S | N |
| **S15.1** | 删除 `auto_handoff_phase1` 全部写入点（`task_tools.go:23,116,231-235`） | `cmd/mcp-orch/tools/task_tools.go` | grep `auto_handoff_phase1` 全代码 0 命中（除 changelog） | S3.4 | S | N |
| **S16.1** | 一份 ADR：NodeExecutor / Ops / Status / FailureClass 契约 | `docs/adr/0001-dag-v2-contracts.md` 新建 | 文档 review 通过；蓝图 v2 第 7-9 节内容固化 | S1-S7 完成 | M | Y |

**S 阶段验收**：
- 全部 22 任务完成
- `go build ./...` / `go test ./...` / `go vet ./...` / `scripts/test_with_guard.sh` 全过
- `cmd/agent-terminal/frontend && npm test` 通过（vitest）
- 旧 DAG 创建/查询/更新调用 100% 兼容
- `grep -r auto_handoff_phase1 cmd/ internal/` 0 命中
- ADR 已 commit

**S 阶段提交粒度建议**：
- S1.1+S1.2 一次（接口 + enum）
- S1.3 / S1.4 / S1.5 各一次
- S2.1 / S2.2 / S2.3 各一次
- S3.1+S3.2+S3.3 一次（一组 migration）/ S3.4 单独 / S3.5 单独
- S4.1+S4.2 一次
- S5.1+S5.2 一次
- S6.1 / S6.2 各一次（**前端必须先发方案给用户确认**）
- S7.1+S7.2 一次
- S15.1 单独（删死代码 commit 单独提）
- S16.1 单独（ADR）

约 14 个 commit。

---

## 2. 阶段 T 工具（18 任务）

| ID | 标题 | 主要触动文件 | 验收 | 依赖 | Size | 并行 |
|---|---|---|---|---|---|---|
| **T1.1** | MCP `task_start_dag` schema + handler | `cmd/mcp-orch/tools/task_tools.go` | 集成测试：调 task_create_dag → task_start_dag → status running | S2.1 | M | Y |
| **T1.2** | `service.StartDAG` 真实实现：创建 run + status 转 running | `cmd/mcp-orch/orchestration/dag_lifecycle.go` | 单测覆盖；run 表插入正确 | T1.1, S3.5 | M | N |
| **T2.1** | MCP `task_dag_apply_ops` schema + handler（draft/ready 状态可改） | `cmd/mcp-orch/tools/task_tools.go` | 集成测试：apply_ops 后 version+1，base_version 不匹配返 conflict | S4.1, S4.2 | M | Y |
| **T2.2** | `service.ApplyOps` 真实实现 ops apply 逻辑 | `cmd/mcp-orch/orchestration/dag_ops.go` | 单测覆盖 4 种 ops + 环检测 + OCC | T2.1, S5.2 | L | N |
| **T3.1** | MCP `task_get_run` schema + handler | `cmd/mcp-orch/tools/task_tools.go` | 集成测试：返回完整 run + 节点状态 | S3.5 | S | Y |
| **T3.2** | MCP `task_list_runs(dag_key, limit)` schema + handler | 同上 | 集成测试：分页正确 | S3.5 | S | Y |
| **T4.1** | MCP `list_models` 工具（hardcoded provider→models） | `cmd/mcp-orch/tools/registry_tools.go` 新建 | 集成测试：claude/codex 各自 model 列表 | — | S | Y |
| **T4.2** | MCP `list_prompt_templates` 工具（已有 prompt_list 复用） | 同上 | 集成测试 | — | S | Y |
| **T4.3** | MCP `list_command_cards` 工具（已有 command_list 复用） | 同上 | 集成测试 | — | S | Y |
| **T4.4** | MCP `list_sharedfiles` 工具 | 同上 | 集成测试 | — | S | Y |
| **T5.1** | UI `useDagDetail` composable（fetch task_get_dag） | `cmd/agent-terminal/frontend/vue-app/composables/useDagDetail.js` 重写 | 单测：response → state 映射正确 | T1.1 | M | Y（前端独立） |
| **T5.2** | UI `DagDetailModal` 渲染节点列表（含状态色 + provider/model/agent_key 显示；**状态刷新先用 polling 3-5s**） | `components/DagDetailModal.js` 修改 | e2e：截图前后对比，"暂无 DAG" → 真实节点列表；polling 可控制开关 | T5.1, S6.1, S6.2 | M | N（**方案先发用户**） |
| **T5.3** | UI Start 按钮（draft/ready 时可点 → 调 task_start_dag） | 同上 | e2e：点 Start → 状态变 running | T5.2, T1.1 | M | N |
| **T6.1** | UI 节点行展示 `assigned_to` → 跳到子 agent thread 链接 | 同上 | e2e：点击跳转正确 | T5.2 | S | Y |
| **T7.1** | UI 列表显示 `trigger / status / version / latest_run_status`（同 polling 3-5s 刷新） | `pages/DagsPage.js` / `DataPage.js` | e2e：列表字段渲染；polling 不造成可见闪烁 | T1.1, T3.1 | M | Y |
| **T8.1** | UI「AI 帮你设计流程」按钮（spawn 新 thread + 调 orchestration_launch_agent） | `pages/DagsPage.js` | e2e：点按钮 → 新 thread 创建 → 跳转到 thread | T4.1-T4.4 | M | N（**方案先发用户**） |
| **T8.2** | base 设计师 prompt 占位（实际 prompt 在 F7） | `cmd/mcp-orch/...` 相关 prompt_template 占位 | 占位 prompt 注入正确 | T8.1 | S | N |
| **T9.1** | codemap 索引刷新 | `docs/doc/codemap/02-mcp-orch.md` 等 | 新工具入索引 | T1-T4 完成 | S | N |

**T 阶段验收**：
- M2 里程碑：UI 上能看到 DAG → 点 Start → 节点状态变化（**目前 cron / AI 设计师都还是占位**）
- 端到端 `task_create_dag → task_dag_apply_ops 改一个节点 → task_start_dag → 看到节点状态变化` 通过
- AI（任意 thread 中的 LLM）能调 `task_dag_apply_ops` 改 DAG（手动让它做也能成）

**T 阶段提交粒度**：
- T1.1+T1.2 一次
- T2.1+T2.2 一次（ops 是大改，单独提）
- T3.1+T3.2 一次
- T4.1-T4.4 一次（registry 工具集）
- T5.1 / T5.2 / T5.3 各一次（前端**每次方案先发用户**）
- T6.1 单独
- T7.1 单独
- T8.1 / T8.2 各一次
- T9.1 单独（codemap）

约 12-14 个 commit。

---

## 3. 阶段 F 功能（26 任务）

| ID | 标题 | 主要触动文件 | 验收 | 依赖 | Size | 并行 |
|---|---|---|---|---|---|---|
| **F1.1** | `AgentExecutor` 解码 `node.config.exec` → `orchestration_launch_agent` 参数映射 | `cmd/mcp-orch/orchestration/executor_agent.go` | 单测：provider/model/agent_key/effort/language/tools 映射正确 | S1.3, S5.2 | M | Y |
| **F1.2** | `AgentExecutor` 处理 `inputs`：注入 prev nodes results / sharedfiles | 同上 | 集成测试：节点 B 看到节点 A.result | F1.1 | M | N |
| **F1.3** | `AgentExecutor` 处理 `outputs`：写 sharedfile / node.result | 同上 | 集成测试：sharedfile 内容正确写入 | F1.2 | S | N |
| **F1.4** | `AgentExecutor` 处理 transient/quota/validation 三类失败基础重试 | 同上 + `retry_strategy.go` 新建 | 单测：模拟三类失败重试次数正确 | F1.1, S7.1 | M | N |
| **F2.1** | `AutomationExecutor` 解码 `command_ref` → command_get + 执行 | `cmd/mcp-orch/orchestration/executor_automation.go` | 单测：command 执行 + 错误处理 | S1.4, S5.2 | M | Y（与 F1 并行） |
| **F2.2** | `AutomationExecutor` 处理 inputs/outputs | 同上 | 集成测试 | F2.1 | S | N |
| **F3.1** | `HybridExecutor` 串联 automation → agent verifier | `cmd/mcp-orch/orchestration/executor_hybrid.go` | 集成测试：automation 失败时 verifier 不跑 | F1, F2 完成 | M | N |
| **F4.1** | `ApplyOps` add_node 真实实现 + 环检测 | `cmd/mcp-orch/orchestration/dag_ops.go` | 单测：环检测；version+1 | S2.2, T2.2 | M | Y |
| **F4.2** | `ApplyOps` update_node 真实实现 | 同上 | 单测 | F4.1 | S | N |
| **F4.3** | `ApplyOps` remove_node 真实实现（含级联清理依赖） | 同上 | 单测：被依赖节点不能删除 | F4.2 | M | N |
| **F4.4** | `ApplyOps` update_dag 真实实现 | 同上 | 单测 | F4.1 | S | Y |
| **F4.5** | `ApplyOps` `status=running` 时只允许 add_node + depends_on 指向 done 节点 | 同上 | 单测：违规 ops 被拒 | F4.1 | M | N |
| **F5.1** | cron daemon 进程入口 + 接 robfig/cron 库 | `cmd/mcp-orch/orchestration/scheduler_cron.go` 新建 | 单测：cron 表达式解析正确 | S2.3 | M | Y |
| **F5.2** | `Scheduler.Tick` 真实实现：扫 `next_run_at <= now` → StartDAG | 同上 | 集成测试：到点自动起 run | F5.1, T1.2 | M | N |
| **F5.3** | cron 多实例锁（避免重复触发） | 同上 | 集成测试：两个进程只一个 tick 成功 | F5.2 | M | N |
| **F6.1** | `StartDAG` 真实实现：snapshot dag.version → run.dag_version_snapshot | `dag_lifecycle.go` 修改 | 集成测试：run 创建后改 dag 不影响这次 run | T1.2 | M | N |
| **F6.2** | run 终态判定：所有节点 done/failed/cancelled/skipped → run.status finished | 同上 | 集成测试 | F6.1 | M | N |
| **F7.1** | AI 设计师 prompt（中文版） | `internal/...` prompt_template 表 seed 或 migration | 集成测试：prompt 注入完整可用资源列表 | T4.1-T4.4 | M | Y |
| **F7.2** | AI 设计师 prompt（英文版） | 同上 | 同上 | F7.1 | S | N |
| **F8.1** | UI 节点编辑表单（typed schema → form field 映射规则） | `components/NodeEditForm.js` 新建 | 单测：schema 渲染对应控件 | S5.1 | L | Y（前端独立，**方案先发用户**） |
| **F8.2** | UI 表单下拉框接 `list_models` / `list_prompt_templates` | 同上 | e2e：下拉框数据正确 | F8.1, T4.1-T4.4 | M | N |
| **F9.1** | UI mermaid 拓扑图（DAG → mermaid 字符串） | `components/DagTopology.js` 新建 | e2e：截图对比 | T5.2 | M | Y |
| **F10.1** | UI run 历史时间轴 | `components/RunHistoryPanel.js` 新建 | e2e：点 run 看那次状态 | T3.2 | M | Y |
| **F11.1** | UI sharedfile 锁可视化（节点 reads/writes 联动） | `pages/SharedFilesPage.js` 修改 | e2e：sharedfile 显示"被节点 X 占用" | F1.3 | M | Y |
| **F12.1** | 智能重试 strategy dispatcher：`by_class` 分发（capability→escalate_model / validation→append_error / 关键节点→replan spawn planner） | `retry_strategy.go` 修改 | 集成测试：模拟 capability 失败 → 升级到 Opus 重跑；validation 失败 → schema 错误注入重跑；replan 策略 spawn planner agent | F1.4 | L | N |
| **F13.1** | lifecycle hooks 真实触发（before/after/on_state_change/on_failure） | `node_executor_dispatch.go` 新建 | 集成测试：hooks 在正确时机被调 | S1.1, S10 | M | Y |

**F 阶段验收**：M3 里程碑端到端用例通过：

> 「点『AI 帮你设计流程』按钮 → 新 thread → 在 thread 里说『帮我设计每天 8 点的报告生成 DAG』 → AI 输出 ops，DAG 创建 → 用户在 UI 改一处 prompt → 点 Start → 第一个 run 跑通 → 第二天 8 点自动起第二个 run → run 历史里看到两次执行 → 一次故意触发 capability 类失败 → 智能重试升级到 Opus → 通过」

**F 阶段提交粒度**：每个 F1-F13 子项独立 commit；同一 task 拆 .1/.2/.3 时，每子项独立 commit（按 prefer-small-commits）。约 25 个 commit。

---

## 4. 阶段 H 加固（按需触发，不预排）

| ID | 主题 | 触发条件 | 优先级 |
|---|---|---|---|
| H1 | 错误信息人话翻译（ErrCallerIdentityRequired / verdict_lost 等） | 用户看不懂报错 | 中 |
| H2 | 节点级 retry / fallback 策略调优 | 节点频繁失败、重试策略效果差 | 中 |
| H3 | 大 DAG 性能（N>50 拆批） | 真有 50+ 节点 DAG | 低 |
| H4 | `task_dag_revisions` 表（编辑历史/回滚 UI） | 用户想 undo | 低 |
| H5 | multi-tenant / 权限模型 | 多人协作场景 | 低 |
| H6 | 监控/告警（cron miss / run timeout） | 跑漏一天后 | 中 |
| H7 | inputs.summarization 真实实现 | 长 DAG 上下文爆炸 | 中 |
| H8 | token budget enforcement | 出现 token 失控成本 | 中 |
| H9 | task_post_message 原语真实落地 | 节点对话 sharedfile 不够用 | 低 |
| H10 | waiting_human HITL 完整流程 | 出现需要人审决策的场景 | 中 |

加固阶段任务**不预排**：每条 H 触发后单独立任务清单。

---

## 5. 关键里程碑映射

### 里程碑 M1 完成 = 阶段 S 全部 22 任务
**用户可见**：什么都没变，但代码内部干净了。

### 里程碑 M2 完成 = 阶段 S + T1.1, T1.2, T3.1, T3.2, T5.1, T5.2, T5.3
**用户可见**：UI 上能看到 DAG → 点 Start → 节点跑起来。
**还不能**：cron 自动触发；AI 帮你设计流程的智能化。

### 里程碑 M3 完成 = 阶段 S + T + F 全部
**用户可见**：两大需求 + 智能重试全部端到端通。

### 你两个核心需求的精确映射

**Need 1（每日定时任务）落地必备 task**：
- S2.3, S3.1, S3.3, S4, S5（cron 字段位 + run 表）
- T1.1, T1.2, T3.1, T3.2, T7.1（StartDAG + Run 工具 + UI 显示）
- F5.1, F5.2, F5.3, F6.1, F6.2（cron daemon + Run snapshot）
- F10.1（run 历史 UI）

**Need 2（AI 帮你设计流程）落地必备 task**：
- S1.1, S2.2, S4, S5（NodeExecutor + ops + typed schema）
- T2.1, T2.2, T4.1-T4.4, T5.2, T8.1, T8.2（ops 工具 + registry + UI 按钮）
- F1.1-F1.3, F4.1-F4.5, F7.1, F7.2, F8.1, F8.2（Executor + ApplyOps + 设计师 prompt + 表单）

---

## 6. 提交规范（commit message 模板）

```
<type>(<scope>): <subject>

<body 中文：动机 + 改动要点>

<footer：关联 task ID + 蓝图节号>
```

- `type`: feat / fix / refactor / docs / test / chore
- `scope`: dag / orch / mcp / ui / migrations / executor / scheduler
- `subject`: 中文，一句话
- 关联 task ID 写在 footer，例如 `Task: S1.1 / Blueprint: §10`

示例：
```
feat(orch): 定义 NodeExecutor 接口 + NodeOutcome 类型 (S1.1)

骨架阶段补丁 1：定义 DAG 节点执行器统一接口。
- NodeExecutor.Execute 返回 NodeOutcome 而不是裸 error
- NodeOutcome 含 Status / Result / FailureClass / RetryHint
- Hooks() map 接口位预留

Task: S1.1
Blueprint: docs/plans/dag改造蓝图v2.md §10 补丁 1, §9
```

---

## 7. 每次动手前的 4 步规矩

按 `feedback/任何-清死代码-重构-大批量改动-类工作开始前固定-4-步`：

1. `git fetch origin main`
2. `git log --left-right --oneline HEAD...origin/main` 看双向差距
3. 远程领先就先 pull
4. 写"改动清单 + 验证计划"短纸条给用户

前端 task（S6.x / T5.x / T6.1 / T7.1 / T8.x / F8.x / F9.x / F10.x / F11.x）按 `feedback/threadstore-whitelist-and-hmr.md` 必须先发方案给用户确认才动手。

---

## 8. 验证清单（每个 commit 前）

| 验证项 | 命令 | 何时跑 |
|---|---|---|
| Go build | `go build ./...` | 任何 Go 改动 |
| Go test | `go test ./...` | 任何 Go 改动 |
| Go vet | `go vet ./...` | 任何 Go 改动 |
| Architecture guard | `scripts/test_with_guard.sh` | 任何 Go 改动 |
| Frontend test | `cd cmd/agent-terminal/frontend && npm test` | 任何前端改动 |
| Frontend e2e（snapshot） | `npm run test:e2e` | UI 涉及视觉时 |
| Migration dry run | `make migrate-dry-run`（如有）| 任何 migration 改动 |
| golangci-lint | `golangci-lint run` | push 前必跑 |

push 必须等用户明确指令。

---

## 9. 并行计划（哪些 task 可同时开 worker）

**S 阶段并行图**（→ 表示依赖）：

```
S1.1 → S1.2 → S1.3 / S1.4 / S1.5 (三个 stub 并行)
S2.1 → S2.2
S2.3 (独立)
S3.1 → S3.2 / S3.3 / S3.4 (后两个并行)
       → S3.5
S4.1 → S4.2
S5.1 → S5.2
S6.1 → S6.2 (前端独立链)
S7.1 → S7.2
S15.1 (依赖 S3.4)
S16.1 (最后写)
```

可同时开 4-5 个 worker：
- worker A: S1 → S2 链
- worker B: S3 migration 链
- worker C: S4 / S5 typed schema 链
- worker D: S6 前端链（独立）
- worker E: S7 状态机链

**T 阶段**：T2.2（ApplyOps）是单点瓶颈，其他可并行。
**F 阶段**：F1 / F2 / F5 / F8 各自独立链，可 4 worker 并行。

---

## 10. 当前进度（用 grep 实时查）

```bash
# Need 1 cron 进度
grep -r "next_run_at" cmd/ migrations/ 2>/dev/null | wc -l

# Need 2 ops 进度
grep -r "task_dag_apply_ops" cmd/ 2>/dev/null | wc -l

# 死代码清理进度
grep -r "auto_handoff_phase1" cmd/ internal/ 2>/dev/null | wc -l   # 目标 0

# 状态机进度
grep -r "NodeStatusReady\|NodeStatusRetrying" cmd/ 2>/dev/null | wc -l  # 目标 ≥ 2

# typed schema 进度
grep -r "FailureClass\|OnFailureStrategy" cmd/ 2>/dev/null | wc -l  # 目标 ≥ 14
```

---

## 11. 下一步

进入 **S1.1（NodeExecutor 接口 + NodeOutcome 类型）**，按以下顺序执行：

1. `git fetch origin main` + 看双向差距
2. 写"改动清单 + 验证计划"短纸条给用户
3. 用户确认后落代码
4. `go build` / `go test` / `go vet` / `scripts/test_with_guard.sh` 全过
5. commit（模板见 §6）
6. 不主动 push

按 §9 并行计划，**S1.1 + S2.1 + S3.1 + S6.1（前端独立） + S7.1** 可同时起 5 个 worker，但建议先把 **S1.1 + S1.2** 跑通再扩散，让接口契约稳定后再让其他 task 引用它。
