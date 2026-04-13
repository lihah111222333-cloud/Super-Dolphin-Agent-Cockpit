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

## 工具协议（MVP）

- `memory_read`：入参优先 `{name, scope?, type?}`；`path` 仅保留给内部/debug 场景，且必须通过严格 containment 校验；返回 `{entry, sourcePath, indexHit}`
- `memory_write`：入参 `{name, type, content, description?, scope?, skipIndex?}`；返回 `{entry, action=create|update, indexUpdated}`
- `memory_search`：入参 `{keyword, type?, limit?, scope?}`；返回 `{results[], truncated}`，默认只回摘要/ID，不直接回整段正文
- `memory_list`：入参 `{scope?, type?}`；返回 `{entries[]}`
- `memory_forget`：入参优先 `{name, scope?}`；`path` 仅作为内部/debug 能力，且必须通过严格 containment 校验；返回 `{deleted, indexUpdated}`

## Memory 工具 ACL

- `user` scope：仅 root agent / 当前线程授权链可读写
- `project` scope：同 project / 同 workspace 授权链可读，写入需当前 project authority
- `local` scope：仅当前机器 + 当前 project 授权链可读写
- 工具上下文必须携带 `requester_agent_id / requester_thread_id / root_agent_id / cwd`
- 审计日志记录 `actor/resource/decision`，但**不记录正文**

## 兼容层

- 现有 `shared_file_read/write` 保持不变
- `shared_files` DB 表继续用于 agent 间实时协作
- Memory 工具与 shared_files 是两套并行机制，不互相替代
- `memory_write` 做服务端敏感信息校验与 type 校验，不把安全约束只留在 prompt 层

## 种子数据迁移

从现有文档**抽取合格片段**转为 memory 格式，而不是按“一篇文档一条 memory”生搬：

| 源文件 | 候选 type | 迁移方式 |
|--------|----------|---------|
| `docs/plans/迁移/session-summary.md` | project | 只提取稳定项目事实和长期决策；默认跳过“本次/当前/下一会话/验证通过”等时态内容 |
| `docs/plans/迁移/会话习惯.md` | user / feedback | 仅保留稳定用户偏好与被确认有效的纠正规则；五阶流水线、LSP 约束、会话交接模板等默认跳过 |

> 迁移原则：
> - **不再迁移** `lsp-mandatory-prefix.md` / `lsp-advanced-guide.md` 到 memory；这类工具规范继续保留在 repo 文档 / prompt prefix / skill / prompt registry
> - session-summary.md 只提取稳定项目事实和已确认长期决策，不迁移会话进度/临时状态
> - 会话习惯.md 拆分为 user（角色/偏好）+ feedback（工作方式纠正），不整篇搬
> - 增加 `ExclusionClassifier`：识别 `convention / path_or_structure / recent_change / debug_recipe / current_context / plan_or_task / duplicate_claude_md / not_non_obvious`
> - 路径/文件名/符号名密度过高的候选内容直接 skip
> - 迁移脚本必须幂等 + dry-run 支持
> - dry-run / apply 报告至少包含：`source_file / candidate_count / created_count / updated_count / skipped_count / skipped_by_reason / created_topics[] / updated_topics[]`
> - apply 模式必须输出 apply manifest：记录新建/更新的 topic file、更新的 `MEMORY.md`、before/after hash 与来源文档，便于回滚/审计

## 发布开关与回滚

- rollout flags：`enable_memory_system`、`enable_prompt_registry`、`enable_prompt_assembly`、`enable_memory_tools`
- 开关统一收口到 `memory.Config` / `prompt.Config`，由 fx 注入，不把发布控制散落在 `StartRequest` / provider runtime config
- 渐进顺序：先 internal dogfood，再 codex 单 provider，再 claude，再双 provider 切换场景
- kill switch：任一 flag 关闭后应停止新写入/新注入，但**不破坏**既有 `shared_files` 协作链路
- 回滚步骤：关 flag → 清 section cache → 停迁移脚本/写入入口 → 保留磁盘 memory 文件（默认不删除）→ 仅在人工确认后做归档/清理
- 可观测性：`memory_write/search/forget` 与 prompt cache invalidate 必须记录 `provider/threadID/reason/scope/result`

## 错误处理与并发安全

- `feature flag disabled` → 工具返回显式 `feature_disabled`，不做静默 no-op
- 敏感信息/type/frontmatter/ACL/path 校验失败 → fail-closed，且不得留下半写 topic file / `MEMORY.md`
- `memory_write` 与迁移脚本统一走 `ExclusionClassifier`；命中排除项直接拒绝/跳过，并给出原因
- 迁移脚本任一步失败 → 输出 dry-run / apply 报告并停止后续写入，不自动清理用户原文档
- 同一 memory root 下的迁移与 `memory_write/forget` 共享写锁，避免并发改坏 `MEMORY.md`
- 迁移脚本按“批量 topic file + 单次 index rebuild”执行，避免每条 seed 都重写一次 `MEMORY.md`
- observability / snapshot / search/list 默认不记录正文，防止二次泄露

## 任务清单
- [ ] `cmd/mcp-orch/tools/memory_tools.go`：5 个 Memory MCP 工具 + ACL / path authorizer
- [ ] `scripts/p18-migrate-memory-seeds.go`：转换现有文档为 memory 格式（幂等 + dry-run）
- [ ] 工具注册到 mcp-orch 动态工具链（不是只建文件不注册）
- [ ] rollout flags + kill switch 接入配置骨架

## 验收
- memory_write 写入后 memory_read 能读回
- memory_list 显示正确索引
- memory_forget 删除后索引同步更新
- 迁移脚本会跳过 LSP 规范文档，并输出 skip reason 报告
- flags 关闭时新链路可立即停用，旧 shared_files 仍可用
- rollback drill：关闭 flags 后 section cache 清空、无新增 memory 写入
