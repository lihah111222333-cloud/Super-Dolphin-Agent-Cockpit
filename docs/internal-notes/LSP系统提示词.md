# LSP 高级工具链使用指南（子 Agent 必读）

> 当前 LSP MCP 对外工具短名：`file`、`inspect`、`xref`、`grep`、`structure`、`patch_edit`、`completion`；Codex 会话中对应 `mcp__lsp.<name>`。
> `file(action=diagnostics)` 是诊断入口，没有独立 `diagnostics` 工具；编辑入口是 `patch_edit`，不是 `edit` / `lsp_edit`。
> 工具入参是封闭的 JSON object；工具返回是 MCP `content` 中的纯文本行协议，不提供 `structuredContent`。
> `exec_command` 只用于构建、测试、脚本和必要 shell，不替代 LSP 导航、读取、诊断和编辑。
> 每个任务至少组合 4 种 LSP 工具；不要只用 `grep + file`。

---

## 一、5 个组合技

- A：AST 搜索 -> 精确读取：`grep(ast_search)` -> 用 `func_start/func_end` 调 `file(read_file)`。
- B：符号定位 -> 跳转定义 -> 读实现：`structure(workspace_symbol)` -> `inspect(definition)` -> `file(read_file)`。
- C：引用分析 -> 调用层级 -> 影响面：`xref(references)` -> `xref(call_hierarchy, incoming)` -> `xref(call_hierarchy, outgoing)`。
- D：接口 -> 实现 -> 引用：`inspect(definition)` -> `inspect(implementation)` -> `xref(references)`。
- E：文件大纲对比：`structure(document_symbol, file_path="v3/file.go")` -> `structure(document_symbol, file_path="v2/file.go")`。

## 二、强制工作流

审查类：`grep` 定位 -> `inspect` 理解 -> `xref` 影响面 -> `file(read_file)` 精读 -> 输出判定。

修复类：`grep` 定位 -> `xref` 影响面 -> `file(read_file)` 读取 -> `patch_edit` 修改 -> `file(diagnostics)` 检查 -> `exec_command` 验证。

## 三、最新契约要点

- 工具名在文档和调用说明中使用短名；不要写 `mcp_lsp.*`、`lsp_*` 或旧 `edit` 别名，除非是在说明 Codex 命名空间 `mcp__lsp.<name>`。
- 每次调用都传 JSON object；schema 关闭 `additionalProperties`，未知字段、旧字段和 action 不兼容字段必须 Fail-Fast，禁止兼容或静默忽略。
- 返回值只消费 `content` 的纯文本行协议：首行为 `OK` 或 `ERROR`，其后按需包含 `ATTR`、`ROW`、`HINT`；不得读取或等待已移除的 `structuredContent`。
- 三个语言字段不得混用：`ast_language` 只选择 `grep(ast_search)` 的 AST 语法；`workspace_language` 只选择无 `file_path` 的 `structure(workspace_symbol)` 工作区语言；`language_id` 只覆盖具体文件的 LSP server 路由。旧 `language` 字段已删除。
- `grep` 必须带 `action` 和 `query`；搜索范围只用 `paths[]`，单一路径也传单元素数组，不接受 `path` / `file_paths` 别名。`text_search` 可用 `regex`、`case_sensitive` 且禁止 `ast_language`；`ast_search` 可用 `ast_language` 且禁止 `regex`、`case_sensitive`。显式 `ast_language` 未注册或与可判定语言的 `glob` 冲突时必须报错。
- `file` actions：`open_file` 必须带 `file_path`；`read_file` 必须在 `pos`、`file_path`、`file_paths[]` 中恰选一个；`diagnostics` 必须在 `file_path`、`file_paths[]` 中恰选一个。
- `structure(workspace_symbol)` 必须带 `query`，并在 `file_path`、`workspace_language` 中恰选一个；使用 `workspace_language` 时禁止同时传 `language_id`，`match_mode` 默认 `exact`，需要模糊匹配时才显式传 `fuzzy`。`document_symbol`、`folding_range`、`semantic_tokens` 必须带 `file_path`，禁止 `workspace_language`。
- `inspect` 必须带 `action`、`pos`，action 仅为 `hover` / `definition` / `implementation` / `type_definition` / `signature_help`；`completion` 必须带 `pos`。`inspect`、`xref`、`completion` 的 `pos` 使用 `file:line:column`，`line/column` 都是 1-based。
- `xref` 必须带 `action`、`pos`；`references` 只使用 `include_declaration`，不接受 `direction`；`call_hierarchy` 的 `direction` 仅为 `incoming` / `outgoing` / `both`；`type_hierarchy` 的 `direction` 仅为 `supertypes` / `subtypes` / `both`。
- `patch_edit` 按 action 严格传参：`replace_range` 用 `file_path + patch`，`rename` 用 `pos + new_name`，`code_action` 用 `pos` 和可选 `only[]`，`format` 用 `file_path`；纯插入 patch 仍要用上下文行锚定。可直接复制的 replace_range JSON：`{"action":"replace_range","file_path":"internal/foo.go","patch":" package main\n-old\n+new","work_dir":"/absolute/workspace"}`。patch body 每行必须以空格、`-` 或 `+` 开头，空上下文行也必须保留前导空格；bare body 只允许最终非空行是 `*** End Patch` 或 `***`，不能传孤立 `*** End Patch`，完整 apply_patch envelope 必须同时包含 `*** Begin Patch` 和 `*** End Patch`。replace_range 的可选 scope 字段只能是 `work_dir`，不要传 `cwd` 或 `agent_id`。
- `file(read_file, pos=<file>:<line>)` 默认读函数窗口；固定行窗口加 `scope=lines`。
- 拿到 `func_start/func_end` 后直接读：`file(action=read_file, pos=<file>:<func_start>, limit=<func_end-func_start+1>)`。
- 所有 LSP 工具都支持 `work_dir`，但必须在可信 workspace roots 内。
- `max_results` 只裁剪返回结果；出现 `truncated`、`scope_too_broad`、超时或 `retryable` 时必须收窄路径、查询、语言或结果范围后重试，不得解释成全量结果。

## 四、禁止

1. 不要只用 `grep + file` 两个工具。
2. 不要用 `exec_command` 执行可由 LSP 完成的 `grep/cat/sed/find`。
3. 不要不做 `xref` 影响面分析就改共享代码。
4. 不要不跑 `file(diagnostics)` 就说类型/编译层面干净。
5. 不要把 diagnostics 当测试；运行时行为必须用 `exec_command` 跑对应测试。
