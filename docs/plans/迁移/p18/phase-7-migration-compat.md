# Phase 7 — Hook 注入 + 记忆读取工具 + 迁移兼容

> 预计：0.5-1 天 | 依赖：Phase 1, Phase 2, Phase 3

## 目标

- 采用三路径记忆写入策略：显式保存 0 token、`/dream` 按需蒸馏 1 次 LLM、定时蒸馏按需触发 1 次 LLM。
- `thread/start` 继续承担记忆加载职责；写盘主链不再要求“每个会话都跑一次 LLM”。
- MCP 面只保留 `memory_read`；查看 / 删除 / 蒸馏通过用户命令完成，写入由 hook 或 slash command 统一落盘。
- 保留 `shared_file_read/write` 兼容语义，并保留 `shared_files → 磁盘记忆` 的迁移能力。

## 7.1 `thread/start` hook — 自动加载记忆

- 启动时加载 `MEMORY.md`，并通过 `PromptAssemblyService` 注入到启动 Prompt。
- 现有 `DynamicSectionProvider` 继续作为统一装配入口，主线程与子 Agent 走同一条加载链路。
- 该路径只负责“读记忆”，不做写盘、不做蒸馏、不引入额外 LLM 调用。

## 7.2 `turn/end` hook — 显式保存（不需要 LLM）

- `turn/end` 仅做**简单意图匹配**：检测“记住这个”“记住了”“save to memory”“remember this”等显式保存模式。
- 命中后直接提取内容，调用 `DiskStore.Create()` / 索引更新完成写盘。
- 未命中则直接 pass，保持 0 开销，不做后台分析。
- Hook 写入链路必须复用既有路径解析、scope 约束、并发锁与索引更新逻辑，禁止新增旁路写入入口。

## 7.3 `/dream` slash command — 按需蒸馏（需要 LLM）

- 仅在用户主动触发 `/dream` 时执行一次蒸馏。
- 通过 provider sideQuery 分析对话历史，提取值得长期保存的条目。
- 蒸馏结果按 `user / feedback / project / reference` 四种 `MemoryType` 分类写入磁盘。
- `/dream` 与定时蒸馏必须复用同一份抽取 / 归类 / 写盘逻辑，避免两套蒸馏实现漂移。

## 7.4 定时蒸馏（预留，可后续配置）

- 触发方式预留为 cron 或“对话轮数阈值”两类策略。
- 触发后与 `/dream` 共用同一蒸馏逻辑，只额外负责调度与节流。
- 默认关闭；用户或部署配置明确开启后才执行。
- 本 Phase 先留扩展点，不要求默认后台任务常驻运行。

## 7.5 `memory_read` MCP 工具（唯一保留的工具）

- 仅供子 Agent 或 debug 链路主动读取指定 `scope/path/name/type` 的记忆。
- 统一流程：`sanitize → resolve → authorize → read`；`path` 仅限 debug/内部场景，但仍走同一授权链。
- 工具只读不写，不触发迁移、索引重建、forget、写盘或自动提取副作用。
- 返回建议保持 `{entry, sourcePath, indexHit}`；索引损坏时允许 `degraded=true / source=rebuilt_view` 的只读降级语义。
- `internal/sidecar/orch/tools/memory_tools.go` 只保留 `memory_read` 的 schema/handler；`memory_write/search/list/forget` 代码与 schema 一律清理。

## 7.6 用户交互

- `/memory`：查看当前线程 / 作用域可见记忆。
- `/forget`：删除指定记忆，并同步更新 topic file / `MEMORY.md` / 索引。
- `/dream`：对当前累积对话执行一次按需蒸馏。
- 显式长期保存不再依赖 MCP 写工具；自然语言“记住这个”走 `turn/end` hook。
- slash command 与 hook 必须复用同一 authorizer / path policy / audit policy，避免权限漂移。

## 7.7 种子数据迁移

- 保留 `shared_files → 磁盘记忆` 的迁移策略，提供 dry-run / apply / rollback manifest。
- 迁移输出仍写入标准 topic file + `MEMORY.md` 索引，不保留“双写 shared_files 作为主存储”。
- 迁移脚本必须幂等，并输出 `source_file / candidate_count / created_count / updated_count / skipped_count / skipped_by_reason`。
- `shared_files` 在迁移后继续承担 agent 间实时协作；memory 目录承担跨会话长期记忆。

## 兼容与发布

- `shared_file_read/write` 保持 V2 兼容：path canonicalization、`file <path> not found`、固定 `agent` actor、10 MiB 上限。
- rollout flags 至少覆盖 `enable_memory_system`、`enable_prompt_assembly`、`enable_memory_tools`；关闭后停止新注入/新读取/新写盘，但不破坏既有 `shared_files` 协作链路。
- Phase 7 工期调整为 **0.5-1 天**：MCP 面缩小，但仍需串接 `thread/start`、`turn/end`、`/dream` 与迁移兼容边界。

## 任务清单

- [ ] `thread/start` hook：接入 `MEMORY.md` 加载，并通过 `PromptAssemblyService` 注入
- [ ] `turn/end` hook：接入显式保存意图检测，命中后直接写盘并更新索引
- [ ] `/dream`：接入 sideQuery 蒸馏与分类写盘
- [ ] 定时蒸馏：预留 cron / 阈值配置入口，默认关闭
- [ ] `internal/sidecar/orch/tools/memory_tools.go`：只保留 `memory_read` schema/handler
- [ ] slash command：补齐 `/memory`、`/forget`、`/dream` 的路由
- [ ] `shared_files → 磁盘记忆` 迁移脚本与报告
- [ ] `shared_file_read/write` 兼容校验 + kill switch 接入

## 验收条件

- `thread/start` hook 能加载 `MEMORY.md`，并经 `PromptAssemblyService` 完成注入
- `turn/end` hook 在命中显式保存意图时可直接写盘，并同步更新 `MEMORY.md` / 索引，未命中时保持 0 LLM / 0 额外开销
- `/dream` 仅在用户主动触发时调用一次 LLM，对话蒸馏结果可按四种 `MemoryType` 分类写盘
- 定时蒸馏默认关闭，但配置开启后可复用 `/dream` 的蒸馏链路
- `cmd/mcp-orch` 只注册 `memory_read`；`memory_write/search/list/forget` 不再出现在工具清单或实现代码中
- `memory_read` 统一走 `sanitize → resolve → authorize`，只读、无副作用、支持 deny / degraded 语义
- `/memory`、`/forget`、`/dream` 经 slash command 可用，并复用同一权限与审计策略
- `shared_file_read/write` 与 V2 兼容，`shared_files → 磁盘记忆` 迁移支持 dry-run / apply / rollback manifest
- flags 关闭时新链路立即停用，既有 `shared_files` 协作不受影响
