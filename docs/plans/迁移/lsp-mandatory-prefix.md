## LSP 工具链强制指令（所有 Agent 必读）

> 先用 shared_file_read 读取 `prompts/lsp-advanced-guide.md` 获取完整指南。

### 你有 9 个 LSP 工具，禁止只用 lsp_grep + lsp_file

| 必用工具 | 场景 |
|---|---|
| `lsp_grep(ast_search, language="go")` | 找函数/类型/模式 |
| `lsp_inspect(definition)` | 从调用点跳到定义 |
| `lsp_inspect(implementation)` | 查接口的所有实现 |
| `lsp_xref(references, verbosity="compact")` | 影响面分析（附带 func_start/func_end） |
| `lsp_xref(call_hierarchy, direction="incoming")` | 谁调用了这个函数 |
| `lsp_structure(document_symbol)` | 文件符号大纲 |
| `lsp_edit(replace_range, patch="@@ ctx\n-old\n+new")` | 精确代码修改 |
| `lsp_file(diagnostics)` | 编译错误检查 |
| `lsp_completion` | 代码补全 |

### 强制工作流
```
审查类：grep定位 → inspect理解 → xref影响面 → read精读 → 输出判定
修复类：grep定位 → xref影响面 → read读取 → edit修改 → diagnostics检查 → build/test验证
```

### 5 个组合技
- **A**: ast_search → func_start/func_end → read_file 精准读取
- **B**: workspace_symbol → definition → read_file 三步定位
- **C**: references → call_hierarchy(incoming+outgoing) 影响面全景
- **D**: definition → implementation → references 接口三级跳
- **E**: document_symbol 对比两个文件的方法覆盖度

### func_start/func_end 快捷读取
lsp_grep、lsp_inspect(definition/implementation)、lsp_xref(references compact) 返回都附带 func_start/func_end，直接 `read_file(offset=func_start, limit=func_end-func_start+1)` 精准读取，无需再调 document_symbol。

### 禁止
1. ❌ 只用 lsp_grep + lsp_file 两个工具
2. ❌ code_run 执行 grep/cat/sed/find
3. ❌ 不做 xref 影响面分析就改代码
4. ❌ 不跑 diagnostics 就说验证通过
5. ❌ 每个任务必须组合使用至少 4 种 LSP 工具
