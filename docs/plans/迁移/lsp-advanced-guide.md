# LSP 高级工具链使用指南（子 Agent 必读）

> 你有 9 个 LSP 工具 + 2 个 Run 工具，不要只用 lsp_grep 和 lsp_file。
> 每个任务必须组合使用至少 4 种 LSP 工具，否则视为不合格。

---

## 一、工具全景（9 个 LSP + 2 个 Run）

| 工具 | 用途 | 场景 |
|---|---|---|
| `lsp_grep(text_search)` | 文本搜索 | 找关键字、调用点 |
| `lsp_grep(ast_search)` | AST 结构搜索 | 找函数签名、类型定义 |
| `lsp_structure(document_symbol)` | 文件符号大纲 | 看文件有哪些函数/类型 |
| `lsp_structure(workspace_symbol)` | 全局符号搜索 | 找接口/类型/函数定义 |
| `lsp_inspect(definition)` | 跳转到定义 | 从调用点跳到声明处 |
| `lsp_inspect(implementation)` | 查接口实现 | 找接口的所有实现类 |
| `lsp_inspect(hover)` | 悬停信息 | 看类型签名和文档 |
| `lsp_xref(references)` | 查引用 | 影响面分析 |
| `lsp_xref(call_hierarchy)` | 调用层级 | 谁调用了 / 调用了谁 |
| `lsp_edit(replace_range)` | 精确替换 | 用 patch 修改代码 |
| `lsp_edit(rename)` | 语义重命名 | 全项目安全重命名 |
| `lsp_file(read_file)` | 读文件 | 精确按行读取 |
| `lsp_file(diagnostics)` | 诊断 | 检查编译错误 |
| `lsp_completion` | 补全 | 写代码时获取建议 |
| `code_run` | 运行命令 | go build / go test |
| `code_run_test` | 运行测试 | 指定测试函数和包 |

## 二、5 个组合技

### A：AST 搜索 → 精确读取
```
1. lsp_grep(ast_search, query="func ($R) MethodName(", language="go")
2. 用返回的 func_start/func_end → read_file 精准读取
```

### B：符号定位 → 跳转定义 → 读实现
```
1. workspace_symbol → 找到符号位置
2. definition → 跳到定义
3. read_file → 读实现
```

### C：引用分析 → 调用层级 → 影响面
```
1. references → 找所有引用点
2. call_hierarchy(incoming) → 谁调用了它
3. call_hierarchy(outgoing) → 它调用了谁
```

### D：接口→实现→引用 三级跳
```
1. definition → 接口定义
2. implementation → 所有实现类
3. references → 所有调用点
```

### E：文件大纲对比
```
1. document_symbol(v3/file.go)
2. document_symbol(v2/file.go)
3. 逐一对比找缺失
```

## 三、强制工作流

审查类：grep定位 → inspect理解 → xref影响面 → read精读 → 输出判定
修复类：grep定位 → xref影响面 → read读取 → edit修改 → diagnostics检查 → build/test验证

## 四、func_start/func_end 快捷读取

lsp_grep、lsp_inspect(definition/implementation)、lsp_xref(references compact) 返回附带 func_start/func_end，直接 `read_file(offset=func_start, limit=func_end-func_start+1)` 精准读取。

## 五、禁止

1. ❌ 只用 lsp_grep + lsp_file 两个工具
2. ❌ code_run 执行 grep/cat/sed/find
3. ❌ 不做 xref 影响面分析就改代码
4. ❌ 不跑 diagnostics 就说验证通过
5. ❌ 每个任务必须组合使用至少 4 种 LSP 工具
