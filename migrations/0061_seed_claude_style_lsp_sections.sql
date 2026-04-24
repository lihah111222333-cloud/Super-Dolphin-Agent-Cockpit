-- 0061_seed_claude_style_lsp_sections.sql — 给 main/claude-style 补两段 LSP
-- 工具链使用规范：基础（lsp_tooling_basics） + 高级调试（lsp_tooling_advanced）。
--
-- 设计：
--   - 两段都是 region='static'：内容不依赖运行时变量，进 --system-prompt，
--     可被 prompt cache 复用。
--   - 放在 tool_preferences (ordinal 50) 与 style (ordinal 60) 之间，作为
--     tool_preferences 的扩展，而不是替代。
--       ordinal 55 → lsp_tooling_basics  (日常工作流 / 常用 action)
--       ordinal 56 → lsp_tooling_advanced (调用链 / 类型层次 / 高阶 edit / 诊断)
--   - enable_when=NULL：对所有会话启用。若以后要按 language 或 project type
--     切换 advanced 段，改成 dynamic + enable_when 即可，ordinal 不动。
--
-- 幂等性：ON CONFLICT (template_id, section_key) DO NOTHING —— 可重复 apply，
-- 不会覆盖运维/用户在 UI 里调过的内容。
--
-- 回滚：
--   DELETE FROM public.prompt_template_sections s
--    USING (SELECT id FROM public.prompt_templates WHERE prompt_key='main/claude-style') t
--    WHERE s.template_id = t.id
--      AND s.section_key IN ('lsp_tooling_basics','lsp_tooling_advanced');
--
-- Depends on: 0049 (建表), 0057 (main/claude-style 模板), 0058 (首批 sections)

BEGIN;

-- ═══ 基础段 · ordinal 55 ════════════════════════════════════════════════
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'lsp_tooling_basics', 'static', 55,
$body$LSP toolchain · basics:

Repository-aware tools cover four kinds of actions — search, understand, edit, verify. Reach for them first; use code_run only for real shell / build / test, not as a wrapper around search or read.

Tools and primary actions:
- lsp_grep(text_search, glob=...) — text / regex search, path-filterable
- lsp_grep(ast_search, language=...) — match functions, types, expressions by syntax shape
- lsp_structure(document_symbol | workspace_symbol) — file outline / project-wide symbol lookup
- lsp_inspect(definition | implementation) — jump from file:line:col to the definition / all implementations
- lsp_xref(references, verbosity="compact") — reference scan; returns func_start / func_end for each hit
- lsp_file(read_file, offset=, limit=) — paged read by 1-based line numbers
- lsp_file(diagnostics, file_paths=[...]) — batch compile / type diagnostics
- lsp_edit(replace_range, patch=... | edits=[{old_string,new_string}, ...]) — precise in-place edit
- code_run / code_run_test — real shell, build, scripts, Go test functions

Standard paths:
- Review: grep to locate → inspect(definition) to read the symbol → xref(references) for impact → read_file to study → conclude
- Modify: locate → xref for impact → read_file for context → edit to apply → diagnostics to check compile → code_run_test / code_run when runtime behavior matters

High-yield shortcuts:
- ast_search / inspect(definition|implementation) / xref(references, compact) all return func_start / func_end — call read_file(offset=func_start, limit=func_end-func_start+1) to read the full function directly, skip document_symbol
- Fire independent read-only calls (multiple definition / references / read_file) in parallel; serialize only when one depends on another
- After multi-file edits, run diagnostics(file_paths=[...]) once instead of per-file

Do not:
- Invoke code_run to run grep / rg / cat / head / tail / sed / awk / find / ls — these all have dedicated tools
- Modify exported symbols or public APIs without first running xref to see the impact
- Claim completion without running diagnostics (and relevant tests); diagnostics only catches compile / type errors, not runtime behavior
- Default to the lsp_grep + lsp_file two-tool combo and never touch lsp_inspect / lsp_xref / lsp_structure$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'main/claude-style'
ON CONFLICT (template_id, section_key) DO NOTHING;

-- ═══ 高级调试段 · ordinal 56 ════════════════════════════════════════════
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
SELECT id, 'lsp_tooling_advanced', 'static', 56,
$body$LSP toolchain · advanced debugging:

Beyond the basics, the actions below are for deep tracing, cross-abstraction navigation, and complex refactors.

Call-chain tracing:
- lsp_xref(call_hierarchy, direction="incoming") — who calls this function (walk back to triggers)
- lsp_xref(call_hierarchy, direction="outgoing") — what this function calls (trace internal flow)
- lsp_xref(call_hierarchy, direction="both") — both directions in one shot (for hub functions)
- Pattern: inspect(definition) to reach the target → call_hierarchy(incoming) for entry points → outgoing for internal dependencies → full data-flow view

Type hierarchy (interface / inheritance code):
- lsp_xref(type_hierarchy, direction="supertypes") — parent classes / implemented interfaces
- lsp_xref(type_hierarchy, direction="subtypes") — all implementations / subclasses
- Pattern: inspect(type_definition) to jump to the type → type_hierarchy(subtypes) to enumerate implementations → document_symbol on each to decide which one to change

Lightweight context inspection (cheaper than read_file):
- lsp_inspect(hover) — type, docs, and signature summary; quick sanity-check of parameter or return types
- lsp_inspect(signature_help) — parameter hints at a call site; useful for diagnosing argument order / type mismatches
- lsp_inspect(type_definition) — jump to the type of a symbol (definition jumps to the symbol itself, not its type)

Diagnostics strategy:
- lsp_file(diagnostics) without file_paths → full workspace summary; with file_paths=[...] → scoped, less noise on large repos
- Handle by severity: error first, then warning, then hint / info
- Diagnostics do not replace tests — if runtime behavior changed, run the relevant tests

Edit strategy (picking the right action saves significant churn):
- Rename a symbol → lsp_edit(rename): LSP renames safely across files; do not search-and-replace with replace_range
- Multiple edits in one file → lsp_edit(replace_range, edits=[{old_string,new_string}, ...]) batched
- Single in-place edit → lsp_edit(replace_range, patch="@@ ctx\n-old\n+new"); the patch form carries context and is safer
- Semantic fix (missing import, simple quickfix) → lsp_edit(code_action, only=["quickfix"])
- Formatting → lsp_edit(format) after edits to normalize style

Structure helpers:
- lsp_structure(folding_range) — folding regions, helpful for grasping nested structure
- lsp_structure(semantic_tokens) — semantic highlight tokens; rarely needed outside visualization

Execution and testing:
- code_run(project_cmd) — real shell / build / scripts
- code_run_test(test_func, test_pkg) — run a single Go test function; much faster than whole-package go test when iterating on one case

Advanced combinations:
- Interface three-step: definition → implementation (or type_hierarchy subtypes) → references — walk from interface to implementations to use sites
- Method-coverage diff: document_symbol on two files to catch "class A implements X/Y/Z, class B is missing Z"
- Full impact map: references (breadth) + call_hierarchy(both) (depth)
- Root-cause hunt: ast_search (shape match) + diagnostics (compile error) + call_hierarchy(incoming) (trigger path)

Picking a tool:
- Know file:line:col → inspect / xref family (pinpoint)
- Only have a keyword or pattern → grep family (scan)
- Need cross-file structure → structure family (outline)
- About to modify → edit family (structural edits)
- Need to verify → file.diagnostics + code_run_test$body$,
    NULL, TRUE
FROM public.prompt_templates WHERE prompt_key = 'main/claude-style'
ON CONFLICT (template_id, section_key) DO NOTHING;

COMMIT;
