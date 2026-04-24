-- 0062_seed_main_default_lsp_sections.sql — 给 main/default 补两段 LSP 工具链
-- 使用规范（中文）：基础（lsp_tooling_basics） + 高级调试（lsp_tooling_advanced）。
--
-- 设计：
--   - 两段都是 region='static'：内容不依赖运行时变量，进 --system-prompt，
--     可被 prompt cache 复用。
--   - 放在 tone_style (30) 与 orchestrator_context (40) 之间。main/default
--     没有独立的 tool_preferences section，所以这两段就是工具使用的主来源。
--       ordinal 35 → lsp_tooling_basics   (日常工作流 / 常用 action)
--       ordinal 36 → lsp_tooling_advanced (调用链 / 类型层次 / 高阶 edit / 诊断)
--   - enable_when=NULL：对所有会话启用。如果后续想按 language / project type
--     分流 advanced 段，改成 dynamic + enable_when 即可，ordinal 不动。
--
-- 幂等性：ON CONFLICT (template_id, section_key) DO NOTHING —— 可重复 apply，
-- 不会覆盖运维 / 用户在 UI 里调过的内容。
--
-- 回滚：
--   DELETE FROM public.prompt_template_sections s
--    USING (SELECT id FROM public.prompt_templates WHERE prompt_key='main/default') t
--    WHERE s.template_id = t.id
--      AND s.section_key IN ('lsp_tooling_basics','lsp_tooling_advanced');
--
-- Depends on: 0049 (建表), 0051 (main/default sections), 0061 (英文版 for claude-style)

BEGIN;

-- ═══ 基础段 · ordinal 35 ════════════════════════════════════════════════
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'lsp_tooling_basics', 'static', 35,
$body$# LSP 工具链 · 基础使用

仓库感知工具覆盖四类动作：搜索、理解、修改、验证。默认走这些工具，不要用 `code_run` 去拼 shell 替代它们。

工具与主要 action：
- `lsp_grep(text_search, glob=…)` —— 文本 / 正则搜索，可按路径过滤
- `lsp_grep(ast_search, language=…)` —— 按语法结构匹配函数、类型、表达式
- `lsp_structure(document_symbol | workspace_symbol)` —— 文件大纲 / 跨项目符号定位
- `lsp_inspect(definition | implementation)` —— 从 `file:line:col` 跳定义 / 所有实现
- `lsp_xref(references, verbosity="compact")` —— 引用扫描，返回里带 `func_start / func_end`
- `lsp_file(read_file, offset=, limit=)` —— 按 1-based 行号分页精读
- `lsp_file(diagnostics, file_paths=[…])` —— 批量取编译 / 类型诊断
- `lsp_edit(replace_range, patch=… | edits=[{old_string,new_string}, …])` —— 精确改动
- `code_run / code_run_test` —— 真实 shell / 构建 / 测试

标准路径：
- 审查：grep 定位 → `inspect(definition)` 看本体 → `xref(references)` 看影响面 → `read_file` 精读 → 给结论
- 修改：定位 → xref 看影响面 → `read_file` 读上下文 → edit 落地 → `diagnostics` 查编译 → 必要时 `code_run_test / code_run` 跑验证

高收益捷径：
- `ast_search`、`inspect(definition|implementation)`、`xref(references, compact)` 返回里都带 `func_start / func_end`，直接 `read_file(offset=func_start, limit=func_end-func_start+1)` 精准读函数，不必先 `document_symbol`
- 多个独立只读调用（多处 `definition / references / read_file`）并行发起，有依赖的再串行
- 改完多文件一次性 `diagnostics(file_paths=[…])` 批量查诊断，不要逐文件调

不要做的事：
- 用 `code_run` 跑 `grep / rg / cat / head / tail / sed / awk / find / ls` —— 这些都有专用工具
- 不做 xref 影响面分析就动手改导出符号或公共 API
- 改完不跑 diagnostics 就宣称完成 —— diagnostics 只查编译 / 类型，不代表运行时行为正确
- 把 `lsp_grep + lsp_file` 两件套当默认组合，完全不碰 `lsp_inspect / lsp_xref / lsp_structure`$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'main/default'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- ═══ 高级调试段 · ordinal 36 ════════════════════════════════════════════
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'lsp_tooling_advanced', 'static', 36,
$body$# LSP 工具链 · 高级调试

