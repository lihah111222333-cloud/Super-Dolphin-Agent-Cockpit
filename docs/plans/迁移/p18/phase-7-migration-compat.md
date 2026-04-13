# P18 Phase 7：迁移工具 + 兼容层

> 预计：1 天 | 依赖：Phase 1, Phase 2

## 目标
提供 Memory MCP 工具 + 现有数据迁移脚本。

## Memory MCP 工具

| 工具 | 功能 |
|------|------|
| `memory_read` | 按名称/路径读取记忆 |
| `memory_write` | 写入新记忆（type 校验 + frontmatter 生成 + 索引更新） |
| `memory_search` | 搜索记忆（按 keyword 匹配 description/content） |
| `memory_list` | 列出 MEMORY.md 索引 |
| `memory_forget` | 删除指定记忆（topic file + 索引项） |

## 兼容层

- 现有 `shared_file_read/write` 保持不变
- `shared_files` DB 表继续用于 agent 间实时协作
- Memory 工具与 shared_files 是两套并行机制，不互相替代

## 种子数据迁移

将现有文档转为 memory 格式：

| 源文件 | 目标 type | 迁移方式 |
|--------|----------|---------|
| `docs/plans/迁移/session-summary.md` | project | 提取核心结论，写为 topic file |
| `docs/plans/迁移/会话习惯.md` | feedback | 提取用户偏好规则，写为 topic file |
| `docs/plans/迁移/lsp-mandatory-prefix.md` | feedback | 工具使用规范 |
| `docs/plans/迁移/lsp-advanced-guide.md` | reference | 工具指南引用 |

## 任务清单
- [ ] `cmd/mcp-orch/tools/memory_tools.go`：5 个 Memory MCP 工具
- [ ] 迁移脚本：转换现有文档为 memory 格式
- [ ] 工具注册到 mcp-orch 动态工具链

## 验收
- memory_write 写入后 memory_read 能读回
- memory_list 显示正确索引
- memory_forget 删除后索引同步更新
