# Copilot CLI 工具映射

技能使用 Claude Code 的工具名称。当你在技能中遇到这些名称时，使用所在平台的等价工具：

| 技能引用 | Copilot CLI 等价工具 |
|-----------------|----------------------|
| `Read`（读取文件） | `view` |
| `Write`（创建文件） | `create` |
| `Edit`（编辑文件） | `edit` |
| `Bash`（运行命令） | `bash` |
| `Grep`（搜索文件内容） | `grep` |
| `Glob`（按名称搜索文件） | `glob` |
| `Skill` 工具（调用技能） | `skill` |
| `WebFetch` | `web_fetch` |
| `Task` 工具（派发子代理） | `task`（见 [代理类型](#代理类型)） |
| 多个 `Task` 调用（并行） | 多个 `task` 调用 |
| Task 状态/输出 | `read_agent`、`list_agents` |
| `TodoWrite`（任务跟踪） | `sql`，使用内置 `todos` 表 |
| `WebSearch` | 无等价工具：使用 `web_fetch` 加搜索引擎 URL |
| `EnterPlanMode` / `ExitPlanMode` | 无等价工具：留在主会话 |

## 代理类型

Copilot CLI 的 `task` 工具接受 `agent_type` 参数：

| Claude Code 代理 | Copilot CLI 等价方式 |
|-------------------|----------------------|
| `general-purpose` | `"general-purpose"` |
| `Explore` | `"explore"` |
| 命名插件代理（例如 `superpowers:code-reviewer`） | 从已安装插件自动发现 |

## 异步 shell 会话

Copilot CLI 支持持久异步 shell 会话，Claude Code 没有直接等价工具：

| 工具 | 用途 |
|------|---------|
| `bash` with `async: true` | 在后台启动长时间运行的命令 |
| `write_bash` | 向正在运行的异步会话发送输入 |
| `read_bash` | 读取异步会话输出 |
| `stop_bash` | 终止异步会话 |
| `list_bash` | 列出所有活跃 shell 会话 |

## 其他 Copilot CLI 工具

| 工具 | 用途 |
|------|---------|
| `store_memory` | 持久保存代码库相关事实，供未来会话使用 |
| `report_intent` | 使用当前意图更新 UI 状态行 |
| `sql` | 查询会话 SQLite 数据库（todos、metadata） |
| `fetch_copilot_cli_documentation` | 查找 Copilot CLI 文档 |
| GitHub MCP 工具（`github-mcp-server-*`） | 原生 GitHub API 访问（issues、PR、code search） |
