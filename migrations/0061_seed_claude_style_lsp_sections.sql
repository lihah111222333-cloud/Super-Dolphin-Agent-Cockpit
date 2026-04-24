-- 0061_seed_claude_style_lsp_sections.sql — 给 main/claude-style 补 4 段 LSP 工具链
-- 规范：英文 basics 常驻 + 英文 advanced 按关键词触发 + 中文 basics/advanced 按 language=zh 触发。
--
-- 语义：
--   static  · lsp_basics      ordinal 55 · 兜底英文基础段（11 工具 / 5 组合 / 5 禁止），每次会话注入
--   dynamic · lsp_advanced    ordinal 20 · 高级调试段，tags_has 命中编程关键词时注入
--   dynamic · lsp_basics_zh   ordinal 30 · 中文基础段，language=zh 用户注入
--   dynamic · lsp_advanced_zh ordinal 40 · 中文高级调试段，language=zh AND tags_has 都命中时注入
--
-- 依赖：section 级 enable_when 的 tags_has 支持来自本仓库 commit 9e6d1d5
--       （internal/module/prompt/enable_when.go:sectionEnableKeyMatches + matchSectionTagsHas）。
--       旧代码读到 tags_has 会 fail-closed（未知 key 丢弃段），对行为是安全降级：
--       高级段不注入，不会误触发。
--
-- 幂等：ON CONFLICT (template_id, section_key) DO NOTHING —— 再次 apply 不覆盖 UI / 运维手改。
--
-- 回滚：
--   DELETE FROM public.prompt_template_sections s
--    USING (SELECT id FROM public.prompt_templates WHERE prompt_key='main/claude-style') t
--    WHERE s.template_id = t.id
--      AND s.section_key IN ('lsp_basics','lsp_advanced','lsp_basics_zh','lsp_advanced_zh');
--
-- Depends on: 0049 (建表), 0057 (main/claude-style 模板), 0058 (首批 sections)

BEGIN;

-- lsp_basics · static · ordinal 55
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'lsp_basics', 'static', 55,
    $LSPSEED$LSP toolchain — required reading for all agents:

You have 11 repository-aware tools covering four action classes (search, understand, edit, verify). Do not default to the `lsp_grep + lsp_file` two-tool combo, and do not wrap `code_run` around shell commands that a dedicated tool already does.

### Tools and primary actions

- `lsp_grep(text_search, glob=...)` — text / regex search, path-filterable
- `lsp_grep(ast_search, language=...)` — match functions, types, and expressions by syntax shape
- `lsp_structure(document_symbol | workspace_symbol)` — file outline / project-wide symbol lookup
- `lsp_inspect(definition | implementation)` — from `file:line:col`, jump to definition / all implementations
- `lsp_xref(references, verbosity="compact")` — reference scan; each hit carries `func_start / func_end`
- `lsp_xref(call_hierarchy, direction="incoming")` — who calls this function
- `lsp_file(read_file, offset=, limit=)` — paged read with 1-based line numbers
- `lsp_file(diagnostics, file_paths=[...])` — batch compile / type diagnostics
- `lsp_edit(replace_range, patch="@@ ctx\n-old\n+new" | edits=[{old_string,new_string}, ...])` — precise edits
- `lsp_completion` — code completion
- `code_run / code_run_test` — real shell, build, scripts, Go test functions

### Mandatory workflow

```
Review: grep locate  → inspect read   → xref impact → read_file study   → conclude
Fix:    grep locate  → xref impact    → read_file context → edit apply  → diagnostics check → code_run_test / code_run verify
```

### Five combos

- **A**: `ast_search` → use the returned `func_start / func_end` with `read_file(offset, limit)` for precise reads
- **B**: `workspace_symbol` → `definition` → `read_file` — three-step locate
- **C**: `references` + `call_hierarchy(incoming + outgoing)` — full impact view
- **D**: `definition` → `implementation` → `references` — interface three-step
- **E**: two `document_symbol` calls to diff method coverage between two files

### func_start / func_end shortcut

`lsp_grep(ast_search)`, `lsp_inspect(definition|implementation)`, and `lsp_xref(references, verbosity="compact")` all return `func_start / func_end`. Call `read_file(offset=func_start, limit=func_end-func_start+1)` directly to read the full function — no need to run `document_symbol` first.

### Parallel and batch

- Fire independent read-only calls (multiple `definition / references / read_file`) in parallel; serialize only when one depends on another
- After multi-file edits, run `diagnostics(file_paths=[...])` once instead of per file

### Prohibitions

1. Do not default to the `lsp_grep + lsp_file` two-tool combo
2. Do not invoke `code_run` to run `grep / rg / cat / head / tail / sed / awk / find / ls` or similar shell commands that a dedicated tool already covers
3. Do not modify code without first running `xref` for impact
4. Do not claim verification passed without running `diagnostics` — `diagnostics` only catches compile / type errors; runtime behavior must be exercised with actual tests
5. Each task must combine at least four LSP tools$LSPSEED$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'main/claude-style'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- lsp_advanced · dynamic · ordinal 20
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'lsp_advanced', 'dynamic', 20,
    $LSPSEED$LSP toolchain advanced debugging:

### Call-chain tracing

