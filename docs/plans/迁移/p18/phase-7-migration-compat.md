# P18 Phase 7：迁移工具 + 兼容层

> 预计：1 天 | 依赖：Phase 1, Phase 2

## 目标
提供 Memory MCP 工具 + 现有数据迁移脚本。

## Memory MCP 工具

| 工具 | 功能 |
|------|------|
| `memory_read` | 按名称/路径读取记忆 |
| `memory_write` | **upsert** 语义：按 `name` 唯一键匹配（大小写不敏感），已有则更新，否则新建（type 校验 + frontmatter + 索引） |
| `memory_search` | 搜索记忆（keyword 匹配 description/content，支持 type filter、limit、fail-soft） |
| `memory_list` | 列出 MEMORY.md 索引 |
| `memory_forget` | 删除指定记忆（topic file + 索引项） |

## 覆盖边界（Claude 对齐口径）

- 这 5 个**公开** MCP 工具只覆盖 topic memory 的显式 CRUD / search / list 操作，不等于 Claude 完整 memory 运行面。
- 不纳入默认公开 MCP 面、但实施期必须落地的内部能力：`RebuildMemoryIndex()` / repair drill、seed migration dry-run/apply、Phase 5 agent memory builder、Phase 6 retrieval / selector / prefetch。
- 如需 debug / 运维修复，只允许通过内部 service / script / debug-gated 入口触发；默认不额外暴露第 6 个 public memory tool，避免把 repair 面直接开放给常规 agent。

## 工具协议（MVP）

- `memory_read`：入参优先 `{name, scope?, type?}`；`path` 仅保留给内部/debug 场景，但**仍必须**经过同一 `sanitize + resolve + authorize` 流程；返回 `{entry, sourcePath, indexHit}`
- `memory_write`：入参 `{name, type, content, description?, scope?, skipIndex?}`；在 authorize 成功前不得创建目录/文件；返回 `{entry, action=create|update, indexUpdated}`
- `memory_search`：入参 `{keyword, type?, limit?, scope?}`；先走统一授权解析，再返回 `{results[], truncated}`，默认只回摘要/ID，不直接回整段正文
- `memory_list`：入参 `{scope?, type?}`；先走统一授权解析，再返回 `{entries[]}`
- `memory_forget`：入参优先 `{name, scope?}`；`path` 仅作为内部/debug 能力，但**仍必须**经过同一 `sanitize + resolve + authorize` 流程；返回 `{deleted, indexUpdated}`

## Memory 工具 ACL

- 与 Phase 5 **同一权限模型**：5 个 MCP 工具统一走 `sanitize → resolve → authorize`，禁止各工具各自发明一套 path / ACL 旁路
- `sanitize`：规范化 `name/scope/type/path/agentType` 等输入；`path`/`agentType` 解析成功只代表“可定位”，**不代表**“可访问”
- `resolve`：只允许在 canonical root / scope 约束内解析候选 memory root、topic file、agent memory 目录；不得因 miss/deny 而 fallback 到其它 memory root / scope
- `authorize`：基于 `requester_agent_id / requester_thread_id / root_agent_id / cwd / machine identity` 判定访问权；Phase 5 的 preview/runtime 与 Phase 7 的 MCP tooling 必须共享同一 authorizer
- `user` scope：仅 root agent / 当前线程授权链可读写；未进入当前线程可见集的 agentType 即使被 `@` 提及，也不能解析到其目录
- `project` scope：仅同 project / 同 workspace 授权链可访问；canonical git root 不匹配时直接 deny，不得回退到别的 memory root
- `local` scope：仅当前机器 + 当前 project 授权链可访问；跨机器 replay / restore 必须视为 unavailable，而不是静默降级成 project/user scope
- `memory_read/search/list/forget` 遇到未命中、未授权、`not_visible`、`local_unavailable` 时，按工具类型返回空结果或显式 deny reason，但**不得**泄露不可见 memory 的存在性细节
- 审计日志记录 `actor/resource/decision`，但**不记录正文**

## 兼容层

- 现有 `shared_file_read/write` 保持 V2 tool 名称、必填字段与共享文件定位不变
- 兼容语义与 V2 对齐：path 统一 `trim → slash normalize → path.Clean → trim leading/trailing /`；`shared_file_read` miss 统一 `file <path> not found`；`shared_file_write` actor 固定为 `agent`；内容上限保持 10 MiB
- 不新增 `shared_file_list` 等新 public tool；`shared_files` 仍只承担 agent 间实时协作，不作为 memory 主存储或默认迁移源
- `shared_files` DB 表继续用于 agent 间实时协作
- Memory 工具与 shared_files 是两套并行机制，不互相替代
- `memory_write` 做服务端敏感信息校验与 type 校验，不把安全约束只留在 prompt 层

