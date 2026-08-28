# LSP 语义工作流回忆包

仅在 `structure`、`xref`、`diagnostics` 均暴露时使用；No-LSP 不注入本包。`structure` 定位真实 symbol，`xref` 只接收其返回的真实 `file:line:column`，`diagnostics` 只覆盖变更文件的差量行。

保持原生读取/搜索与 `apply_patch` 修改边界。记录 patch/hash 和 self-check；`OK` 不是 clean，错误、警告、信息和提示都要分类。
