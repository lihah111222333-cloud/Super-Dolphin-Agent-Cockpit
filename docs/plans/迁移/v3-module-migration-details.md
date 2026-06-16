# V3 模块迁移细化调查

> 调查基线：
> - 主方案：`docs/plans/迁移/v3-migration-plan.md`
> - 契约：`docs/契约/fx-convention.md`、`docs/契约/rungroup-convention.md`、`docs/契约/sqlc-convention.md`、`docs/契约/jrpc2-convention.md`、`docs/契约/modularity-convention.md`、`docs/契约/statemachine-event-convention.md`
> - 代码调查：仅使用 LSP 对 `go-agent-v2/` 和当前仓库做检索、符号结构、引用和调用层级分析
>
> 现状说明：
> - 当前仓库多数 `internal/module/*`、`internal/platform/*`、`internal/mcpserver/*` 目标目录尚未完全落地。
> - 当前仓库已存在的 V3 前置骨架主要在 `internal/app`、`internal/bus`、`internal/rpc`、`internal/runner`、`internal/store/module.go`、`internal/contract`、`internal/provider/unified`、`internal/platform/shared`。
> - `docs/plans/迁移/v3-migration-plan.md` 中引用的 `stateless-convention.md` / `event-convention.md` 未单独落盘；实际可执行契约位于 `docs/契约/statemachine-event-convention.md`。

## 内部模块

### 1. module/thread

#### 迁移来源（V2）
- `go-agent-v2/internal/apiserver/methods_thread_turn.go`
- `go-agent-v2/internal/apiserver/methods.go`
- `go-agent-v2/internal/apiserver/codexadapter/adapter_thread_listing.go`
- `go-agent-v2/internal/apiserver/codexadapter/thread_messages.go`
- `go-agent-v2/internal/apiserver/codexadapter/thread_recover.go`
- `go-agent-v2/legacy-agentsdk/service/listing/thread_listing_core.go`
- `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_io.go`
- `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_utils.go`
- `go-agent-v2/internal/store/agent_thread.go`
- `go-agent-v2/internal/store/agent_thread_binding.go`
- `go-agent-v2/internal/store/agent_provider_binding.go`

#### V3 目标文件结构
- `internal/module/thread/module.go`
- `internal/module/thread/contract.go`
- `internal/module/thread/service.go`
- `internal/module/thread/rpc.go`
- `internal/module/thread/events.go`
- `internal/module/thread/archive.go`
- `internal/module/thread/config.go`
- `internal/module/thread/helpers.go`

#### 6 框架使用方式
- fx：`thread.Module` 只对外提供一个 `Facade`、线程查询 facade 和 `handler.Map` 片段；归档、配置、绑定归一化留在包内协作对象，不向其他模块暴露二级 service。
- run.Group：无模块内长跑 actor；线程生命周期由 provider session actor 驱动，`thread` 只消费结果。
- sqlc：不直接依赖生成代码；只依赖 `store/thread`、`store/providerbinding` 之类接口。
- stateless：不持有主状态机；线程是会话聚合，不应再复制 V2 的隐式 provider 状态。
- jrpc2：负责 `thread/start`、`thread/resume`、`thread/recover`、`thread/fork`、`thread/archive`、`thread/unarchive`、`thread/delete`、`thread/read`、`thread/list`、`thread/messages`、`thread/name/set`、`thread/rollback`、`thread/config/*`；provider-specific 线程控制面按下表合并或下沉。
- kelindar/event：发布 `ThreadStarted`、`ThreadRecovered`、`ThreadArchived`、`ThreadDeleted`、`ThreadConfigUpdated`；订阅 provider 侧会话恢复事件。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 `platform/rpc` 的校验/中间件，`platform/config` 的 timeout，`platform/shared/pathscope`、`platform/shared/validation`、`platform/shared/idgen`。
- Zone B（模块内 DRY）：线程别名、绑定归一化、历史源选择、归档清理留在模块内，以 `archive.go`/`config.go`/`events.go` 收敛。
- Rule of Two 检查：若“线程别名归一化”同时被 `uistate` 和 `dashboard` 稳定复用，再提升到 `platform/shared`；否则保留在模块内。

#### V2 RPC 方法迁移处置表

| V2 方法 | 处置 | V3 去向 | 说明 |
| --- | --- | --- | --- |
| `thread/name/set` | 保留 | `module/thread/service.go` + `rpc.go` | 结构化线程元数据更新，继续由 thread 域拥有。 |
| `thread/compact/start` | 下沉 | `provider/unified` capability facade；`module/thread/rpc.go` 仅保留兼容包装 | 本质是 provider-specific compaction，不扩展为 thread 主 contract。 |
| `thread/rollback` | 保留 | `module/thread/service.go` + `archive.go` | 属于线程历史控制，不再散在 slash command wrapper。 |
| `thread/loaded/list` | 合并 | 合并进 `thread/list`，新增 `loadedOnly` / `runtimeOnly` 过滤 | 不保留第二条并行注册链。 |
| `thread/read` | 保留 | `module/thread/service.go` + `rpc.go` | 线程详情查询。 |
| `thread/resolve` | 合并 | 合并进 `thread/read` / `thread/list` 的 canonical ID 解析 | 不再单列 RPC。 |
| `thread/config/get` | 保留 | `module/thread/config.go` + `rpc.go` | 结构化 config 读取。 |
| `thread/config/set` | 保留 | `module/thread/config.go` + `rpc.go` | 结构化 config 写入。 |
| `thread/messages` | 保留 | `module/thread/archive.go` + `rpc.go` | 统一运行时 timeline 与持久化历史读取。 |
| `thread/backgroundTerminals/clean` | 下沉 | `provider/unified` session-control facade | 属于 provider session 清理，不进入 thread 领域接口。 |
| `thread/realtime/start` | 下沉 | `provider/unified` realtime 子 facade | phase 1 不作为 thread 模块主 contract。 |
| `thread/realtime/appendAudio` | 下沉 | `provider/unified` realtime 子 facade | 保留 provider 专属语义，不在 thread 域复制 transport 细节。 |
| `thread/realtime/appendText` | 下沉 | `provider/unified` realtime 子 facade | 同上。 |
| `thread/realtime/stop` | 下沉 | `provider/unified` realtime 子 facade | 同上。 |
| `thread/undo` | 下沉 | `provider/unified` session-control facade | 仍属 provider command，不再由 thread 域定义业务语义。 |
| `thread/model/set` | 合并 | 合并进 `thread/config/set` | 改成结构化字段，不再走 slash command 字符串透传。 |
| `thread/personality/set` | 合并 | 合并进 `thread/config/set` | 改成结构化字段。 |
| `thread/approvals/set` | 合并 | 合并进 `thread/config/set` | 改成结构化字段。 |
| `thread/mcp/list` | 下沉 | `provider/unified/mcp_manifest.go` 或 `tool/registry` 只读 facade | 这是工具面查询，不再归 thread 域拥有。 |
| `thread/skills/list` | 下沉 | `module/skill/service.go` 的 thread-scope facade | 技能域拥有技能视图，thread 只消费结果。 |
| `thread/debugMemory` | 删除 | 无公共去向 | 仅保留本地调试入口，不进入正式 V3 RPC 契约。 |
| `review/start` | 合并 | 合并进 `module/turn` 的 `review/start` | review 是 turn 派生流程，不属于 thread 域。 |
| `mock/experimentalMethod` | 删除 | 无 | demo/stub 方法，不进入 V3 正式面。 |

#### 关键迁移风险
- V2 中 `threadID`、`agentID`、provider session ID 混用，迁移时必须先统一主键语义。
- 归档/反归档同时涉及 provider 状态、绑定表和 UI 偏好，极易出现部分成功。
- `ThreadMessages` 在运行中会话与历史回放之间切换，V3 必须明确“实时源优先”还是“持久化源优先”。

#### 代码量预估
- V2：约 `1200-1600` 行。
- V3：约 `700-1000` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 8 个 |
| 包有效行数 | ≤ 1200 行 |

**专项守卫：**
- RPC golden response contract：冻结 `thread/read`、`thread/list`、`thread/messages`、`thread/config/*` 的响应 shape。
- 副作用守卫：归档、反归档、recover、绑定更新只允许一次持久化决策和一次事件发布。
- 事件映射守卫：`ThreadStarted`、`ThreadRecovered`、`ThreadArchived`、`ThreadDeleted`、`ThreadConfigUpdated` 的 payload 与 route 固定。
- `jrpc2` 严格模式守卫：公共 handler 只允许出现在 `rpc.go`，并使用 strict object-only binder。
- timeout 散落守卫：模块内不得新增 `context.WithTimeout`。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 2. module/turn

#### 迁移来源（V2）
- `go-agent-v2/internal/apiserver/methods_turn.go`
- `go-agent-v2/internal/apiserver/methods_thread_turn.go`
- `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go`
- `go-agent-v2/internal/apiserver/codexadapter/adapter_deferred_turn_start.go`
- `go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_adapters.go`

#### V3 目标文件结构
- `internal/module/turn/module.go`
- `internal/module/turn/contract.go`
- `internal/module/turn/service.go`
- `internal/module/turn/prepare.go`
- `internal/module/turn/runtime.go`
- `internal/module/turn/tracker.go`
- `internal/module/turn/review.go`
- `internal/module/turn/rpc.go`