## MCP 独立服务边界收口

- `cmd/mcp-orch` 必须满足 `docs/契约/mcp-service-convention.md` 的独立服务约束：Memory tooling 只能依赖 `internal/contract/*`、`internal/dto/*`、`cmd/mcp-orch/store/*` 与本地 adapter；不得把 `internal/platform/bus`、`internal/store/*`、`internal/module/*` 带进新链路
- 审查锚点：当前 `cmd/mcp-orch/fx.go:30-58` 仍注入 `platformbus.Module` / `internalStore.Module`，`cmd/mcp-orch/orchestration/service.go:13-25` 仍直接 import `internal/platform/bus`；Phase 7 需要先完成收口，再接 Memory 工具
- `cmd/mcp-orch/tools/memory_tools.go` 只保留 schema、参数解包、handler 壳与错误码映射；真正的 CRUD / ACL / path / migration 逻辑通过 `internal/contract/*` 接口 + `cmd/mcp-orch/*` 本地 adapter 落地，禁止直连 `internal/module/memory`
- 若 Memory 工具需要事件或存储能力，也必须经 contract / store adapter 显式注入，不能靠复用桌面侧全局 bus/store 模块“顺手拿到”

## 种子数据迁移

从现有文档**抽取合格片段**转为 memory 格式，而不是按“一篇文档一条 memory”生搬：

| 源文件 | 候选 type | 迁移方式 |
|--------|----------|---------|
| `docs/plans/迁移/p18/review-summary.md` | project | 只提取被多轮审查确认的长期决策、显式延后项与 authoritative source 结论；跳过轮次评分/参与 agent 数/临时争议 |
| `docs/plans/迁移/session-summary.md` | project | 只在 freshness check 通过时提取稳定项目事实和长期决策；若与 `review-summary.md` / `README.md` 冲突或时间戳更旧，则降级为 historical hint 并默认 skip |
| `docs/plans/迁移/会话习惯.md` | user / feedback | 仅保留稳定用户偏好与被确认有效的纠正规则；五阶流水线、LSP 约束、会话交接模板等默认跳过 |

> 迁移原则：
> - **不再迁移** `lsp-mandatory-prefix.md` / `lsp-advanced-guide.md` 到 memory；这类工具规范继续保留在 repo 文档 / prompt prefix / skill / prompt registry
> - `review-summary.md` / `README.md` 属于 authoritative source；`session-summary.md` 只有在 freshness check 通过且不与 authoritative docs 冲突时才参与 apply
> - session-summary.md 只提取稳定项目事实和已确认长期决策，不迁移会话进度/临时状态
> - 会话习惯.md 拆分为 user（角色/偏好）+ feedback（工作方式纠正），不整篇搬
> - `shared_files` DB 快照**不是**默认 seed source；若后续需要导入，必须单列 one-off importer，补 owner/scope/审计规则，不能在本 Phase 静默混入
> - 增加 `ExclusionClassifier`：识别 `convention / path_or_structure / recent_change / debug_recipe / current_context / plan_or_task / duplicate_claude_md / not_non_obvious`
> - 路径/文件名/符号名密度过高的候选内容直接 skip
> - 多文档候选先按 `canonical_name + normalized content hash` 去重；冲突时以 authoritative source 优先，并在报告中记录 `source_priority / conflict_reason`
> - 迁移脚本必须幂等 + dry-run 支持
> - dry-run / apply 报告至少包含：`source_file / candidate_count / created_count / updated_count / skipped_count / skipped_by_reason / created_topics[] / updated_topics[]`
> - apply 模式必须输出 apply manifest：记录新建/更新的 topic file、更新的 `MEMORY.md`、before/after hash 与来源文档，便于回滚/审计

## 发布开关与回滚

- rollout flags：`enable_memory_system`、`enable_prompt_registry`、`enable_prompt_assembly`、`enable_memory_tools`
- 共享 rollout flag 定义统一收口到 `internal/platform/config`（或 `cmd/mcp-orch` 本地 config adapter）；桌面侧 `memory.Config` / `prompt.Config` 只做模块内消费，`cmd/mcp-orch` 不得为了读开关去 import `internal/module/{memory,prompt}`
- **pre-rollout gate**：在真正启用前，必须先落地并验证 `flag/config 骨架 + prompt cache invalidate + memory tools 注册/feature_disabled + 子 Agent launch contract 统一`
- 渐进顺序：先 internal dogfood，再 codex 单 provider，再 claude，再双 provider 切换场景
- kill switch：任一 flag 关闭后应停止新写入/新注入，但**不破坏**既有 `shared_files` 协作链路
- 当前最安全的 rollback 策略其实是**不放量启用**；只有当 flags/cache/tool/launch-contract 四个前置条件全部落地后，回滚 drill 才算可演练
- 回滚步骤：关 flag → 清 section cache → 停迁移脚本/写入入口 → 保留磁盘 memory 文件（默认不删除）→ 仅在人工确认后做归档/清理
- 可观测性：`memory_write/search/forget` 与 prompt cache invalidate 必须记录 `provider/threadID/reason/scope/result`

