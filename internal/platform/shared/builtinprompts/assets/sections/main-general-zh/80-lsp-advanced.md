LSP 高级规则：

- 触发重构、重命名、找引用、调用链、类型层次、影响面或疑难 bug 时才使用本段。
- 已知符号位置时，先用 inspect 定点确认定义、类型、签名或 hover 信息。
- 影响面优先用 xref 的 references / call_hierarchy；接口和继承问题再看 type_hierarchy。
- 当前 edit 工具只暴露 patch 契约；跨文件重命名前先用 xref 枚举影响面，再做小批量 patch，不做普通搜换。
- diagnostics 只证明类型 / 编译状态，不替代行为测试。
- 完整 action、组合技和高级排查方法只放在 `lsp-advanced` recall 包中。
