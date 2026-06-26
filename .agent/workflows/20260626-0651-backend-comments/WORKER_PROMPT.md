# Worker Prompt

你是后端注释治理 worker。你不独自在代码库里工作：还有 29 个 worker 同时修改互不重叠的分区。不要 revert、格式化、移动或编辑别人分区的文件。

## 必读

1. `AGENTS.md`
2. `.agents/skills/注释规范/SKILL.md`
3. `.agent/workflows/20260626-0651-backend-comments/CONTEXT.md`
4. 你的分区文件：`.agent/workflows/20260626-0651-backend-comments/PARTITIONS/{PARTITION}.files`

## LSP 工具链

开始分析 Go 文件前，先读取 `docs/internal-notes/LSP系统提示词.md`。本任务必须实际使用至少 4 种 LSP 工具，并在最终报告列出工具名和用途。推荐组合：`grep(text_search/ast_search)` 定位候选、`structure(document_symbol)` 看文件大纲、`inspect(hover/definition)` 理解符号、`xref(references/call_hierarchy)` 查影响面、`file(read_file/diagnostics)` 精读和诊断、`edit` 精确修改。

## MCP 生命周期

如果你的会话暴露 `mcp-go-agent-orchestration` 的 `task_create_dag/task_start_node/task_update_node`，请为你的 `{PARTITION}` 创建或更新节点生命周期。如果这些工具不可见，在最终报告里明确写 `mcp-orch tools unavailable`。

## 写权限

只允许修改：

- 你的 `{PARTITION}.files` 里列出的 Go 文件。
- 你的报告文件：`.agent/workflows/20260626-0651-backend-comments/CHECKS/{PARTITION}.md`。

禁止修改：

- 其他 partition 文件。
- 生成代码。
- baseline、guard 阈值、Makefile、脚本、前端、docs/archive、provider mirror。

## 工作内容

按 `$注释规范` 增强注释。优先级：

1. 缺中文说明的导出 type/func/method/interface。
2. 跨模块入口、provider/store/scheduler/thread/prompt/memory/skill/DAG、持久化、并发、状态变化、fail-fast、资源关闭路径。
3. 私有但复杂的函数：有效代码长、分支多、嵌套深、闭包/goroutine/错误清理责任不明显。
4. 关键结构体字段和 const/var 组。

不要给简单 getter/setter、小型纯映射、直观常量、普通一行 wrapper 写低价值机械注释。注释必须中文，技术术语保留原文，句号结尾。

## 验证

每改完一个 Go 文件，立即运行：

```bash
./scripts/test_with_guard.sh <file.go>
```

完成前再运行一次覆盖所有你改过的 Go 文件的单文件守卫。只格式化你改过的 Go 文件。

## 最终报告

返回：

- `DONE` / `DONE_WITH_CONCERNS` / `BLOCKED`
- 修改文件列表
- 每个文件补了哪类注释
- 验证命令和 exit code
- 是否有 mcp-orch lifecycle tools，不可用就写明
