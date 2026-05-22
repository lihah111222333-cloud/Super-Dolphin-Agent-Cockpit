LSP 工具链 · 所有 Agent 必读：

你有 11 个仓库感知工具，覆盖四类动作：搜索、理解、修改、验证。**禁止**只用 `lsp_grep + lsp_file` 两件套，也不要用 `code_run` 去拼 shell 替代已有的专用工具。

### 工具与主要 action

- `lsp_grep(text_search, glob=…)` —— 文本 / 正则搜索，可按路径过滤
- `lsp_grep(ast_search, language=…)` —— 按语法结构匹配函数、类型、表达式
- `lsp_structure(document_symbol | workspace_symbol)` —— 文件大纲 / 跨项目符号定位
- `lsp_inspect(definition | implementation)` —— 从 `file:line:col` 跳定义 / 所有实现
- `lsp_xref(references, verbosity="compact")` —— 引用扫描，返回里带 `func_start / func_end`
- `lsp_xref(call_hierarchy, direction="incoming")` —— 谁调了这个函数
- `lsp_file(read_file, offset=, limit=)` —— 按 1-based 行号分页精读
- `lsp_file(diagnostics, file_paths=[…])` —— 批量取编译 / 类型诊断
- `lsp_edit(replace_range, patch="@@ ctx\n-old\n+new" | edits=[{old_string,new_string}, …])` —— 精确改动
- `lsp_completion` —— 代码补全
- `code_run / code_run_test` —— 真实 shell / 构建 / 测试

### 强制工作流

```
审查类：grep 定位 → inspect 理解 → xref 影响面 → read_file 精读 → 输出判定
修复类：grep 定位 → xref 影响面 → read_file 读上下文 → edit 修改 → diagnostics 检查 → code_run_test / code_run build / test 验证
```

### 5 个组合技

- **A**：`ast_search` → 用返回的 `func_start / func_end` 直接 `read_file(offset, limit)` 精准读取
- **B**：`workspace_symbol` → `definition` → `read_file` 三步定位
- **C**：`references` + `call_hierarchy(incoming + outgoing)` 影响面全景
- **D**：`definition` → `implementation` → `references` 接口三级跳
- **E**：两次 `document_symbol` 对比两个文件的方法覆盖度

### func_start / func_end 快捷读取

`lsp_grep(ast_search)`、`lsp_inspect(definition|implementation)`、`lsp_xref(references, verbosity="compact")` 返回里都带 `func_start / func_end`，直接 `read_file(offset=func_start, limit=func_end-func_start+1)` 精准读函数，不必再调 `document_symbol`。

### 并行与批量

- 多个独立只读调用（多处 `definition / references / read_file`）并行发起，有依赖的再串行
- 改完多文件一次性 `diagnostics(file_paths=[…])` 批量查诊断，不要逐文件调

### 禁止

1. 只用 `lsp_grep + lsp_file` 两件套
2. 用 `code_run` 执行 `grep / rg / cat / head / tail / sed / awk / find / ls` 等可被专用工具替代的命令
3. 不做 `xref` 影响面分析就改代码
4. 不跑 `diagnostics` 就说验证通过 —— `diagnostics` 只查编译 / 类型，运行时行为必须跑对应测试
5. 每个任务必须组合使用至少 4 种 LSP 工具
