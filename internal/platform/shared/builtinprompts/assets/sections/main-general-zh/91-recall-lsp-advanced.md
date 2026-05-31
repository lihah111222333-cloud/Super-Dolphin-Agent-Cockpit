LSP 工具链 · 高级调试（中文）：

### 调用链追踪

- `xref(call_hierarchy, direction="incoming")` —— 谁调了这个函数，回溯触发入口
- `xref(call_hierarchy, direction="outgoing")` —— 这个函数调了谁，追内部流
- `xref(call_hierarchy, direction="both")` —— 枢纽函数一次拿双向
- 组合：`inspect(definition)` 跳到目标 → `call_hierarchy(incoming)` 回溯入口 → `outgoing` 看依赖，拼出完整数据流

### 类型层次（接口 / 继承体系）

- `xref(type_hierarchy, direction="supertypes")` —— 父类 / 实现的接口
- `xref(type_hierarchy, direction="subtypes")` —— 所有实现 / 子类
- 组合：`inspect(type_definition)` 跳到类型 → `type_hierarchy(subtypes)` 枚举实现 → `document_symbol` 挨个看方法，定位该改哪个实现

### 轻量上下文理解（不用读整段代码就能确认）

- `inspect(hover)` —— 类型 / 注释 / 签名摘要，快速确认参数类型或返回值
- `inspect(signature_help)` —— 调用点的参数提示，排查参数顺序 / 类型不匹配
- `inspect(type_definition)` —— 跳到符号的"类型"的定义（`definition` 跳的是符号自身的定义）

### 诊断策略

- 通过 `file_paths=[…]` 把诊断限定到关心的文件，避免大仓库跨文件噪声
- 按 severity 处理：先解 error，再看 warning，最后视情况处理 hint / info
- 诊断不能代替测试 —— 运行时行为变了必须跑对应测试

### 修改策略（选对 action 能避免大量杂音）

- 改符号名 → 先用 `xref(references)` 枚举影响面，再用 `edit(file_path, patch)` 分文件小批量改动
- 同文件多处改动 → 用一个 `edit(file_path, patch)` 提交带上下文的多 hunk 补丁
- 单点小改动 → 用 `edit(file_path, patch="*** Begin Patch…")`，补丁带上下文更安全
- 语义修复和格式化 → 当前工具面未暴露语义动作或格式化动作；改动后用 `file(diagnostics)` 和测试验证

### 结构辅助

- `structure(folding_range)` —— 折叠区间，看嵌套结构
- `structure(semantic_tokens)` —— 语义高亮，罕用

### 执行与测试

- `exec_command` —— 普通 shell / git / 包脚本（如 `npm run lint`）/ 测试

### 高级组合（对基础 A–E 之外的补充）

- 数据流全景：`call_hierarchy(both)` 一次看双向，比单独跑 incoming / outgoing 少一轮
- 根因定位：`ast_search`（形状） + `diagnostics`（编译错） + `call_hierarchy(incoming)`（触发路径）

### 选工具的原则

- 已知 `file:line:col` → inspect / xref（定点操作）
- 只有关键字或模式 → grep（扫描定位）
- 要跨文件结构感 → structure（大纲）
- 要改动 → edit（结构化改）
- 要验证 → `file(diagnostics)` + `exec_command`
