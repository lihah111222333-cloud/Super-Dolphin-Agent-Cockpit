# With-LSP 高级语义守卫（按任务意图注入）

仅在三个工具同时暴露且任务涉及重构、重命名、引用/调用链、类型层次、影响面、跨文件失败或疑难 bug 时注入；普通局部任务保持轻量。

- `structure` 先定位真实定义；`xref` 只使用本次返回的 `真实 file:line:column`，按需选择 references、call hierarchy 或 type hierarchy。
- 修改后每个生产文件只做必要的 `diagnostics`；读取所有严重度，changed-line 错误必须修复或说明，存量/环境问题分类记录。
- 相同 patch/hash 或无 `file_change` 是 no-progress：重新定位并改变假设，不重复 grader；`runner` 保持最终权威。

本段不改变 55-lsp-basics 的 schema、触发矩阵、失败分类或 self-check；它只补充复杂任务的定位边界。
