# LSP 高级工具链使用指南（子 Agent 必读）

> 当前契约：`grep`、`file`、`structure`、`inspect`、`xref`、`patch_edit`、`completion` + `exec_command`。
> 每个任务至少组合 4 种 LSP 工具；不要只用 `grep + file`。
> `exec_command` 只用于构建、测试、脚本和必要 shell，不替代 LSP 导航、读取、诊断和编辑。

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

- `pos` 使用 `file:line:column`；`line/column` 都是 1-based。
- `file(read_file, pos=<file>:<line>)` 默认读函数窗口；固定行窗口加 `scope=lines`。
- 拿到 `func_start/func_end` 后直接读：`file(action=read_file, pos=<file>:<func_start>, limit=<func_end-func_start+1>)`。
- `structure(workspace_symbol)` 必须带 `query`；`language` 与 `file_path` 二选一。
- 所有 LSP 工具都支持 `work_dir`，但必须在可信 workspace roots 内。
- `max_results` 只裁剪返回结果；超时要收窄路径、查询或语言。

## 四、禁止

1. 不要只用 `grep + file` 两个工具。
2. 不要用 `exec_command` 执行可由 LSP 完成的 `grep/cat/sed/find`。
3. 不要不做 `xref` 影响面分析就改共享代码。
4. 不要不跑 `file(diagnostics)` 就说类型/编译层面干净。
5. 不要把 diagnostics 当测试；运行时行为必须用 `exec_command` 跑对应测试。