#### 6 框架使用方式
- fx：`turn.Module` 只对外提供一个 `Facade` 和 `handler.Map`；`prepare`、`runtime`、`tracker`、`review` 是包内协作对象，不跨模块暴露独立 service。
- run.Group：不直接持有独立 actor；tracker 通过 bus 订阅驱动，不单独暴露长跑循环。
- sqlc：间接依赖 interaction/audit/thread 相关 store 接口；不直接碰 `sqlc` 生成层。
- stateless：phase 1 不单独建 turn 状态机；turn 状态由 provider 事件 + orchestration 规则投影。只有 review 子流复杂化后才单独上 stateless。
- jrpc2：负责 `turn/start`、`turn/steer`、`turn/interrupt`、`turn/forceComplete`、`review/start`。
- kelindar/event：发布 `TurnPrepared`、`TurnStarted`、`TurnDeferred`、`TurnInterrupted`、`TurnCompleted`、`ReviewStarted`；订阅 tool call、approval、provider delta 事件。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 `platform/rpc` 验证链、`platform/config` timeout、`provider/unified` 的统一 `TurnRequest`。
- Zone B（模块内 DRY）：prepare/runtime/tracker/review 四段各自独立文件，不再把输入整形、技能解析、延迟投递、review 参数拼在单个 handler 中。
- Rule of Two 检查：若“deferred turn start 恢复”逻辑在 main turn 和 sub-agent turn 都复用，再提升为 `platform/shared/deferred.go`；否则留在 turn 模块。

#### 关键迁移风险
- V2 的 deferred turn start 恢复路径隐式依赖 adapter 缓存；V3 若不先定义持久化/内存边界，会丢转向请求。
- interrupt/forceComplete 在 V2 同时读 runtime hook 和 provider 状态；迁移时必须先明确权威状态源。
- review/start 本质是 turn 的派生流程，不应再通过 slash command 隐式绕路。

#### 代码量预估
- V2：约 `1600-2100` 行。
- V3：约 `900-1300` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 9 个 |
| 包有效行数 | ≤ 1600 行 |

**专项守卫：**
- RPC golden response contract：冻结 `turn/start`、`turn/steer`、`turn/interrupt`、`turn/forceComplete`、`review/start` 的响应 shape。
- 副作用守卫：prepare、runtime、tracker、review 的持久化、事件发布、provider 调用必须定序。
- 错误路径守卫：interrupt、force-complete、deferred turn 恢复不能留下半状态。
- 事件映射守卫：provider delta、tool call、approval 事件到 turn 生命周期事件的映射必须固定。
- `jrpc2` 严格模式守卫：公共 handler 只允许在 `rpc.go`，并拒绝数组参数和未知字段。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 3. module/skill

#### 迁移来源（V2）
- `go-agent-v2/internal/service/skills_core.go`
- `go-agent-v2/internal/service/skills_indexing.go`
- `go-agent-v2/internal/service/skills_import.go`
- `go-agent-v2/internal/service/skills_frontmatter.go`
- `go-agent-v2/internal/service/skills_parser.go`
- `go-agent-v2/internal/skills/manager.go`
- `go-agent-v2/internal/skills/methods.go`
- `go-agent-v2/internal/apiserver/methods_command.go`
- `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go`

#### V3 目标文件结构
- `internal/module/skill/module.go`
- `internal/module/skill/contract.go`
- `internal/module/skill/service.go`
- `internal/module/skill/loader.go`
- `internal/module/skill/matcher.go`
- `internal/module/skill/helpers.go`
- `internal/module/skill/rpc.go`

#### 6 框架使用方式
- fx：`skill.Module` 提供 `Service`、磁盘 loader、matcher、`handler.Map`。
- run.Group：无模块级 actor；技能索引按需加载，不常驻后台循环。
- sqlc：无直接使用；该域核心是文件系统和元数据，不是数据库。
- stateless：不需要。
- jrpc2：负责 `skills/list`、`skills/local/*`、`skills/remote/*`、`skills/config/*`、`skills/summary/write`、`skills/match/preview`。
- kelindar/event：发布 `SkillImported`、`SkillUpdated`、`SkillDeleted`、`SkillConfigChanged`；订阅 thread/turn 事件只做匹配，不持有状态。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：只复用路径校验、错误归一化、通用文件读取 budget。
- Zone B（模块内 DRY）：frontmatter 解析、索引刷新、导入复制、自动匹配评分全部留在 skill 模块，避免再次分裂到 `service` + `skills` + `apiserver` 三层。
- Rule of Two 检查：frontmatter 解析若被 prompt/template 等其他模块复用，再上提到 `platform/shared/frontmatter.go`。

#### 关键迁移风险
- V2 同时存在 `service.SkillService` 和 `internal/skills.Manager`；迁移时必须合并成单一业务入口。
- 本地技能与远端线程内已加载技能是两个不同概念，V3 需要明确 contract。
- 自动匹配直接影响 turn prepare，匹配结果必须可解释且可测试。

#### 代码量预估
- V2：约 `1400-1800` 行。
- V3：约 `600-900` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 8 个 |
| 包有效行数 | ≤ 1100 行 |

**专项守卫：**
- RPC golden response contract：冻结 `skills/list`、`skills/match/preview`、`skills/config/*` 的响应 shape。
- 副作用守卫：导入、覆盖、删除、重建索引不能产生重复写入或遗漏刷新。
- 协议守卫：frontmatter、summary、config DTO 的字段语义固定，不接受隐式扩张。
- 错误路径守卫：非法路径、frontmatter 解析失败、远端技能元数据损坏都必须走稳定错误分支。
- `jrpc2` 严格模式守卫：`rpc.go` 只承接 transport，参数校验一律走 typed request。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 4. module/orchestration

#### 迁移来源（V2）
- `go-agent-v2/internal/runner/manager.go`
- `go-agent-v2/internal/runner/manager_launch.go`
- `go-agent-v2/internal/runner/manager_recover.go`
- `go-agent-v2/internal/runner/manager_auto_recover.go`
- `go-agent-v2/internal/runner/manager_lifecycle.go`
- `go-agent-v2/internal/runner/provider_registry.go`
- `go-agent-v2/internal/apiserver/dagwatcher/*`
- `go-agent-v2/internal/store/task_dag.go`
- `go-agent-v2/internal/store/task_dag_phase1.go`
- `go-agent-v2/internal/store/task_ack.go`
- `go-agent-v2/internal/store/task_trace.go`
- `go-agent-v2/internal/bus/orchestration.go`
- `go-agent-v2/pkg/toolsdk/tools/orchestration.go`
- `go-agent-v2/pkg/toolsdk/tools/resource_dag.go`
- `go-agent-v2/internal/apiserver/tool_provider_adapters.go`

#### V3 目标文件结构
- `internal/sidecar/orch/orchestration/module.go`
- `internal/sidecar/orch/orchestration/contract.go`
- `internal/sidecar/orch/orchestration/service.go`
- `internal/sidecar/orch/orchestration/phase1_watcher.go`
- `internal/sidecar/orch/orchestration/runner_actor.go`
- `internal/sidecar/orch/orchestration/recover.go`
- `internal/sidecar/orch/orchestration/events.go`
- `internal/sidecar/orch/orchestration/patterns.go`

#### 6 框架使用方式
- fx：`orchestration.Module` 提供 DAG service、phase1 watcher、recovery service、tool-facing facade、`Runner` 适配输出。
- run.Group：强依赖；phase1 watcher、agent runtime supervisor、recover worker 都注册到 `group:"runners"`。
- sqlc：不直接碰生成层；通过 `store/taskdag`、`store/taskack`、`store/tasktrace`、`store/workspace` 接口访问，其中 `taskdag` 同时承接 phase1 wakeup/lease/turn binding 能力。
- stateless：负责 runner lifecycle/recover 规则；状态表定义在模块内，factory 在 `platform/statemachine`。
- jrpc2：负责 `orchestration_*`、`task_*` 以及 DAG/phase1 相关 RPC。
- kelindar/event：发布 `DAGCreated`、`NodeReady`、`NodeStarted`、`NodeDone`、`NodeFailed`、`RecoverTriggered`；订阅 `AgentStateChanged`、`WorkspaceRunMerged`、`TurnCompleted`。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 `platform/runner`、`platform/statemachine`、`platform/bus`、`platform/db` 事务边界和 `platform/config` timeout。
- Zone B（模块内 DRY）：依赖检查、就绪判定、队列策略、recover 选择器必须集中在 orchestration 内部，不再散落到 bus、runner、apiserver、MCP adapter。
- Rule of Two 检查：如果“backoff/lease 轮询”模式被 orchestration 和 ida 共同使用，再提到 `platform/shared/poller.go`。

#### 关键迁移风险
- V2 中同步 RPC、异步 watcher、store 事务和 bus 事件混写；V3 若不切清命令面和 actor 面，会再次出现重复调度。
- recover 逻辑跨 provider、runner、thread、workspace，多处 side effect 极易失幂等。
- DAG phase1 watcher 涉及 scope/CWD、assignee、thread busy 检查，边界错误会直接造成脏任务。

#### 代码量预估
- V2：约 `5000-6500` 行。
- V3：约 `2200-3200` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 12 个 |
| 包有效行数 | ≤ 3500 行（迁移期显式例外） |

**专项守卫：**
- `stateless` 全矩阵守卫：state、trigger、guard、entry/exit action 组合必须全量表驱动验证。
- 副作用守卫：DAG create/start/lease/recover 的持久化、事件发布、ack 更新必须有稳定顺序。
- 并发守卫：phase1 watcher、lease worker、recover worker 不能双启动、双消费或双回收。
- 事件映射守卫：`DAGCreated`、`NodeReady`、`NodeStarted`、`NodeDone`、`NodeFailed`、`RecoverTriggered` 的路由和 payload 固定。
- `jrpc2` 严格模式守卫：公共方法全部走 strict object-only binder，不允许第二条注册链。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 5. module/workspace

#### 迁移来源（V2）
- `go-agent-v2/internal/service/workspace.go`
- `go-agent-v2/internal/service/workspace_file_ops.go`
- `go-agent-v2/internal/apiserver/workspace_methods.go`
- `go-agent-v2/internal/apiserver/tool_provider_adapters.go`
- `go-agent-v2/internal/mcp/resource_adapters.go`
- `go-agent-v2/internal/store/workspace_run.go`
- `go-agent-v2/migrations/0006_workspace_runs.sql`