## 错误处理与并发安全

- `feature flag disabled` → 工具返回显式 `feature_disabled`，不做静默 no-op
- 敏感信息/type/frontmatter/ACL/path 校验失败 → fail-closed，且不得留下半写 topic file / `MEMORY.md`
- 敏感信息校验必须具备**可执行规范**：规则集版本、稳定错误码、fixture 样例、误报复核路径
- `memory_write` 与迁移脚本统一走 `ExclusionClassifier`；命中排除项直接拒绝/跳过，并给出原因
- 迁移脚本任一步失败 → 输出 dry-run / apply 报告并停止后续写入，不自动清理用户原文档
- 同一 memory root 下的迁移与 `memory_write/forget` 共享写锁，避免并发改坏 `MEMORY.md`
- 迁移脚本按“批量 topic file + 单次 index rebuild”执行，避免每条 seed 都重写一次 `MEMORY.md`
- `memory_read/list/search` 遇到 `MEMORY.md` 缺失/损坏时，可降级到 `scan headers` 生成 `degraded` 视图；返回必须显式带 `degraded=true` / `source=rebuilt_view`，且**不得自动覆写磁盘索引**
- MCP error code 映射矩阵：
  - 参数/ACL/path/type/frontmatter 错误 → `ErrCodeInvalidParams`
  - topic/index 持久化失败 → `ErrCodePersistFailed`
  - manifest / selector / scan 超时 → `ErrCodeTimeout`
  - `feature_disabled` 暂保留业务错误字符串；若后续补独立 code，需在 DTO 层统一声明
- observability / snapshot / search/list 默认不记录正文，防止二次泄露

## 任务清单
- [ ] MCP 独立服务边界收口：清理 `cmd/mcp-orch` 对 `internal/platform/bus` / `internal/store/*` / `internal/module/*` 的直连，补齐 `internal/contract/*` + 本地 adapter 注入点，满足 `mcp-service-convention`
- [ ] `cmd/mcp-orch/tools/memory_tools.go`：只保留 5 个 Memory MCP 工具的 schema/handler 壳 + 统一 `sanitize + resolve + authorize` authorizer（与 Phase 5 共模）+ DTO/error 映射；实际 CRUD / ACL / path 逻辑通过 `internal/contract/*` 接口调用，禁止直连 `internal/module/memory`
- [ ] `scripts/p18-migrate-memory-seeds.go`：转换现有文档为 memory 格式（幂等 + dry-run）
- [ ] 提取 / 补齐 tool-facing Memory service/interface 到 `internal/contract/*`（仅放接口与错误码/常量），供桌面侧与 `cmd/mcp-orch` 共享边界
- [ ] 工具注册到 mcp-orch 动态工具链（不是只建文件不注册）
- [ ] rollout flags + kill switch 接入配置骨架

## 验收
- memory_write 写入后 memory_read 能读回
- 5 个 MCP 工具与 Phase 5 共享同一 `sanitize + resolve + authorize` 权限模型，无 path/ACL 旁路
- `cmd/mcp-orch` 符合 `mcp-service-convention`：Memory 工具链不再 import `internal/platform/bus` / `internal/store/*` / `internal/module/memory`，`memory_tools.go` 仅承担 schema/handler 壳
- memory_list 显示正确索引
- memory_forget 删除后索引同步更新
- 未授权 / 不可见 / cross-machine local scope 场景下，工具返回空结果或显式 deny reason，且不泄露不可见 memory 细节
- `shared_file_read/write` 与 V2 兼容：path canonicalization、`file <path> not found`、`agent` actor、10 MiB 上限语义保持一致
- 迁移脚本会跳过 LSP 规范文档与默认排除的 `shared_files` DB 快照，并输出 skip reason 报告
- `review-summary.md` / `README.md` / `session-summary.md` 同时存在时，authoritative source 优先级与 freshness check 行为可验证
- flags 关闭时新链路可立即停用，旧 shared_files 仍可用
- rollback drill：关闭 flags 后 section cache 清空、无新增 memory 写入
