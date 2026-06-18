# LSP 工具链使用规范

本文档是使用 LSP 工具前的必读规范。LSP 工具用于代码理解、符号跳转、引用分析、诊断和结构化编辑；普通 shell、git、目录检查、包脚本和测试仍使用常规命令。

## 基础工具

- `grep(text_search, glob=...)`：文本或正则搜索，可按路径过滤。
- `grep(ast_search, language=...)`：按语法结构匹配函数、类型和表达式。
- `structure(document_symbol | workspace_symbol)`：查看文件大纲或项目符号。
- `inspect(definition | implementation | hover | signature_help | type_definition)`：从 `file:line:col` 跳转或查看符号信息。
- `xref(references, verbosity="compact")`：扫描引用，结果可带 `func_start` 和 `func_end`。
- `xref(call_hierarchy, direction="incoming|outgoing|both")`：追踪调用链。
- `file(read_file, pos=<file>:<line>, limit=<n>)`：按 1-based 行号分页读取文件。
- `file(diagnostics, file_paths=[...])`：批量获取编译或类型诊断。
- `edit(file_path=..., patch="*** Begin Patch...")`：使用 apply-patch 风格精确修改文件并同步 LSP。
- `completion(pos=<file>:<line>:<col>)`：获取代码补全候选。

## 推荐流程

审查类任务：

```text
grep 定位 -> inspect 理解 -> xref 影响面 -> file(read_file) 精读 -> 输出结论
```

修复类任务：

```text
grep 定位 -> xref 影响面 -> file(read_file) 读上下文 -> edit 修改 -> file(diagnostics) 检查 -> 运行对应测试
```

## 关键约束

- 已知行号时，使用 `file(read_file, pos=<file>:<line>, limit=<n>)` 精读上下文。
- 从 `ast_search`、`definition`、`implementation` 或 `references` 得到 `func_start` / `func_end` 后，可直接使用 `file(read_file, pos=<file>:<func_start>, limit=<func_end-func_start+1>)` 读取函数范围。
- 修改共享符号前先用 `xref(references)` 或调用链工具确认影响面。
- 多文件修改后优先一次性运行 `file(diagnostics, file_paths=[...])`，再运行对应测试。
- 诊断不能替代测试；运行时行为变化必须跑相关测试。

## 禁止事项

- 不要只靠 `grep + file` 两件套完成复杂代码审查。
- 不要在应使用 LSP 符号工具时用普通文本搜索替代影响面分析。
- 不要未读取上下文就修改代码。
- 不要未运行诊断或测试就声称修复完成。
