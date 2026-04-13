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

- `memory_read`：入参 `{name?|path?, scope?, type?}`；返回 `{entry, sourcePath, indexHit}`
- `memory_write`：入参 `{name, type, content, description?, scope?, skipIndex?}`；返回 `{entry, action=create|update, indexUpdated}`
- `memory_search`：入参 `{keyword, type?, limit?, scope?}`；返回 `{results[], truncated}`
- `memory_list`：入参 `{scope?, type?}`；返回 `{entries[]}`
- `memory_forget`：入参 `{name|path, scope?}`；返回 `{deleted, indexUpdated}`

## 兼容层

- 现有 `shared_file_read/write` 保持不变
- `shared_files` DB 表继续用于 agent 间实时协作
- Memory 工具与 shared_files 是两套并行机制，不互相替代
- `memory_write` 做服务端敏感信息校验与 type 校验，不把安全约束只留在 prompt 层

## 种子数据迁移

将现有文档转为 memory 格式：

| 源文件 | 目标 type | 迁移方式 |
|--------|----------|---------|
| `docs/plans/迁移/session-summary.md` | project | 提取核心结论，写为 topic file |
| `docs/plans/迁移/会话习惯.md` | feedback | 提取用户偏好规则，写为 topic file |
| `docs/plans/迁移/lsp-mandatory-prefix.md` | feedback | 工具使用规范 |
| `docs/plans/迁移/lsp-advanced-guide.md` | reference | 工具指南引用 |

> 迁移原则：
> - session-summary.md 只提取稳定项目事实和已确认长期决策，不迁移会话进度/临时状态
> - 会话习惯.md 拆分为 user（角色/偏好）+ feedback（工作方式纠正），不整篇搬
> - 迁移脚本必须幂等 + dry-run 支持

## 发布开关与回滚

- rollout flags：`enable_memory_system`、`enable_prompt_registry`、`enable_prompt_assembly`、`enable_memory_tools`
- 开关统一收口到 `memory.Config` / `prompt.Config`，由 fx 注入，不把发布控制散落在 `StartRequest` / provider runtime config
- 渐进顺序：先 internal dogfood，再 codex 单 provider，再 claude，再双 provider 切换场景
- kill switch：任一 flag 关闭后应停止新写入/新注入，但**不破坏**既有 `shared_files` 协作链路
- 回滚步骤：关 flag → 清 section cache → 停迁移脚本/写入入口 → 保留磁盘 memory 文件（默认不删除）→ 仅在人工确认后做归档/清理
- 可观测性：`memory_write/search/forget` 与 prompt cache invalidate 必须记录 `provider/threadID/reason/scope/result`

## 任务清单
- [ ] `cmd/mcp-orch/tools/memory_tools.go`：5 个 Memory MCP 工具
- [ ] `scripts/p18-migrate-memory-seeds.go`：转换现有文档为 memory 格式（幂等 + dry-run）
- [ ] 工具注册到 mcp-orch 动态工具链
- [ ] rollout flags + kill switch 接入配置骨架

## 验收
- memory_write 写入后 memory_read 能读回
- memory_list 显示正确索引
- memory_forget 删除后索引同步更新
- flags 关闭时新链路可立即停用，旧 shared_files 仍可用
- rollback drill：关闭 flags 后 section cache 清空、无新增 memory 写入
