# Gemini CLI 工具映射

技能使用 Claude Code 的工具名称。当你在技能中遇到这些名称时，使用所在平台的等价工具：

| 技能引用 | Gemini CLI 等价工具 |
|-----------------|----------------------|
| `Read`（读取文件） | `read_file` |
| `Write`（创建文件） | `write_file` |
| `Edit`（编辑文件） | `replace` |
| `Bash`（运行命令） | `run_shell_command` |
| `Grep`（搜索文件内容） | `grep_search` |
| `Glob`（按名称搜索文件） | `glob` |
| `TodoWrite`（任务跟踪） | `write_todos` |
| `Skill` 工具（调用技能） | `activate_skill` |
| `WebSearch` | `google_web_search` |
| `WebFetch` | `web_fetch` |
| `Task` 工具（派发子代理） | 无等价工具：Gemini CLI 不支持子代理 |

## 无子代理支持

Gemini CLI 没有 Claude Code `Task` 工具的等价能力。依赖子代理派发的技能（`子代理驱动开发`、`调度并行代理`）将回退到通过 `执行计划` 进行单会话执行。

## 其他 Gemini CLI 工具

这些工具在 Gemini CLI 中可用，但 Claude Code 没有等价工具：

| 工具 | 用途 |
|------|---------|
| `list_directory` | 列出文件和子目录 |
| `save_memory` | 将事实持久保存到 GEMINI.md，供后续会话使用 |
| `ask_user` | 请求用户提供结构化输入 |
| `tracker_create_task` | 丰富任务管理（创建、更新、列出、可视化） |
| `enter_plan_mode` / `exit_plan_mode` | 在修改前切换到只读研究模式 |
