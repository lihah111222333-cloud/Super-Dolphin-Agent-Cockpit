-- 0062_add_lsp_sections_enabled_tools_gate.sql — 给 main/claude-style 的 4 段 LSP
-- section 的 enable_when 追加 enabled_tools_has 过滤，避免在没有 LSP MCP 工具的
-- agent 上注入 LSP 指令（那些 agent 读了也用不上，反而会误导它调用不存在的工具）。
--
-- 追加的 gate（OR 语义，任一 LSP 工具可用即通过）：
--   {"enabled_tools_has":["lsp_grep","lsp_structure","lsp_inspect","lsp_xref",
--                         "lsp_file","lsp_edit","lsp_completion"]}
--
-- AND 组合到原有 gate 上：
--   lsp_basics      : (none) → enabled_tools_has
--   lsp_advanced    : tags_has → tags_has + enabled_tools_has
--   lsp_basics_zh   : language → language + enabled_tools_has
--   lsp_advanced_zh : language + tags_has → language + tags_has + enabled_tools_has
--
-- 依赖：section 级 enable_when 的 enabled_tools_has 支持来自本仓库同一 PR 的
-- prompt 包改动（internal/module/prompt/enable_when.go:sectionEnableKeyMatches +
-- matchEnabledToolsHas）。旧代码对 enabled_tools_has 是 fail-closed 安全降级：
-- 所有 4 段 LSP section 都会被过滤，不会误触发。
--
-- 幂等：使用 jsonb || 合并语义，多次 apply 只会覆盖同名 key，不会产生冲突。
--
-- 回滚：
--   用 0061 里保存的 enable_when 原值重置。或直接 DELETE + 重跑 0061。

BEGIN;

UPDATE public.prompt_template_sections s
   SET enable_when = COALESCE(s.enable_when, '{}'::jsonb)
                   || '{"enabled_tools_has":["lsp_grep","lsp_structure","lsp_inspect","lsp_xref","lsp_file","lsp_edit","lsp_completion"]}'::jsonb,
       updated_at = NOW()
  FROM public.prompt_templates t
 WHERE t.id = s.template_id
   AND t.prompt_key = 'main/claude-style'
   AND s.section_key IN ('lsp_basics','lsp_advanced','lsp_basics_zh','lsp_advanced_zh');

COMMIT;