- `lsp_xref(call_hierarchy, direction="incoming")` — who calls this function (walk back to triggers)
- `lsp_xref(call_hierarchy, direction="outgoing")` — what this function calls (trace internal flow)
- `lsp_xref(call_hierarchy, direction="both")` — both directions in one shot, useful for hub functions
- Pattern: `inspect(definition)` to reach the target → `call_hierarchy(incoming)` for entry points → `outgoing` for internal dependencies — a full data-flow picture

### Type hierarchy (interfaces / inheritance)

- `lsp_xref(type_hierarchy, direction="supertypes")` — parent classes / implemented interfaces
- `lsp_xref(type_hierarchy, direction="subtypes")` — all implementations / subclasses
- Pattern: `inspect(type_definition)` to jump to the type → `type_hierarchy(subtypes)` to enumerate implementations → `document_symbol` on each to decide which implementation to change

### Lightweight context (without reading full code)

- `lsp_inspect(hover)` — type, docs, and signature summary; quick confirmation of parameter or return types
- `lsp_inspect(signature_help)` — parameter hints at a call site; useful for diagnosing argument order / type mismatches
- `lsp_inspect(type_definition)` — jump to the "type" of a symbol (`definition` jumps to the symbol itself)

### Diagnostics strategy

- Scope diagnostics with `file_paths=[...]` to the files you care about, avoiding cross-file noise on large repos
- Handle by severity: error first, then warning, then hint / info as appropriate
- Diagnostics do not replace tests — if runtime behavior changed, run the relevant tests

### Edit strategy (picking the right action saves a lot of churn)

- Rename a symbol → `lsp_edit(rename)`: LSP renames safely across files; do not search-and-replace with `replace_range`
- Multiple edits in one file → `lsp_edit(replace_range, edits=[{old_string,new_string}, ...])` batched in one call
- Single in-place edit → `lsp_edit(replace_range, patch="@@ ctx\n-old\n+new")`; the patch form carries context and is safer
- Semantic fix (missing import, simple quickfix) → `lsp_edit(code_action, only=["quickfix"])`
- Format after edits → `lsp_edit(format)`

### Structure helpers

- `lsp_structure(folding_range)` — folding regions for understanding nested structure
- `lsp_structure(semantic_tokens)` — semantic highlight tokens; rarely needed

### Execution and testing

- `code_run(project_cmd)` — real shell / build / scripts
- `code_run_test(test_func, test_pkg)` — run a single Go test function; faster than whole-package `go test`

### Advanced combinations (beyond basic A–E)

- Full data-flow: `call_hierarchy(both)` collects both directions in one call, saving a round compared to separate incoming / outgoing runs
- Root-cause hunt: `ast_search` (shape) + `diagnostics` (compile error) + `call_hierarchy(incoming)` (trigger path)

### Picking a tool

- Know `file:line:col` → inspect / xref family (pinpoint operations)
- Only have a keyword or pattern → grep family (scan)
- Need cross-file structure → structure family (outline)
- About to modify → edit family (structural edits)
- Need to verify → `file.diagnostics` + `code_run_test`$LSPSEED$,
    '{"tags_has": ["refactor", "rename", "trace", "call hierarchy", "type hierarchy", "find references", "implementations", "bug", "重构", "重写", "改名", "重命名", "调用链", "被谁调用", "谁调用", "调用关系", "哪里调用", "类型层次", "继承体系", "接口的实现", "有几个实现", "影响面", "影响哪", "会不会影响", "牵一发", "排查", "追查", "追踪", "调试", "根因", "定位问题", "找引用", "查引用", "哪里用到", "哪里引用", "找用法", "用法", "报错", "编译错", "编译失败", "bug", "修 bug", "修复"]}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'main/claude-style'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- lsp_basics_zh · dynamic · ordinal 30
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'lsp_basics_zh', 'dynamic', 30,
    $LSPSEED$LSP 工具链 · 所有 Agent 必读：

你有 11 个仓库感知工具，覆盖四类动作：搜索、理解、修改、验证。**禁止**只用 `lsp_grep + lsp_file` 两件套，也不要用 `code_run` 去拼 shell 替代已有的专用工具。

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
5. 每个任务必须组合使用至少 4 种 LSP 工具$LSPSEED$,
    '{"language": "zh"}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'main/claude-style'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- lsp_advanced_zh · dynamic · ordinal 40
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'lsp_advanced_zh', 'dynamic', 40,
    $LSPSEED$LSP 工具链 · 高级调试（中文）：

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
- 要验证 → `file.diagnostics` + `code_run_test`$LSPSEED$,
    '{"language": "zh", "tags_has": ["refactor", "rename", "trace", "call hierarchy", "type hierarchy", "find references", "implementations", "bug", "重构", "重写", "改名", "重命名", "调用链", "被谁调用", "谁调用", "调用关系", "哪里调用", "类型层次", "继承体系", "接口的实现", "有几个实现", "影响面", "影响哪", "会不会影响", "牵一发", "排查", "追查", "追踪", "调试", "根因", "定位问题", "找引用", "查引用", "哪里用到", "哪里引用", "找用法", "用法", "报错", "编译错", "编译失败", "修 bug", "修复"]}'::jsonb, TRUE
FROM public.prompt_templates WHERE prompt_key = 'main/claude-style'
ON CONFLICT (template_id, section_key) DO NOTHING;

COMMIT;