#### V3 目标文件结构
- `internal/module/workspace/module.go`
- `internal/module/workspace/contract.go`
- `internal/module/workspace/service.go`
- `internal/module/workspace/merge.go`
- `internal/module/workspace/helpers.go`
- `internal/module/workspace/rpc.go`

#### 6 框架使用方式
- fx：`workspace.Module` 提供 `Service`、merge engine、RPC facade。
- run.Group：无常驻 actor；需要后台清理时由独立 runner 接管，不放进 `Service` constructor。
- sqlc：间接依赖 `store/workspace`，由 store 层包住 `workspace_runs` 和 `workspace_run_files` 查询。
- stateless：phase 1 不单独建状态机；`active -> merging -> merged|aborted|failed` 仍由 service 显式持久化。
- jrpc2：负责 `workspace/run/create|get|list|merge|abort`。
- kelindar/event：发布 `WorkspaceRunCreated`、`WorkspaceRunMerging`、`WorkspaceRunMerged`、`WorkspaceRunAborted`、`WorkspaceRunFailed`。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 `platform/shared/pathscope`、`platform/shared/hash`、`platform/config` 的文件大小/超时预算。
- Zone B（模块内 DRY）：bootstrap、walk、candidate build、apply、delete handling 分文件收敛；不要再把路径校验和状态更新复制到 tool adapter 与 RPC handler。
- Rule of Two 检查：若“文件哈希 + atomic copy + root containment”被 `ida`/`dashboard code-open save` 同时复用，再提到 `platform/shared/fileops.go`。

#### 关键迁移风险
- 文件系统与数据库双写；任一步骤中断都可能造成状态漂移。
- 合并冲突判定若不稳定，会让 orchestration / workspace / UI 三边看到不同结论。
- 路径逃逸和 symlink 处理是安全边界，不能回退到 V2 的分散校验。

#### 代码量预估
- V2：约 `1000-1400` 行。
- V3：约 `700-1000` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 8 个 |
| 包有效行数 | ≤ 1200 行 |

**专项守卫：**
- RPC golden response contract：冻结 `workspace/run/create|get|list|merge|abort` 的响应 envelope。
- 副作用守卫：merge、abort、delete handling 不能出现 DB 与文件系统双写失配。
- 并发守卫：同一 run 的 merge/abort 互斥，不能并发推进到两个终态。
- 错误路径守卫：部分复制、冲突、删除失败都必须保持可恢复状态。
- 路径范围守卫：root containment、symlink、scope/CWD 校验必须稳定。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 6. module/uistate

#### 迁移来源（V2）
- `go-agent-v2/internal/uistate/*`
- `go-agent-v2/internal/apiserver/methods_ui_state.go`
- `go-agent-v2/internal/apiserver/server_event_handler.go`
- `go-agent-v2/internal/dashboard/state_service.go`

#### V3 目标文件结构
- `internal/module/uistate/module.go`
- `internal/module/uistate/contract.go`
- `internal/module/uistate/runtime.go`
- `internal/module/uistate/projection.go`
- `internal/module/uistate/patterns.go`
- `internal/module/uistate/rpc_bridge.go`

#### 6 框架使用方式
- fx：`uistate.Module` 提供 runtime projector、preference facade、只读 projection assembler。
- run.Group：无独立 actor；依赖 event subscription 和 request-time assembly。
- sqlc：间接依赖 UI preference store 和可能的 lightweight read-model store。
- stateless：不承担真相状态；只消费状态机和事件总线输出。
- jrpc2：负责 `ui/state/get`、`ui/sidebar/get`、`ui/dashboard/get` 这类投影型 handler。
- kelindar/event：重度订阅 `TurnStarted`、`AssistantDelta`、`ToolCall*`、`Approval*`、`WorkspaceRun*`、`AgentStateChanged`；一般不反向发布业务事件。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 `platform/bus` typed event、`platform/shared/jsonutil`、`platform/shared/cursor`。
- Zone B（模块内 DRY）：事件归一化、timeline patch、snapshot clone、state resolution 必须留在 `uistate`；不要再拆到 apiserver 或 dashboard。
- Rule of Two 检查：若 timeline item ID 生成和 deep-copy helper 同时被 `dashboard` 和 `thread` 复用，再上提。

#### 关键迁移风险
- `uistate` 是高扇入投影面，最容易再次长成 God package。
- 事件顺序、backfill 与 patch 更新若没有统一 contract，会出现 UI “跳回去”。
- preference 与 runtime projection 混在一起时，scope/CWD 切换最容易回归。

#### 代码量预估
- V2：约 `3000-4000` 行。
- V3：约 `1200-1800` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 9 个 |
| 包有效行数 | ≤ 2200 行 |

**专项守卫：**
- RPC golden response contract：冻结 `ui/state/get`、`ui/sidebar/get`、`ui/dashboard/get` 的投影 shape。
- 事件映射守卫：typed event 到 timeline patch、snapshot clone、state resolution 的映射必须固定。
- 并发守卫：投影更新、backfill、订阅取消在高扇入场景下不能乱序或丢补丁。
- 错误路径守卫：缺字段事件、回放失败、快照复制失败都必须走稳定降级路径。
- `jrpc2` 严格模式守卫：桥接层只暴露只读 handler，不能把 transport 细节带进 projector。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 7. module/coderun

#### 迁移来源（V2）
- `go-agent-v2/internal/executor/code_runner.go`
- `go-agent-v2/pkg/toolsdk/tools/code_run.go`
- `go-agent-v2/internal/apiserver/tool_provider_adapters.go`
- `go-agent-v2/internal/mcp/runtime.go`

#### V3 目标文件结构
- `internal/module/coderun/module.go`
- `internal/module/coderun/contract.go`
- `internal/module/coderun/service.go`
- `internal/module/coderun/tool.go`
- `internal/module/coderun/audit.go`

#### 6 框架使用方式
- fx：`coderun.Module` 提供 `Service` 和 tool-facing facade。
- run.Group：无独立 actor。
- sqlc：不直接使用；只通过 audit/system log store 追加审计。
- stateless：不需要。
- jrpc2：通常不暴露公共 RPC；主要通过 tool registry 暴露 `code_run` / `code_run_test`。
- kelindar/event：发布 `CodeRunStarted`、`CodeRunFinished`、`CodeRunDenied`；订阅 thread stop / workspace abort 取消在途执行。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 `platform/shared/pathscope`、`platform/shared/truncate`、`platform/config` 的执行 timeout。
- Zone B（模块内 DRY）：危险命令探测、workdir 归一化、审计 payload 截断留在模块内，不要散回 tool adapter。
- Rule of Two 检查：若“dangerous command gate”同时被 coderun 和 ida shell worker 复用，再提升。

#### 关键迁移风险
- workdir sandbox、符号链接、项目根限制是硬安全边界。
- 取消执行必须连带 kill process group；只 cancel context 不够。
- 审计日志和输出截断若不统一，会让 dashboard/tool 两侧看到不同结果。

#### 代码量预估
- V2：约 `900-1200` 行。
- V3：约 `500-700` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 6 个 |
| 包有效行数 | ≤ 900 行 |

**专项守卫：**
- 响应守卫：`code_run` / `code_run_test` 结果 envelope、截断策略、退出码语义固定。
- 副作用守卫：审计写入、进程启动、工作目录解析只能出现一次决策路径。
- 错误路径守卫：拒绝执行、启动失败、超时、取消都必须显式杀掉进程组并记录审计。
- 危险命令门禁守卫：危险命令检测不能被 wrapper 层绕过。
- timeout 散落守卫：执行超时统一来自 `platform/config/timeouts.go`。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 8. module/ida

#### 迁移来源（V2）
- `go-agent-v2/pkg/idamcp/*`
- `go-agent-v2/pkg/toolsdk/tools/ida_tools*.go`
- `go-agent-v2/internal/mcp/runtime_ida.go`

#### V3 目标文件结构
- `internal/module/ida/module.go`
- `internal/module/ida/contract.go`
- `internal/module/ida/service.go`
- `internal/module/ida/lifecycle.go`
- `internal/module/ida/gateway.go`
- `internal/module/ida/helpers.go`

#### 6 框架使用方式
- fx：`ida.Module` 提供 gateway manager、lease service、tool facade。
- run.Group：需要；gateway supervisor、feedback/poll worker、长期附着调试会话都应注册为 actor。
- sqlc：通常不直接使用；必要审计走上层 store。
- stateless：适合表达 gateway lifecycle，如 `booting -> ready -> attached -> stopping -> stopped|error`。
- jrpc2：不直接暴露公共 app RPC；主要通过 MCP tool 面对外。
- kelindar/event：发布 `IDAGatewayStarted`、`IDALeaseGranted`、`IDADebugAttached`、`IDAStopped`；订阅 workspace/thread 事件做环境协调。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 `platform/runner`、`platform/statemachine`、`platform/shared/pathscope`、`platform/shared/retry`。
- Zone B（模块内 DRY）：lease、forward、workspace materialize、tool envelope、gateway spawn 必须都留在 ida 模块，不再和 mcp runtime 混写。
- Rule of Two 检查：只有当 `lease/backoff` 与 orchestration 稳定共享时才允许提升。

#### 关键迁移风险
- 强平台依赖、端口/设备 lease、Python/IDA/Frida 运行时耦合高。
- tool surface 大且版本差异多，schema 冻结必须先行。
- workspace materialization 与本地工具链路径校验是安全和稳定性双重风险。

#### 代码量预估
- V2：约 `4500-6000` 行。
- V3：约 `2000-3000` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 12 个 |
| 包有效行数 | ≤ 3200 行（迁移期显式例外） |

