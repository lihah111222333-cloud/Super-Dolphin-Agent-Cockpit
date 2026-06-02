LSP 工具链 · 所有 Agent 必读：

你有 7 个仓库感知 LSP 工具，覆盖搜索、理解、修改和诊断。工具名使用当前短名：`file`、`grep`、`inspect`、`xref`、`structure`、`edit`、`completion`。普通 shell、git、目录 / 文件检查、包脚本和测试默认走 `exec_command`；代码理解、跳转、诊断和编辑优先 LSP。**禁止**只用 `grep + file` 两件套，也不要用普通 shell 替代已有专用工具。

### 工具与主要 action

- `grep(text_search, glob=…)` —— 文本 / 正则搜索，可按路径过滤
- `grep(ast_search, language=…)` —— 按语法结构匹配函数、类型、表达式
- `structure(document_symbol | workspace_symbol)` —— 文件大纲 / 跨项目符号定位
- `inspect(definition | implementation)` —— 从 `file:line:col` 跳定义 / 所有实现
- `xref(references, verbosity="compact")` —— 引用扫描，返回里带 `func_start / func_end`
- `xref(call_hierarchy, direction="incoming")` —— 谁调了这个函数
- `file(read_file, pos=<file>:<line>, limit=)` —— 按 1-based 行号分页精读
- `file(diagnostics, file_paths=[…])` —— 批量取编译 / 类型诊断
- `edit(file_path=…, patch="*** Begin Patch…")` —— 用 apply-patch 风格补丁精确改动磁盘文件并同步 LSP
- `completion(pos=…)` —— 代码补全
- `exec_command` —— 普通 shell / git / 包脚本 / 测试

### 强制工作流

```
审查类：grep 定位 → inspect 理解 → xref 影响面 → file(read_file) 精读 → 输出判定
修复类：grep 定位 → xref 影响面 → file(read_file) 读上下文 → edit 修改 → file(diagnostics) 检查 → exec_command 验证
```

### 5 个组合技

- **A**：`ast_search` → 用返回的 `func_start / func_end` 直接 `file(read_file, pos=<file>:<func_start>, limit)` 精准读取
- **B**：`workspace_symbol` → `definition` → `file(read_file)` 三步定位
- **C**：`references` + `call_hierarchy(incoming + outgoing)` 影响面全景
- **D**：`definition` → `implementation` → `references` 接口三级跳
- **E**：两次 `document_symbol` 对比两个文件的方法覆盖度

### func_start / func_end 快捷读取

`grep(ast_search)`、`inspect(definition|implementation)`、`xref(references, verbosity="compact")` 返回里都带 `func_start / func_end`，直接 `file(read_file, pos=<file>:<func_start>, limit=func_end-func_start+1)` 精准读函数，不必再调 `document_symbol`。

### 并行与批量

- 多个独立只读调用（多处 `definition / references / read_file`）并行发起，有依赖的再串行
- 改完多文件一次性 `file(diagnostics, file_paths=[…])` 批量查诊断，不要逐文件调

### 禁止

1. 只用 `grep + file` 两件套
2. 用普通 shell 执行 `grep / rg / cat / head / tail / sed / awk / find / ls` 等可被专用工具替代的命令
3. 不做 `xref` 影响面分析就改代码
4. 不跑 `file(diagnostics)` 就说验证通过 —— diagnostics 只查编译 / 类型，运行时行为必须跑对应测试
5. 复杂跨文件改动应组合使用多种 LSP 工具；简单问题不强制凑工具
