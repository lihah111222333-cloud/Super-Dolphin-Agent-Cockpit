# Phase 7 — Hook 注入 + 记忆读取工具 + 迁移兼容

> 预计：0.5-1 天 | 依赖：Phase 1, Phase 2, Phase 3

## 目标

- 建立 Hook 驱动的记忆主链：启动注入、回合结束自动写盘、会话结束预留蒸馏入口。
- MCP 面只保留 `memory_read`；子 Agent 需要时主动查阅记忆，不再公开 `memory_write/search/list/forget`。
- 用户通过 slash command 查看/删除记忆；保存动作由 hook + 意图检测自动完成。
- 保留 `shared_file_read/write` 兼容语义，并保留 `shared_files → 磁盘记忆` 的迁移能力。

## 7.1 Hook 注入（自动化）

- `thread/start` hook：加载 `MEMORY.md` + scope 内 memory 文件，并交给 `PromptAssemblyService.AssembleStart()` 统一装配；主线程与子 Agent 都走同一注入入口。
- `turn/end` hook：执行保存意图检测；命中后调用 `extractMemory()`，完成 topic file 写盘、`MEMORY.md`/索引更新与必要审计。
- `session/stop` hook：本 Phase 只预留扩展点，不默认执行后台任务；`extractMemories` / `autoDream` 已转记到 `p18-unimplemented.md`。
- Hook 写入链路必须复用 Phase 1 / Phase 5 的路径解析、scope 约束与并发锁，禁止新增旁路写入入口。
- 公开 MCP 面不再承担写入、搜索、列举、删除职责，避免“工具写盘”和“hook 写盘”双主链并存。

## 7.2 `memory_read` 工具（唯一 MCP 工具）

- 仅供子 Agent 或 debug 链路主动读取指定 `scope/path/name/type` 的记忆。
- 统一流程：`sanitize → resolve → authorize → read`；`path` 仅限 debug/内部场景，但仍走同一授权链。
- 工具只读不写，不触发迁移、索引重建、forget、写盘或自动提取副作用。
- 返回建议保持 `{entry, sourcePath, indexHit}`；索引损坏时允许 `degraded=true / source=rebuilt_view` 的只读降级语义。
- `cmd/mcp-orch/tools/memory_tools.go` 只保留 `memory_read` 的 schema/handler；`memory_write/search/list/forget` 代码与 schema 一律清理。

## 7.3 用户交互

- `/memory`：查看当前线程/作用域可见记忆，由 slash command 走 skill 框架。
- `/forget`：删除指定记忆，由 slash command 走 skill 框架；删除后同步更新 topic file / `MEMORY.md` / 索引。
- 保存长期记忆不再通过 MCP tool 显式调用；自然语言“记住这个”一类请求由 `turn/end` hook 的意图检测自动落盘。
- slash command 与 hook 必须复用同一 authorizer / path policy / audit policy，避免权限漂移。

## 7.4 种子数据迁移

- 保留 `shared_files → 磁盘记忆` 的迁移策略，提供 dry-run / apply / rollback manifest。
- 迁移输出仍写入标准 topic file + `MEMORY.md` 索引，不保留“双写 shared_files 作为主存储”。
- 迁移脚本必须幂等，并输出 `source_file / candidate_count / created_count / updated_count / skipped_count / skipped_by_reason`。
- `shared_files` 在迁移后继续承担 agent 间实时协作；memory 目录承担跨会话长期记忆。

## 兼容与发布

- `shared_file_read/write` 保持 V2 兼容：path canonicalization、`file <path> not found`、固定 `agent` actor、10 MiB 上限。
- rollout flags 至少覆盖 `enable_memory_system`、`enable_prompt_assembly`、`enable_memory_tools`；关闭后停止新注入/新读取/新写盘，但不破坏既有 `shared_files` 协作链路。
- Phase 7 工期调整为 **0.5-1 天**：MCP 面缩小，但仍需串接 `thread/start`、`turn/end` 与 slash command 的边界。

## 任务清单

- [ ] `thread/start` hook：接入记忆加载，并通过 `PromptAssemblyService.AssembleStart()` 注入
- [ ] `turn/end` hook：接入保存意图检测、`extractMemory()`、写盘与索引更新
- [ ] `cmd/mcp-orch/tools/memory_tools.go`：只保留 `memory_read` schema/handler
- [ ] slash command：补齐 `/memory` 与 `/forget` 的 skill 路由
- [ ] `shared_files → 磁盘记忆` 迁移脚本与报告
- [ ] `shared_file_read/write` 兼容校验 + kill switch 接入

## 验收条件

- `thread/start` hook 能加载 `MEMORY.md` + topic files，并经 `PromptAssemblyService.AssembleStart()` 完成注入
- `turn/end` hook 在命中保存意图时可自动写盘，并同步更新 `MEMORY.md` / 索引，且无半写状态
- `session/stop` hook 仅保留未实现扩展点（详见 `p18-unimplemented.md`），不影响当前主链稳定性
- `cmd/mcp-orch` 只注册 `memory_read`；`memory_write/search/list/forget` 不再出现在工具清单或实现代码中
- `memory_read` 统一走 `sanitize → resolve → authorize`，只读、无副作用、支持 deny/degraded 语义
- `/memory` 与 `/forget` 经 skill/slash command 可用，并复用同一权限与审计策略
- `shared_file_read/write` 与 V2 兼容，`shared_files → 磁盘记忆` 迁移支持 dry-run / apply / rollback manifest
- flags 关闭时新链路立即停用，既有 `shared_files` 协作不受影响