**专项守卫：**
- `stateless` 生命周期矩阵守卫：gateway 状态、lease、attach、stop 迁移必须全量枚举。
- 协议守卫：IDA tool schema、gateway envelope、debug attach/recover contract 必须冻结。
- 并发守卫：transport locking、gateway supervisor、worker bootstrap 不能出现锁顺序回归。
- 错误路径守卫：attach、shutdown、materialize、lease 失败必须保留可诊断状态。
- MCP 家族隔离守卫：该模块只能通过 `tool/ida` / `mcpserver/ida` 暴露，不得反向耦合其他 family。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 9. module/dashboard

#### 迁移来源（V2）
- `go-agent-v2/internal/dashboard/*`
- `go-agent-v2/internal/apiserver/dashboard_bindings.go`
- `go-agent-v2/cmd/server/main.go`

#### V3 目标文件结构
- `internal/module/dashboard/module.go`
- `internal/module/dashboard/contract.go`
- `internal/module/dashboard/service.go`
- `internal/module/dashboard/rpc.go`
- `internal/module/dashboard/projection.go`

#### 6 框架使用方式
- fx：`dashboard.Module` 提供 read-model service 和 RPC projection facade。
- run.Group：模块本身无 actor；HTTP/SSE server actor 归 `internal/ui/dashboard` 或入口层。
- sqlc：不直接碰生成层；通过 dashboard 相关 store 接口读取聚合数据。
- stateless：不需要。
- jrpc2：负责 dashboard 聚合查询和 code-open 相关 RPC。
- kelindar/event：订阅 agent/DAG/workspace/skill 事件刷新投影；通常不反向发布领域事件。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 `platform/rpc`、`platform/shared/cursor`、`platform/shared/pathscope`。
- Zone B（模块内 DRY）：scope 过滤、聚合视图、code-open 预览逻辑留在 dashboard 模块或 `internal/ui/dashboard`，不回流到 store。
- Rule of Two 检查：若 code-open 文本预览和 workspace diff 复用同一文件读裁剪逻辑，再上提。

#### 关键迁移风险
- dashboard 读模型跨 10+ store，最容易重新形成隐式 god service。
- scope/CWD 过滤若仍散在 handler 级，迁移后会继续出现“跨项目脏数据”。
- code-open 功能应视为 UI 辅助，不应反向污染领域层。

#### 代码量预估
- V2：约 `1200-1600` 行。
- V3：约 `700-900` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 7 个 |
| 包有效行数 | ≤ 1200 行 |

**专项守卫：**
- RPC golden response contract：冻结 dashboard 聚合查询与 code-open 预览返回 shape。
- 事件映射守卫：agent、DAG、workspace、skill 事件到 dashboard projection 的映射必须固定。
- 错误路径守卫：scope/CWD 过滤失败、文件预览失败、聚合缺项都必须给出稳定降级结果。
- 路径范围守卫：code-open 只能读取允许的工作区范围。
- `jrpc2` 严格模式守卫：只允许 object 参数和显式字段集。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 9.A module/core（已废弃）

- 不再建立 `internal/module/core`；原职责拆回已有模块和平台层。
- `initialize`、`initialized` 以及初始化兼容 noop 归 `internal/platform/rpc/initialize.go`。
- `approval/respond` 归 `internal/module/turn/rpc.go`，等待/确认状态迁移归 `internal/platform/rpc/approval.go`。
- `log/list`、`log/filters`、`log/relay` 归 `internal/platform/bus` sink + `internal/platform/rpc/push.go`。

### 9.B module/config（已废弃）

- 不再建立独立 `internal/module/config`。
- `thread/config/get`、`thread/config/set` 以及 `thread/model/set`、`thread/personality/set`、`thread/approvals/set` 的兼容包装统一收敛到 `internal/module/thread/rpc.go`。
- 全局配置读取/写入、LSP prompt hint、MCP server reload 归 `internal/platform/config`，由 `internal/platform/rpc/initialize.go` 暴露 RPC facade。

### 9.C module/debug（已废弃）

- 不再建立 `internal/module/debug`。
- `debug/runtime`、`debug/gc`、`thread/debugMemory`、`mock/experimentalMethod` 以及其余 compat/debug stub 统一归 `internal/platform/rpc/debug.go`。
- `module/debug` 只保留迁移映射，不作为 V3 最终包边界。

## 平台层

### 10. platform/rpc

#### 迁移来源（V2）
- `go-agent-v2/internal/apiserver/server.go`
- `go-agent-v2/internal/apiserver/methods.go`
- `go-agent-v2/internal/apiserver/methods_turn.go`
- `go-agent-v2/internal/apiserver/methods_thread_turn.go`
- `go-agent-v2/internal/apiserver/dashboard_bindings.go`
- `go-agent-v2/cmd/app-server/main.go`
- 当前仓库前置骨架：`internal/rpc/module.go`、`internal/rpc/handlers.go`

#### V3 目标文件结构
- `internal/platform/rpc/module.go`
- `internal/platform/rpc/initialize.go`
- `internal/platform/rpc/server.go`
- `internal/platform/rpc/registry.go`
- `internal/platform/rpc/middleware.go`
- `internal/platform/rpc/errors.go`
- `internal/platform/rpc/request_context.go`
- `internal/platform/rpc/codec.go`
- `internal/platform/rpc/transport_ws.go`
- `internal/platform/rpc/approval.go`
- `internal/platform/rpc/push.go`
- `internal/platform/rpc/debug.go`

#### 6 框架使用方式
- fx：`rpc.Module` 提供 config、registry、middleware、server，并收集各模块的 `handler.Map` 片段。
- run.Group：jrpc2 server 是一个 actor，由 `platform/runner` 托管。
- sqlc：无直接使用。
- stateless：无直接使用。
- jrpc2：核心承接层；统一 `handler.Map`、`WrapAssigner`、错误映射、strict binder、push bridge、approval 状态机桥接。
- kelindar/event：只通过窄桥接接口把 typed event 推给 RPC client，不直接承载业务总线语义。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：中间件链、validated binder、error builder、context accessor 都是平台层共享能力。
- Zone B（模块内 DRY）：具体业务 route 留在 `module/*/rpc.go`；`platform/rpc` 只保留 transport、registry、middleware。
- Rule of Two 检查：如果某类 request-scoped helper 只被 thread 一个模块用，不能放进 `platform/rpc`。

#### P5 修正版补充：Server 基础设施归宿

| V2 文件 | 行数 | V3 归宿 |
|---|---:|---|
| `server_conn_ws.go` | 277 | `internal/platform/rpc/transport_ws.go` |
| `server_payload.go` | 412 | `internal/platform/rpc/codec.go` |
| `server_approval.go` | 483 | `internal/platform/rpc/approval.go` |
| `notifications.go` | 100 | `internal/platform/rpc/push.go` |

#### 关键迁移风险
- 若 registry/middleware/context 再次与业务耦合，V2 的 God Object 会原样重生。
- HTTP/WebSocket/push/approval 语义不同，必须先定义 transport contract 和状态机边界。
- 不能再保留 dashboard/特殊方法的旁路注册链。

#### 代码量预估
- V2：约 `2500-3500` 行相关骨架。
- V3：约 `900-1300` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 9 个 |
| 包有效行数 | ≤ 1500 行 |