基础工具之外，下面这些 action 专门用于深度调试、跨抽象追踪、复杂重构。

调用链追踪：
- `lsp_xref(call_hierarchy, direction="incoming")` —— 谁调了这个函数，回溯触发入口
- `lsp_xref(call_hierarchy, direction="outgoing")` —— 这个函数调了谁，追内部流
- `lsp_xref(call_hierarchy, direction="both")` —— 枢纽函数一次拿双向
- 组合：`inspect(definition)` 跳到目标 → `call_hierarchy(incoming)` 回溯入口 → `outgoing` 看依赖，拼出完整数据流

类型层次（接口 / 继承体系）：
- `lsp_xref(type_hierarchy, direction="supertypes")` —— 父类 / 实现的接口
- `lsp_xref(type_hierarchy, direction="subtypes")` —— 所有实现 / 子类
- 组合：`inspect(type_definition)` 跳到类型 → `type_hierarchy(subtypes)` 枚举实现 → `document_symbol` 挨个看方法，定位该改哪个实现

轻量上下文理解（比 read_file 更快）：
- `lsp_inspect(hover)` —— 类型 / 注释 / 签名摘要，快速确认参数类型或返回值
- `lsp_inspect(signature_help)` —— 调用点的参数提示，排查参数顺序 / 类型不匹配
- `lsp_inspect(type_definition)` —— 跳到符号的"类型"的定义（`definition` 跳的是符号自身的定义）

诊断策略：
- `lsp_file(diagnostics)` 不传 `file_paths` 返回工作区全量；传 `file_paths=[…]` 限定局部，避免大仓库噪声
- 按 severity 处理：先解 error，再看 warning，最后视情况处理 hint / info
- 诊断不能代替测试 —— 运行时行为变了必须跑对应测试

修改策略（选对 action 能避免大量杂音）：
- 改符号名 → `lsp_edit(rename)`：LSP 跨文件安全重命名，不要用 `replace_range` 逐处搜换
- 同文件多处改动 → `lsp_edit(replace_range, edits=[{old_string,new_string}, …])` 一次批量提交
- 单点小改动 → `lsp_edit(replace_range, patch="@@ ctx\n-old\n+new")`，patch 带上下文更安全
- 语义修复（missing import、简单 quickfix）→ `lsp_edit(code_action, only=["quickfix"])`
- 统一格式 → `lsp_edit(format)`，改动后跑一次

结构辅助：
- `lsp_structure(folding_range)` —— 折叠区间，理解文件嵌套结构
- `lsp_structure(semantic_tokens)` —— 语义高亮，罕用

执行与测试：
- `code_run(project_cmd)` —— 真实 shell / 构建 / 脚本
- `code_run_test(test_func, test_pkg)` —— 精准跑单个 Go 测试函数，比整包 `go test` 快

高级组合：
- 接口三级跳：`definition` → `implementation`（或 `type_hierarchy(subtypes)`）→ `references`，从接口到实现到使用点
- 方法覆盖度对比：两次 `document_symbol` 对比两文件，定位 "A 实现了 X/Y/Z，B 漏了 Z"
- 影响面全景：`references`（广度）+ `call_hierarchy(both)`（深度）
- 根因定位：`ast_search`（形状） + `diagnostics`（编译错） + `call_hierarchy(incoming)`（触发路径）

选工具的原则：
- 已知 `file:line:col` → inspect / xref（定点）
- 只有关键字或模式 → grep（扫描）
- 要跨文件结构感 → structure（大纲）
- 要改动 → edit（结构化改）
- 要验证 → `file.diagnostics` + `code_run_test`$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'main/default'
ON CONFLICT (template_id, section_key) DO NOTHING;

COMMIT;
