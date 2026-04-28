# LSP 工具链使用规范 · 所有 Agent 必读

你有 11 个仓库感知工具，覆盖四类动作：搜索、理解、修改、验证。**禁止**只用 `lsp_grep + lsp_file` 两件套，也不要用 `code_run` 去拼 shell 替代已有的专用工具。

## 基础使用

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

## 高级调试


### 调用链追踪

- `lsp_xref(call_hierarchy, direction="incoming")` —— 谁调了这个函数，回溯触发入口
- `lsp_xref(call_hierarchy, direction="outgoing")` —— 这个函数调了谁，追内部流
- `lsp_xref(call_hierarchy, direction="both")` —— 枢纽函数一次拿双向
- 组合：`inspect(definition)` 跳到目标 → `call_hierarchy(incoming)` 回溯入口 → `outgoing` 看依赖，拼出完整数据流

### 类型层次（接口 / 继承体系）

- `lsp_xref(type_hierarchy, direction="supertypes")` —— 父类 / 实现的接口
- `lsp_xref(type_hierarchy, direction="subtypes")` —— 所有实现 / 子类
- 组合：`inspect(type_definition)` 跳到类型 → `type_hierarchy(subtypes)` 枚举实现 → `document_symbol` 挨个看方法，定位该改哪个实现

### 轻量上下文理解（不用读整段代码就能确认）

- `lsp_inspect(hover)` —— 类型 / 注释 / 签名摘要，快速确认参数类型或返回值
- `lsp_inspect(signature_help)` —— 调用点的参数提示，排查参数顺序 / 类型不匹配
- `lsp_inspect(type_definition)` —— 跳到符号的"类型"的定义（`definition` 跳的是符号自身的定义）

### 诊断策略

- 通过 `file_paths=[…]` 把诊断限定到关心的文件，避免大仓库跨文件噪声
- 按 severity 处理：先解 error，再看 warning，最后视情况处理 hint / info
- 诊断不能代替测试 —— 运行时行为变了必须跑对应测试

### 修改策略（选对 action 能避免大量杂音）

- 改符号名 → `lsp_edit(rename)`：LSP 跨文件安全重命名，不要用 `replace_range` 逐处搜换
- 同文件多处改动 → `lsp_edit(replace_range, edits=[{old_string,new_string}, …])` 一次批量提交
- 单点小改动 → `lsp_edit(replace_range, patch="@@ ctx\n-old\n+new")`，patch 带上下文更安全
- 语义修复（missing import、简单 quickfix）→ `lsp_edit(code_action, only=["quickfix"])`
- 统一格式 → `lsp_edit(format)`，改动后跑一次

### 结构辅助

- `lsp_structure(folding_range)` —— 折叠区间，看嵌套结构
- `lsp_structure(semantic_tokens)` —— 语义高亮，罕用

### 执行与测试

- `code_run(project_cmd)` —— 真实 shell / 构建 / 脚本
- `code_run_test(test_func, test_pkg)` —— 精准跑单个 Go 测试函数，比整包 `go test` 快

### 高级组合（对基础 A–E 之外的补充）

- 数据流全景：`call_hierarchy(both)` 一次看双向，比单独跑 incoming / outgoing 少一轮
- 根因定位：`ast_search`（形状） + `diagnostics`（编译错） + `call_hierarchy(incoming)`（触发路径）

### 选工具的原则

- 已知 `file:line:col` → inspect / xref（定点操作）
- 只有关键字或模式 → grep（扫描定位）
- 要跨文件结构感 → structure（大纲）
- 要改动 → edit（结构化改）
- 要验证 → `file.diagnostics` + `code_run_test`