**专项守卫：**
- RPC golden response contract：统一冻结公共方法的响应和 notify envelope。
- `jrpc2` 严格模式守卫：所有公共方法必须使用 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`。
- 错误路径守卫：错误码映射、invalid params、capability denied、transport failure 的返回必须稳定。
- 依赖方向守卫：`platform/rpc` 不能 import `module/*` concrete package，也不能形成第二条注册链。
- `fx` import 范围守卫：`fx` 只允许出现在 `module.go`。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 11. platform/db

#### 迁移来源（V2）
- `go-agent-v2/internal/database/pool.go`
- `go-agent-v2/internal/database/migrator.go`
- `go-agent-v2/internal/store/helpers.go`
- `go-agent-v2/internal/store/sql_safety.go`
- `go-agent-v2/migrations/*`
- 当前仓库前置骨架：`internal/database/*`、`internal/store/module.go`

#### V3 目标文件结构
- `internal/platform/db/module.go`
- `internal/platform/db/pool.go`
- `internal/platform/db/migrate.go`
- `internal/platform/db/tx.go`
- `internal/platform/db/queries.go`
- `internal/platform/db/query_guard.go`

#### 6 框架使用方式
- fx：提供 `Config`、`*pgxpool.Pool`、`*sqlc.Queries`、`sqlc.Querier` 和 migration hook。
- run.Group：不使用；DB 是资源，不是 actor。
- sqlc：核心承接层；schema、query、Querier、WithTx 都在这里生效。
- stateless：不使用。
- jrpc2：不使用。
- kelindar/event：不直接使用；日志/审计由上层模块发布再决定是否持久化。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：事务封装、query guard、pool lifecycle 是平台层统一资产。
- Zone B（模块内 DRY）：各 store 的领域语义查询不应放回 `platform/db`。
- Rule of Two 检查：只有“纯数据库技术性 helper”能进 `platform/db`/`platform/shared`，业务命名查询一律留在 store。

#### 关键迁移风险
- migration 顺序与 `sqlc` schema 一致性必须锁死。
- `DBQueryStore` 不能被误迁进 `sqlc`。
- 事务边界若仍由业务模块手搓，会复制 V2 `BaseStore` 模式。

#### 代码量预估
- V2：约 `600-900` 行 Go 代码，外加 `migrations/`。
- V3：约 `350-500` 行手写代码，外加 `sqlc` 生成物。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 6 个 |
| 包有效行数 | ≤ 600 行 |

**专项守卫：**
- `sqlc` import 边界守卫：生成目录只允许被 `store/*` 依赖，平台层只暴露 pool/tx/Querier。
- 协议守卫：migration hook、`Queries.WithTx`、query guard surface 必须稳定。
- 错误路径守卫：事务回滚、pool 初始化、迁移失败必须给出一致错误分支。
- 依赖方向守卫：`platform/db` 不得回依赖具体 store。
- `fx` import 范围守卫：`fx` 只允许出现在 `module.go`。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 12. platform/bus

#### 迁移来源（V2）
- `go-agent-v2/internal/bus/bus.go`
- `go-agent-v2/internal/bus/router.go`
- `go-agent-v2/internal/bus/resilient.go`
- `go-agent-v2/internal/bus/orchestration.go`
- 当前仓库前置骨架：`internal/bus/event_bus.go`

#### V3 目标文件结构
- `internal/platform/bus/module.go`
- `internal/platform/bus/bus.go`
- `internal/platform/bus/events.go`
- `internal/platform/bus/subscription.go`
- `internal/platform/bus/bridge_rpc.go`

#### 6 框架使用方式
- fx：提供单例 dispatcher 和 typed subscription helper。
- run.Group：仅当存在 bus-log sink 或 replay worker 时使用；bus 本身不是 actor。
- sqlc：不直接依赖；bus log sink 若持久化，依赖 store 接口。
- stateless：不直接使用。
- jrpc2：通过窄桥接把事件映射成通知，不耦合 handler。
- kelindar/event：直接承接；Typed Event + `Type() uint32` 取代 V2 `Msg*`/`Topic*`。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：typed event 定义、订阅取消、dispatcher lifecycle 属于平台层共享。
- Zone B（模块内 DRY）：业务事件命名与 payload 仍归各 module。
- Rule of Two 检查：若某个 event projector 只服务一个模块，不允许塞进 platform/bus。

#### 关键迁移风险
- event 类型数量会增长，必须有 domain 编码规则。
- 慢消费者处理必须显式，不允许重新回退到 drop-on-full。
- bus log 持久化如果放错层，会把 platform/bus 再次绑回 store 细节。

#### 代码量预估
- V2：约 `1500-2000` 行。
- V3：约 `350-500` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 6 个 |
| 包有效行数 | ≤ 800 行 |

**专项守卫：**
- 事件映射守卫：typed event `Type()`、`Route()`、payload shape 和桥接逻辑必须固定。
- 并发守卫：publish、subscribe、unsubscribe、close 的并发行为必须显式验证。
- 错误路径守卫：慢消费者、bridge 失败、取消订阅后的残留消费都必须被覆盖。
- 依赖方向守卫：`platform/bus` 只承接分发和桥接，不得 import 业务模块实现。
- `map[string]any` 禁止守卫：生产事件体不得回退到非 typed payload。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 13. platform/statemachine

#### 迁移来源（V2）
- `go-agent-v2/internal/runner/manager.go`
- `go-agent-v2/internal/runner/manager_event.go`
- `go-agent-v2/internal/runner/manager_auto_recover.go`
- `go-agent-v2/internal/guards/state_matrix_snapshot.json`
- 当前仓库前置骨架：`internal/runner/state_machine.go`

#### V3 目标文件结构
- `internal/platform/statemachine/module.go`
- `internal/platform/statemachine/factory.go`
- `internal/platform/statemachine/graph.go`
- `internal/platform/statemachine/matrix.go`

#### 6 框架使用方式
- fx：提供 factory、graph exporter、matrix test helper。
- run.Group：不直接使用；状态机宿主 service 由 `run.Group` 托管。
- sqlc：不使用。
- stateless：核心承接层；`FiringQueued`、guard、entry/exit action、graph export 都从这里统一提供。
- jrpc2：不使用。
- kelindar/event：通过 action 或 adapter 发布/订阅 typed event。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：factory、graph export、matrix harness 可以被 orchestration、ida、runner 复用。
- Zone B（模块内 DRY）：具体状态/触发器/guard 仍归对应模块 builder，不放进平台层。
- Rule of Two 检查：只有“状态机技术骨架”进入平台层；业务状态名和 trigger 不进入共享。

#### 关键迁移风险
- 双状态真相是 V2 最大问题；V3 必须保证只有一个 authoritative state。
- guard 非互斥会直接 panic。
- action 中写共享状态必须受控，否则只是把隐式状态搬到回调里。

#### 代码量预估
- V2：约 `1200-1800` 行决策/恢复相关逻辑。
- V3：约 `300-450` 行平台 factory，另加各模块 builder。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 6 个 |
| 包有效行数 | ≤ 600 行 |

**专项守卫：**
- `stateless` 模式守卫：统一使用 `FiringQueued`，禁止分叉运行模式。
- 全矩阵守卫：builder、graph export、matrix harness 必须同源。
- 业务枚举隔离守卫：`platform/statemachine` 不能 import `module/*`，也不能持有业务状态名。
- 协议守卫：graph/matrix 导出格式必须稳定，不能让文档图和代码图分叉。
- 错误路径守卫：guard false、非法 trigger、action failure 都必须有确定行为。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 14. platform/runner

#### 迁移来源（V2）
- `go-agent-v2/internal/runner/manager_lifecycle.go`
- `go-agent-v2/internal/runner/launch_facade.go`
- `go-agent-v2/internal/runner/provider_registry.go`
- `go-agent-v2/cmd/agent-terminal/app_helpers.go`
- 当前仓库前置骨架：`internal/app/runner.go`

#### V3 目标文件结构
- `internal/platform/runner/module.go`
- `internal/platform/runner/group.go`
- `internal/platform/runner/signal.go`
- `internal/platform/runner/lifecycle.go`

#### 6 框架使用方式
- fx：只负责收集 `group:"runners"` 并提供 `RunnerHost`。
- run.Group：核心承接层；信号、RPC server、watcher、provider session loop 都在这里编排。
- sqlc：不使用。
- stateless：不直接使用；runner 只是宿主。
- jrpc2：把 RPC server actor 加进 group。
- kelindar/event：run.Group interrupt 中负责取消订阅和收敛。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：actor 桥接、signal actor、top-level cancel 语义。
- Zone B（模块内 DRY）：业务 actor 自己实现 `Run(ctx)`，不要把业务恢复逻辑塞回 runner 宿主。
- Rule of Two 检查：只有通用 actor host 能放平台层；某模块专用 supervisor 留在模块。

#### 关键迁移风险
- 如果 `fx.OnStart` 又去起无限循环，runner 分层会立刻失效。
- interrupt 不能做复杂阻塞清理。
- group 退出原因必须被记录并可观测，否则停机诊断会回退到 V2 水平。

#### 代码量预估
- V2：约 `800-1200` 行宿主/停止路径。
- V3：约 `200-350` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 5 个 |
| 包有效行数 | ≤ 450 行 |

**专项守卫：**
- 并发守卫：一个 actor 返回错误后，其他 actor 必须收到取消并收敛退出。
- 错误路径守卫：interrupt、signal、shutdown 失败路径必须稳定，不允许二次阻塞。
- runner 注册守卫：长跑组件只能通过 `group:"runners"` 注入，不能在 `fx.OnStart` 裸起 goroutine。
- 依赖方向守卫：runner 只做宿主，不得吞入业务恢复和业务状态逻辑。
- `fx` import 范围守卫：`fx` 只允许出现在 `module.go` 和装配入口。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 15. platform/config

#### 迁移来源（V2）
- `go-agent-v2/internal/config/config.go`
- `go-agent-v2/internal/config/architecture.go`
- `go-agent-v2/internal/apiserver/*` 中散落的 timeout / env 使用点
- `go-agent-v2/internal/runner/*` 中散落的 recover / stream timeout

#### V3 目标文件结构
- `internal/platform/config/module.go`
- `internal/platform/config/config.go`
- `internal/platform/config/provider.go`
- `internal/platform/config/timeouts.go`
- `internal/platform/config/load.go`

#### 6 框架使用方式
- fx：提供统一 `Config`、`ProviderConfig`、`Timeouts`。
- run.Group：不使用。
- sqlc：不使用。
- stateless：不使用。
- jrpc2：给 middleware / request timeout 供配置切片。
- kelindar/event：不使用。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：所有 timeout、env key、默认值统一在这里。
- Zone B（模块内 DRY）：模块自己的派生默认值在模块内解释，不反向污染全局 config。
- Rule of Two 检查：只有全局、跨模块稳定配置才放平台层；一次性调试 flag 不进入正式 config。

#### 关键迁移风险
- V2 有 50+ 处显式 timeout 调用，迁移时最容易漏收敛。
- provider-specific 配置如果继续散落在 adapter/runner，会让 unified provider 失真。
- CWD/scope 类配置必须明确优先级，否则 dashboard/thread/workspace 会互相污染。

#### 代码量预估
- V2：约 `400-700` 行核心配置，外加大量散落常量。
- V3：约 `250-400` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 6 个 |
| 包有效行数 | ≤ 500 行 |

**专项守卫：**
- timeout locality 守卫：只有 `timeouts.go` 允许直接出现 `context.WithTimeout`。
- 协议守卫：`Config`、`ProviderConfig`、`Timeouts` 的字段和默认值必须稳定。
- 依赖方向守卫：平台配置不得反向依赖业务模块。
- `fx` import 范围守卫：`fx` 只允许出现在 `module.go`。
- 错误路径守卫：配置缺失、非法 env、派生默认值冲突必须给出可诊断错误。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

## Provider 层

### 16. provider/unified

#### 迁移来源（V2）
- `go-agent-v2/legacy-agentsdk/claude/*`
- `go-agent-v2/legacy-agentsdk/codex/*`
- `go-agent-v2/legacy-agentsdk/agentcore/*`
- `go-agent-v2/internal/apiserver/codexadapter/*`
- `go-agent-v2/internal/apiserver/commonadapter/*`
- `go-agent-v2/legacy-agentsdk/service/lifecycle/*`
- `go-agent-v2/legacy-agentsdk/service/runtime/*`
- 当前仓库前置骨架：`internal/provider/unified/client.go`、`internal/contract/provider.go`

#### V3 目标文件结构
- `internal/provider/unified/module.go`
- `internal/provider/unified/contract.go`
- `internal/provider/unified/session.go`
- `internal/provider/unified/turn.go`
- `internal/provider/unified/event_map.go`
- `internal/provider/unified/mcp_manifest.go`
- `internal/provider/unified/capabilities.go`
- `internal/provider/unified/thread_config.go`

#### 6 框架使用方式
- fx：`unified.Module` 提供 driver registry、session factory、capability mapper、manifest builder。
- run.Group：无模块级 actor；具体 driver 的 read loop 由 driver/session 自行封装。
- sqlc：间接依赖 thread config / binding / preference store 接口。
- stateless：不使用。
- jrpc2：不作为公共 server；只在 codex driver 内部使用其等价语义。
- kelindar/event：输出统一 provider event，供 turn/uistate/orchestration 消费。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：统一 `TurnRequest`、capability error、MCP manifest builder、thread config resolver。
- Zone B（模块内 DRY）：provider-neutral 语义聚合必须留在 unified，不回流到 driver 或 apiserver。
- Rule of Two 检查：只有真正 provider-neutral 的 helper 才能提升到 `platform/shared`。

#### 关键迁移风险
- 若统一层仍泄漏 `DynamicTools`、`SubmitWithSkillsAndOverrides` 之类旧接口名，迁移失败。
- capability fallback 策略若不稳定，会在 thread/turn 模块形成 provider 分支。
- manifest 生成必须是唯一工具接入路径，不能再允许 driver 自己加工具。

#### 代码量预估
- V2：约 `3500-4500` 行。
- V3：约 `1400-2000` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 10 个 |
| 包有效行数 | ≤ 2200 行 |

**专项守卫：**
- 响应守卫：统一 capability、manifest、thread config、session surface 的 DTO 不能漂移。
- 事件映射守卫：driver 事件到统一 provider event 的映射必须固定。
- 副作用守卫：session factory、capability fallback、manifest compose 不能产生双重决策。
- 错误路径守卫：unsupported capability、driver unavailable、fallback miss 必须返回稳定错误分类。
- 依赖方向守卫：统一层不得直接 import 具体 store 包实现。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 17. provider/claudecli

#### 迁移来源（V2）
- `go-agent-v2/legacy-agentsdk/claude/*`

#### V3 目标文件结构
- `internal/provider/claudecli/module.go`
- `internal/provider/claudecli/driver.go`
- `internal/provider/claudecli/transport.go`
- `internal/provider/claudecli/event_map.go`
- `internal/provider/claudecli/history.go`

#### 6 框架使用方式
- fx：`claudecli.Module` 提供 driver 和 transport 配置。
- run.Group：通常不直接注册公共 actor；CLI 进程读循环由 driver 内部托管。
- sqlc：不使用。
- stateless：不使用。
- jrpc2：不使用。
- kelindar/event：把 CLI 事件映射成统一 typed event 供 unified 层消费。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 provider/unified contract、platform/config timeout、platform/shared/retry。
- Zone B（模块内 DRY）：CLI spawn、NDJSON 读取、history 读取都留在 claude driver，不做跨 provider 共用。
- Rule of Two 检查：只有 transport-agnostic 的重试/退避 helper 才允许上提。

#### 关键迁移风险
- CLI stdout/stderr 解析与 session/thread ID 获取不稳定。
- Claude 特有的 resume/history 能力必须通过统一 contract 暴露，不能再回传 CLI 专有结构。
- 启停时的临时 MCP config 清理是资源泄漏风险。

#### 代码量预估
- V2：约 `1800-2500` 行。
- V3：约 `800-1100` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 6 个 |
| 包有效行数 | ≤ 1300 行 |

**专项守卫：**
- 事件映射守卫：CLI 流事件到统一 provider event 的映射必须冻结。
- 并发守卫：transport、history 读取、临时 MCP config 清理不能出现竞态。
- 错误路径守卫：spawn 失败、stdout/stderr 解析失败、history 恢复失败必须给出稳定分类。
- 依赖方向守卫：driver 不能直接 import `store/*`。
- timeout 散落守卫：驱动层不得自行发明超时常量。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 18. provider/codexapp

#### 迁移来源（V2）
- `go-agent-v2/legacy-agentsdk/codex/*`
- `go-agent-v2/internal/apiserver/codexadapter/*`

#### V3 目标文件结构
- `internal/provider/codexapp/module.go`
- `internal/provider/codexapp/driver.go`
- `internal/provider/codexapp/transport.go`
- `internal/provider/codexapp/event_map.go`
- `internal/provider/codexapp/recovery.go`
- `internal/provider/codexapp/history.go`

#### 6 框架使用方式
- fx：`codexapp.Module` 提供 driver、transport、history loader、recovery policy。
- run.Group：若把 app-server keepalive/health probe做成公共 supervisor，可注册一个 actor；否则由 session 内部管理。
- sqlc：不使用。
- stateless：不使用。
- jrpc2：只作为 app-server transport 协议语义，不对业务层暴露。
- kelindar/event：统一映射 app-server 事件，供 unified/turn/uistate 使用。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：复用 provider/unified contract、platform/config timeout、platform/shared/retry。
- Zone B（模块内 DRY）：JSON-RPC 协议、connection-dead 恢复、rollout history 读取都必须留在 codexapp。
- Rule of Two 检查：只有 transport 级重试策略才有上提资格；app-server health/reconnect 细节不能共享。

#### 关键迁移风险
- `connection dead` 与 stream retry 是 Codex 迁移的最大专属复杂度。
- `DynamicTools` 路径必须彻底删除，不能以兼容名保留。
- 历史读取和实时流混接时，最容易再次出现 thread truth 分叉。

#### 代码量预估
- V2：约 `4000-5500` 行。
- V3：约 `1400-2000` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 7 个 |
| 包有效行数 | ≤ 2200 行 |

**专项守卫：**
- 事件映射守卫：app-server 事件、history 事件、recovery 事件到统一 provider event 的映射必须固定。
- 并发守卫：read loop、connection dead 恢复、health probe 不能双重运行。
- 错误路径守卫：连接中断、重连失败、history 拼接失败必须保持 thread truth 一致。
- 依赖方向守卫：driver 不能直接 import `store/*`。
- timeout 散落守卫：transport/recovery 超时统一来自 `platform/config/timeouts.go`。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

## Store 层

### 19. store/*

#### 迁移来源（V2）
- `go-agent-v2/internal/store/agent_status.go`
- `go-agent-v2/internal/store/agent_thread.go`
- `go-agent-v2/internal/store/agent_thread_binding.go`
- `go-agent-v2/internal/store/agent_provider_binding.go`
- `go-agent-v2/internal/store/interaction.go`
- `go-agent-v2/internal/store/task_trace.go`
- `go-agent-v2/internal/store/task_ack.go`
- `go-agent-v2/internal/store/task_dag.go`
- `go-agent-v2/internal/store/task_dag_phase1.go`
- `go-agent-v2/internal/store/workspace_run.go`
- `go-agent-v2/internal/store/shared_file.go`
- `go-agent-v2/internal/store/prompt_template.go`
- `go-agent-v2/internal/store/command_card.go`
- `go-agent-v2/internal/store/audit_log.go`
- `go-agent-v2/internal/store/system_log.go`
- `go-agent-v2/internal/store/ai_log.go`
- `go-agent-v2/internal/store/bus_log.go`
- `go-agent-v2/internal/store/topology_approval.go`
- `go-agent-v2/internal/store/cwd_lock.go`
- `go-agent-v2/internal/store/ui_preference.go`
- 例外：`go-agent-v2/internal/store/db_query.go`
- 对应 schema：`go-agent-v2/migrations/*.sql`

#### V3 目标文件结构
- `internal/store/sqlc/*`：生成代码，只读。
- `sql/queries/*.sql`：按表或域拆分 query。
- `internal/store/<name>/module.go`
- `internal/store/<name>/contract.go`
- `internal/store/<name>/store.go`
- `internal/store/rawquery/`：承接 `DBQueryStore` 例外，命名建议 `ReadOnlySQLExecutor`。

#### 6 框架使用方式
- fx：每个 store 导出自己的 `Module`，`platform/db` 提供 `*pgxpool.Pool`、`*sqlc.Queries`、`sqlc.Querier`。
- run.Group：不使用。
- sqlc：直接核心承接；静态查询全部迁入 `sql/queries/*.sql`，事务用 `Queries.WithTx`。
- stateless：不使用。
- jrpc2：不使用。
- kelindar/event：store 层不主动发业务事件；事件在 module 层发布，store 只负责持久化。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：`platform/db` 提供 pool/tx/query guard；`platform/shared` 只允许技术性 helper。
- Zone B（模块内 DRY）：每个 store 自己的 query wrapper、scan/aggregate 逻辑只留在该 store。
- Rule of Two 检查：只有分页、cursor、JSON 编码、tx helper 这类纯技术逻辑才可上提；领域查询绝不共享。

#### 19 个 Store 的 sqlc 迁移概要

| V2 Store | 主要表 / 迁移 | V3 目标 |
| --- | --- | --- |
| `AgentStatusStore` | `agent_status` / `0006_agent_status.sql` | `store/agentstatus` + `sql/queries/agent_status.sql` |
| `AgentThreadStore` | `agent_threads` / `0012_agent_threads.sql` | `store/thread` + `sql/queries/agent_thread.sql` |
| `AgentThreadBindingStore` | `agent_codex_binding` / `0013_*` `0018_*` | 合并进 `store/threadbinding` |
| `AgentProviderBindingStore` | `agent_provider_binding` / `0021_*` `0022_*` | `store/providerbinding` |
| `InteractionStore` | `agent_interactions` / `0001_initial_schema.sql` | `store/interaction` |
| `TaskTraceStore` | `task_traces` / `0003_task_trace_prompt_versions.sql` | `store/tasktrace` |
| `TaskAckStore` | `task_acks` / `0004_ack_dag.sql` | `store/taskack` |
| `TaskDAGStore` | `task_dags` `task_dag_nodes` `task_dag_wakeups` `task_dag_worker_leases` / `0004_ack_dag.sql` `0023_dag_watcher_phase1.sql` | `store/taskdag` |
| `WorkspaceRunStore` | `workspace_runs` `workspace_run_files` / `0006_workspace_runs.sql` | `store/workspace` |
| `SharedFileStore` | `shared_files` / `0001_initial_schema.sql` | `store/sharedfile` |
| `PromptTemplateStore` | `prompt_templates` `prompt_versions` / `0001_*` `0003_*` | `store/prompt` |
| `CommandCardStore` | `command_cards` `command_card_versions` `command_card_runs` / `0001_*` `0005_*` | `store/commandcard` |
| `AuditLogStore` | `audit_events` / `0001_initial_schema.sql` | `store/auditlog` |
| `SystemLogStore` | `system_logs` / `0001_*` `0009_*` | `store/systemlog` |
| `AILogStore` | 当前混在日志域 | `store/ailog` |
| `BusLogStore` | `bus_exception_logs` / `0007_bus_exception_logs.sql` | `store/buslog` |
| `TopologyApprovalStore` | `topology_approvals` `topology_approval_archives` / `0001_initial_schema.sql` | `store/topologyapproval` |
| `CwdLockStore` | `cwd_instance_locks` / `0019_cwd_instance_locks.sql` | `store/cwdlock` |
| `UIPreferenceStore` | `ui_preferences` / `0010_*` `0020_*` | `store/uipreference` |

#### 关键迁移风险
- 如果继续保留“19 个手写大 Store + 部分 sqlc”，迁移收益会被完全吃掉。
- query 文件命名和模块命名必须按领域拆分，否则生成层会重新变成巨石目录。
- `DBQueryStore` 是唯一允许的显式例外，必须被隔离命名，不能再冒充普通 store。

#### 代码量预估
- V2：约 `3100` 行手写 Store + `migrations/` SQL。
- V3：约 `1100-1500` 行手写 repo/store + `8000-10000` 行 sqlc 生成代码。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 6 个（每个手写 `store/<name>` 子包） |
| 包有效行数 | ≤ 700 行（每个手写 `store/<name>` 子包；`internal/store/sqlc/` 生成物除外） |

**专项守卫：**
- D2 副作用守卫：upsert、audit、trace、workspace run 等持久化语义必须冻结。
- D5 协议守卫：schema、constructor、interface、一致性、export surface 不能回归到手写巨石 store。
- D6 并发守卫：并发事务、lease、锁冲突路径必须有专门守卫。
- D7 错误路径守卫：zero-row、not found、tx rollback、raw query 失败必须稳定。
- `sqlc` 边界守卫：生成目录只读，且只允许被 `store/*` import。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

## Tool 层

### 20. tool/lsp

#### 迁移来源（V2）
- `go-agent-v2/pkg/toolsdk/lsp/*`
- `go-agent-v2/pkg/toolsdk/tooladapter/lsp_tool_meta.go`
- `go-agent-v2/internal/apiserver` 中与 LSP 相关的 wrapper / diagnostics 查询

#### V3 目标文件结构
- `internal/tool/lsp/module.go`
- `internal/tool/lsp/manager.go`
- `internal/tool/lsp/bootstrap.go`
- `internal/tool/lsp/file.go`
- `internal/tool/lsp/search.go`
- `internal/tool/lsp/inspect.go`
- `internal/tool/lsp/xref.go`
- `internal/tool/lsp/structure.go`
- `internal/tool/lsp/edit.go`
- `internal/tool/lsp/replace_range.go`
- `internal/tool/lsp/display.go`

#### 6 框架使用方式
- fx：`lsp.Module` 提供 manager、cache、tool set。
- run.Group：无顶层 actor；语言服务器进程由 manager 按需拉起和关闭。
- sqlc：不使用。
- stateless：不使用。
- jrpc2：不使用。
- kelindar/event：发布 `LSPDiagnosticsUpdated`、`LSPServerStarted`，订阅 workspace merge / cwd switch 事件做 cache reset。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：结果截断、路径 scope、request budget 可以复用 `platform/shared`。
- Zone B（模块内 DRY）：action dispatch、display formatting、replace-range 语义都留在 tool/lsp 内部。
- Rule of Two 检查：只有“通用路径/输出 budget”能上提；LSP 专属协议逻辑不能共享。

#### 关键迁移风险
- LSP 工具面大且细节多，是 V3 保留真实复杂度最多的域之一。
- 多工作区 root、缓存、bootstrap、edit correctness 任何一项处理不严都会造成错误结果。
- 若 registry 继续把 schema 与 dispatch 耦成一体，family 拆分会失败。

#### 代码量预估
- V2：约 `4000-5000` 行。
- V3：约 `2300-3000` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 12 个 |
| 包有效行数 | ≤ 3000 行（迁移期显式例外） |

**专项守卫：**
- 响应守卫：file/search/inspect/xref/structure/edit/display 各 action 的返回 shape 固定。
- 协议守卫：LSP request/response envelope、replace-range 语义、display 格式不能漂移。
- 生命周期守卫：server bootstrap、workspace root、cache reset 的状态迁移必须稳定。
- 并发守卫：多 workspace、多 server、多 edit 请求下不能出现共享状态竞争。
- 错误路径守卫：bootstrap 失败、diagnostics 失败、edit 回滚失败必须给出稳定错误 envelope。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 21. tool/code

#### 迁移来源（V2）
- `go-agent-v2/pkg/toolsdk/tools/code_run.go`
- `go-agent-v2/internal/executor/code_runner.go`
- `go-agent-v2/internal/mcp/runtime.go`

#### V3 目标文件结构
- `internal/tool/code/module.go`
- `internal/tool/code/runner.go`
- `internal/tool/code/audit.go`

#### 6 框架使用方式
- fx：`code.Module` 只提供 wrapper-only tool definitions，注入 `module/coderun` facade；运行策略与审计策略不在 `tool/code` 重复实现。
- run.Group：不使用。
- sqlc：不直接使用。
- stateless：不使用。
- jrpc2：不使用。
- kelindar/event：发布 code run lifecycle 事件，供 uistate/dashboard 消费。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：危险命令识别、审计截断若被 ida 复用才允许上提。
- Zone B（模块内 DRY）：`tool/code` 只保留 schema、参数 envelope、结果归一化；真正的 mode 解析与 command runner 策略留在 `module/coderun`。
- Rule of Two 检查：在 coderun 之外没有第二个稳定消费者前，不上提任何运行器细节。

#### 关键迁移风险
- 输出截断、安全审计、workdir 校验三者必须保持同一 contract。
- tool/code 与 module/coderun 的边界要清楚：前者是 wrapper-only，后者负责编排、运行和审计策略。

#### 代码量预估
- V2：约 `700-900` 行。
- V3：约 `350-500` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 5 个 |
| 包有效行数 | ≤ 700 行 |

**专项守卫：**
- 响应守卫：wrapper-only 参数 envelope、mode 解析结果、输出截断格式必须固定。
- 副作用守卫：审计落盘、执行策略选择、workdir 归一化必须委托给 `module/coderun`，不能出现第二份业务语义。
- 错误路径守卫：dangerous command、timeout、permission denied、sandbox failure 必须统一成稳定错误 envelope。
- timeout 散落守卫：工具层不得自行创建新的 timeout 常量。
- 依赖方向守卫：`tool/code` 只依赖 facade，不直接下钻 store/UI。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 22. tool/orchestration

#### 迁移来源（V2）
- `go-agent-v2/pkg/toolsdk/tools/orchestration.go`
- `go-agent-v2/pkg/toolsdk/tools/resource_dag.go`
- `go-agent-v2/internal/mcp/resource_adapters.go`
- `go-agent-v2/internal/apiserver/tool_provider_adapters.go`

#### V3 目标文件结构
- `internal/tool/orchestration/module.go`
- `internal/tool/orchestration/agent.go`
- `internal/tool/orchestration/resource_dag.go`
- `internal/tool/orchestration/workspace.go`

#### 6 框架使用方式
- fx：`tool/orchestration.Module` 只提供 wrapper-only tool set，并注入 `module/orchestration`、`module/workspace` facade。
- run.Group：不使用。
- sqlc：不直接使用。
- stateless：不直接使用。
- jrpc2：不使用。
- kelindar/event：一般只消费 service 输出，不直接发业务事件。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：tool schema builder、scoped DAG key helper 若已稳定复用，可留在 registry/shared。
- Zone B（模块内 DRY）：`tool/orchestration` 只保留参数解析、scope 校验、错误 envelope；DAG/workspace 业务规则全部留在 module facade。
- Rule of Two 检查：不要把 DAG/workspace 业务校验错误地上提到 tool registry。

#### 关键迁移风险
- 同一动作同时存在 RPC 面和 MCP tool 面，最容易出现双重校验和双重命名。
- DAG / workspace / agent orchestration 在 V2 已混在一个工具包里，V3 必须按 facade 明确依赖方向；`tool/orchestration` 不能再拥有第二份业务语义。

#### 代码量预估
- V2：约 `2000-2600` 行。
- V3：约 `600-900` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 6 个 |
| 包有效行数 | ≤ 1000 行 |

**专项守卫：**
- 响应守卫：DAG、workspace、agent orchestration tool 的参数和结果 envelope 必须冻结。
- 副作用守卫：wrapper 只做参数解析和 scope 校验，不得复制 DAG/workspace 业务逻辑。
- 错误路径守卫：scope 错误、依赖未满足、merge 失败都必须透传稳定错误 envelope。
- 依赖方向守卫：`tool/orchestration` 只能依赖 facade，不能反向持有 store 或 runner 细节。
- family 边界守卫：orchestration tool 不能偷带 LSP/IDA family 专属依赖。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 23. tool/ida

#### 迁移来源（V2）
- `go-agent-v2/pkg/toolsdk/tools/ida_tools*.go`
- `go-agent-v2/pkg/idamcp/*`
- `go-agent-v2/internal/mcp/runtime_ida.go`

#### V3 目标文件结构
- `internal/tool/ida/module.go`
- `internal/tool/ida/runtime.go`
- `internal/tool/ida/tools.go`
- `internal/tool/ida/schemas.go`

#### 6 框架使用方式
- fx：`tool/ida.Module` 只提供 wrapper-only IDA tool set 和 schema exporter；生命周期与 gateway 统一由 `module/ida` 承接。
- run.Group：仅当 runtime 需要长期轮询 worker 时使用；纯 tool schema 本身不需要。
- sqlc：不使用。
- stateless：不直接使用。
- jrpc2：不使用。
- kelindar/event：通常不直接发业务事件，由 `module/ida` 统一发布。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：tool envelope、schema availability、error mapping 可与 registry 共享。
- Zone B（模块内 DRY）：`tool/ida` 只保留 IDA 专属工具名、参数和 envelope 归一化；lease、gateway、workspace materialize 全部留在 `module/ida`。
- Rule of Two 检查：只有通用 tool schema 逻辑能上提，IDA 工具语义绝不共享。

#### 关键迁移风险
- IDA tool 数量多且平台特异性强，schema 变更成本高。
- V2 把 management、forwarded、debug 工具混在一起，V3 必须按生命周期与 surface 分层；`tool/ida` 不能回吸 gateway 逻辑。

#### 代码量预估
- V2：约 `2500-3500` 行工具面代码。
- V3：约 `900-1300` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 7 个 |
| 包有效行数 | ≤ 1500 行 |

**专项守卫：**
- 协议守卫：IDA tool schema、availability、tool envelope 和 schema exporter 必须冻结。
- 事件映射守卫：tool runtime 和 gateway lifecycle 之间的事件桥接必须固定。
- 错误路径守卫：gateway not ready、lease denied、debug attach fail、workspace materialize fail 必须稳定。
- family 隔离守卫：`tool/ida` 不得 import LSP 或 orchestration family。
- 依赖方向守卫：工具层只保留 wrapper/schemas，不回吸 gateway 业务逻辑。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

### 24. tool/registry

#### 迁移来源（V2）
- `go-agent-v2/pkg/toolsdk/tooladapter/registry.go`
- `go-agent-v2/pkg/toolsdk/tooladapter/dispatch*.go`
- `go-agent-v2/pkg/toolsdk/tooladapter/lsp_tool_meta.go`
- `go-agent-v2/internal/mcp/runtime.go`

#### V3 目标文件结构
- `internal/tool/registry/module.go`
- `internal/tool/registry/registry.go`
- `internal/tool/registry/schemas.go`
- `internal/tool/registry/availability.go`

#### 6 框架使用方式
- fx：收集各 family 的 tool provider，生成统一 registry。
- run.Group：不使用。
- sqlc：不使用。
- stateless：不使用。
- jrpc2：不使用。
- kelindar/event：不使用。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：schema list、availability gating、tool family compose 是 registry 的共享职责。
- Zone B（模块内 DRY）：family-specific dispatch 仍在各自 `tool/*` 包内，不塞回 registry。
- Rule of Two 检查：registry 只承接 schema/lookup/availability；一旦开始承担业务 facade，就应拆回具体 tool 包。

#### 关键迁移风险
- registry 很容易取代 `apiserver.Server` 成为新的神对象。
- family split 完成前，旧的“全量 schema 一次注册”惯性会不断回流。
- availability 逻辑若混入业务状态，会破坏 tool family 独立编译目标。

#### 代码量预估
- V2：约 `1200-1800` 行。
- V3：约 `550-850` 行。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | ≤ 5 个 |
| 包有效行数 | ≤ 900 行 |

**专项守卫：**
- 协议守卫：schema list、availability gating、family compose 结果必须冻结。
- 错误路径守卫：未知工具、禁用工具、schema 缺失必须返回稳定错误。
- family 隔离守卫：registry 只做查找和组合，不允许内嵌具体 family 业务逻辑。
- 依赖方向守卫：`tool/registry` 不能演化成第二个 `apiserver.Server`。
- 导出面守卫：对外只暴露 registry contract，不暴露具体 family 实现。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

## MCP Server 层

### 25. mcpserver/common + cmd/mcp-lsp + mcpserver/orch + mcpserver/ida

#### 迁移来源（V2）
- `go-agent-v2/internal/mcp/server.go`
- `go-agent-v2/internal/mcp/runtime.go`
- `go-agent-v2/internal/mcp/stdio.go`
- `go-agent-v2/internal/mcp/resource_adapters.go`
- `go-agent-v2/internal/mcp/runtime_ida.go`
- `go-agent-v2/cmd/mcp-server/main.go`

#### V3 目标文件结构
- `internal/mcpserver/common/module.go`
- `internal/mcpserver/common/runtime.go`
- `internal/mcpserver/common/stdio.go`
- `internal/mcpserver/common/manifest.go`
- `cmd/mcp-lsp/main.go`
- `cmd/mcp-lsp/fx.go`
- `cmd/mcp-lsp/runtime.go`
- `cmd/mcp-lsp/http_runner.go`
- `cmd/mcp-lsp/schema.go`
- `internal/sidecar/lsp/tools.go`
- `internal/mcpserver/orch/module.go`
- `internal/mcpserver/orch/tools.go`
- `internal/mcpserver/ida/module.go`
- `internal/mcpserver/ida/tools.go`
- `cmd/mcp-orch/*`
- `cmd/mcp-ida/*`

#### 6 框架使用方式
- fx：每个 family binary 用独立 `fx` 图装配自己的 tool 集合；`common` 只承接 stdio runtime 和 manifest。
- run.Group：standalone MCP binary 通常不需要多 actor；如有 background subscription/reaper，可在 common runtime 下接入。
- sqlc：仅 orch family 通过 module/store 间接使用。
- stateless：不直接使用。
- jrpc2：不直接使用；MCP runtime 是另一套协议面。
- kelindar/event：orch family 可订阅 bus 事件；common runtime 不直接持有业务事件。

#### Two-Zone DRY 落地
- Zone A（跨模块共享）：`common` 提供 stdio framing、manifest 输出、工具调用分发壳。
- Zone B（模块内 DRY）：family-specific tool list、schema snapshot、依赖装配分别留在 `lsp` / `orch` / `ida`。
- Rule of Two 检查：只有真正 family-agnostic 的 runtime 代码留在 `common`；任何工具族专属依赖一律下沉。

#### 关键迁移风险
- 如果 `common` 重新吸收 family-specific 依赖，三二进制拆分会失效。
- manifest/tool list 与 registry 的边界必须一致，否则 provider 看到的工具面和二进制实际能力会漂移。
- stdio framing、schema snapshot、binary build smoke 都必须分家验证。

#### 代码量预估
- V2：约 `1800-2500` 行。
- V3：约 `1000-1500` 行，分散到四个包和三个二进制入口。

#### 代码守卫

| 守卫维度 | 约束 |
|---|---|
| 单文件行数 | ≤ 400 行（无例外） |
| 单函数行数 | ≤ 80 行（无例外） |
| 嵌套深度 | ≤ 4 层 |
| 圈复杂度 | ≤ 10 |
| 包内文件数 | `common` ≤ 6 个；单个 family 包 ≤ 3 个 |
| 包有效行数 | `common` ≤ 800 行；单个 family 包 ≤ 400 行 |

**专项守卫：**
- 协议守卫：stdio framing、manifest、tool list、schema snapshot 必须冻结。
- 并发守卫：runtime、subscription、reaper、stdio 读写不能出现竞态和交叉阻塞。
- 错误路径守卫：malformed frame、unknown method、tool failure、binary build failure 都必须稳定可诊断。
- MCP 家族交叉 import 守卫：`lsp`、`orch`、`ida` 三个 family 二进制互不 import。
- 依赖方向守卫：`common` 只保留 family-agnostic runtime，不得回吸具体工具族依赖。

**拆分触发规则：**
- 文件超过 300 行时必须主动拆分，不等到触碰 400 行上限。
- 函数超过 60 行时应考虑提取子函数。

## 代码量汇总表

> 去重规则：
> - `tool/code`、`tool/orchestration`、`tool/ida` 为 wrapper-only，不再把与 `module/coderun`、`module/orchestration`、`module/ida` 重叠的 V2 业务逻辑重复累计。
> - `task_dag_phase1` 并入 `store/taskdag`，不再单列 Store。
> - 下表以 V3 手写核心代码量为主；V2 去重后的总体对照口径仍以主文档的域级统计为准。

| 条目 | V3 手写估算 |
| --- | ---: |
| `module/thread` | `700-1000` |
| `module/turn` | `900-1300` |
| `module/skill` | `600-900` |
| `module/orchestration` | `2200-3200` |
| `module/workspace` | `700-1000` |
| `module/uistate` | `1200-1800` |
| `module/coderun` | `500-700` |
| `module/ida` | `2000-3000` |
| `module/dashboard` | `700-900` |
| `platform/rpc` | `900-1300` |
| `platform/db` | `350-500` |
| `platform/bus` | `350-500` |
| `platform/statemachine` | `300-450` |
| `platform/runner` | `200-350` |
| `platform/config` | `250-400` |
| `provider/unified` | `1400-2000` |
| `provider/claudecli` | `800-1100` |
| `provider/codexapp` | `1400-2000` |
| `store/*` | `1100-1500` |
| `tool/lsp` | `2300-3000` |
| `tool/code` | `350-500` |
| `tool/orchestration` | `600-900` |
| `tool/ida` | `900-1300` |
| `tool/registry` | `550-850` |
| `mcpserver/*` | `1000-1500` |
| `25 个迁移条目小计` | `22250-31950` |
| `未逐项展开的 app/ui/cmd/contract/dto/platform/shared/archtest` | `7750-8050` |
| `手写核心总计` | `30000-40000` |
| `sqlc 生成代码` | `8000-10000` |
