# Prompt Inventory Whitelist

Source: Task 1 of `2026-05-22-内置提示词优化方案.md`.

Inventory command used:

```bash
{
  rg -o "'[^']+'" migrations/*.sql | sed -E "s/^.*'([^']+)'.*$/\1/";
  rg -o '"prompt_key"[[:space:]]*:[[:space:]]*"[^"]+"' internal/platform/shared/builtinprompts/assets/templates/*.json | sed -E 's/^.*"prompt_key"[[:space:]]*:[[:space:]]*"([^"]+)".*$/\1/';
} | rg '^(main/|test/|examples/|coder/|frontend$|sql/expert$)' | rg -v '%' | sort -u
```

SQL LIKE cleanup patterns are not whitelist keys. They are tracked only as cleanup patterns:

- `main/%`
- `test/%`

## Core Registry

Registry core is fixed to the repo-owned system prompt assets below. `main/general-en` is not registry core; it is a legacy cleanup candidate unless a separate English product mode is implemented.

| prompt_key | whitelist status | source / evidence | notes |
|---|---|---|---|
| `main/default` | Keep in builtin registry core | `internal/platform/shared/builtinprompts/assets/templates/main-default.json`; `migrations/0104_disable_registry_backed_system_seed_prompts.sql` whitelist | Thin fallback only. DB system seed row is disabled when registry-backed. |
| `main/general-zh` | Keep in builtin registry core | `internal/platform/shared/builtinprompts/assets/templates/main-general-zh.json`; `migrations/0104_disable_registry_backed_system_seed_prompts.sql` whitelist | Chinese main system prompt. DB system seed row is disabled when registry-backed. |

## Default Visible Expert Templates

This table is the target system-owned default developer expert subset. Target size is at most 8. Do not use total `available_experts` return count as this metric, because DAG and enterprise presets can enter the same runtime discovery surface when enabled and populated with `when_to_use`.

| prompt_key | whitelist status | source / evidence | notes |
|---|---|---|---|
| `main/code-task` | Keep as default developer expert | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Consolidated implementation/refactor/explain/test expert. |
| `main/code-review` | Keep as default developer expert | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Dedicated review methodology and findings structure. |
| `main/code-debug` | Keep as default developer expert | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Dedicated debugging/root-cause workflow. |
| `main/sql` | Keep as default developer expert | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Canonical SQL/schema/migration/sqlc expert. Keep this rather than `sql/expert`. |
| `main/git-ops` | Target keep; roster repair required if absent after full migration chain | `migrations/0039_prompt_templates_production_v2.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | `0040` resets `main/%` and does not recreate this row; future repair must create a system-owned runtime-visible row if retained. |
| `main/docs` | Target keep; roster repair required if absent after full migration chain | `migrations/0039_prompt_templates_production_v2.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | `0040` resets `main/%` and does not recreate this row; future repair must create a system-owned runtime-visible row if retained. |
| `main/planning` | Keep as default developer expert | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Engineering implementation plans, phased specs, dependencies, and risk mapping. |
| `main/orchestrator` | Keep as default developer expert with constrained scope | `migrations/0036_seed_orchestrator_prompt.sql`; `migrations/0040_prompt_templates_production_v3.sql` | Needs `when_to_use` / metadata repair to become visible under the current `available_experts` predicate. Do not use it as DAG designer. |

## DAG and Enterprise Presets

These keys remain DB prompt templates or workflow presets, not builtin registry assets. They can be default optional runtime-visible templates only when their metadata and scope tags match the relevant discovery surface.

| prompt_key | whitelist status | source / evidence | notes |
|---|---|---|---|
| `main/dag_designer_zh` | Keep as DAG primary entry | `migrations/0084_seed_dag_designer_prompt_zh.sql`; `migrations/0090_refresh_dag_designer_prompt_run_id_signature.sql` | Chinese DAG designer. Must discover resources instead of inventing `agent_key`, `command_ref`, or sharedfile paths. |
| `main/dag_designer_en` | Keep hidden by default | `migrations/0085_seed_dag_designer_prompt_en.sql`; `migrations/0090_refresh_dag_designer_prompt_run_id_signature.sql` | English mirror. Do not make default-visible until language/mode filtering exists; no registry migration. |
| `main/morning_briefer` | Keep as enterprise preset | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Normalize metadata, `when_to_use`, and output schema. |
| `main/pr_summarizer` | Keep as enterprise preset | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Normalize metadata, `when_to_use`, and output schema. |
| `main/weekly_reviewer` | Keep as enterprise preset | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Normalize metadata, `when_to_use`, and output schema. |
| `main/data_inspector` | Keep as enterprise preset | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Normalize metadata, `when_to_use`, and output schema. |
| `main/health_reporter` | Keep as enterprise preset | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Normalize metadata, `when_to_use`, and output schema. |
| `main/source_monitor` | Keep as enterprise preset | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Normalize metadata, `when_to_use`, and output schema. |
| `main/note_organizer` | Keep as enterprise preset | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Normalize metadata, `when_to_use`, and output schema. |
| `main/todo_prioritizer` | Keep as enterprise preset | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Normalize metadata, `when_to_use`, and output schema. |
| `main/email_drafter` | Optional keep; low default priority | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Business email drafting preset. Keep out of registry. |

## Deleted Builtin Seeds / Marketplace Examples

These keys are not retained as system-owned default builtins. Cleanup migrations must protect user-created or manually edited rows and should delete only system-owned, non-manually-edited seed rows.

| prompt_key | cleanup status | source / evidence | notes |
|---|---|---|---|
| `examples/sections-demo` | Delete system seed | `migrations/0050_seed_prompt_template_sections_example.sql`; `migrations/0102_remove_demo_prompt_templates.sql` | Demo/example asset; not production whitelist. |
| `test/auto-high` | Delete system seed | `migrations/0056_seed_simple_match_when_demo.sql`; cleanup pattern `test/%` | Test/demo prompt; real key listed separately from SQL LIKE pattern. |
| `test/auto-low` | Delete system seed | `migrations/0056_seed_simple_match_when_demo.sql`; cleanup pattern `test/%` | Test/demo prompt; real key listed separately from SQL LIKE pattern. |
| `test/greeting` | Delete system seed | `migrations/0052_seed_test_prompts_with_sections.sql`; cleanup pattern `test/%` | Test/demo prompt; real key listed separately from SQL LIKE pattern. |
| `test/match-by-cwd` | Delete system seed | `migrations/0055_seed_match_when_test_prompts.sql`; cleanup pattern `test/%` | Test/demo prompt; real key listed separately from SQL LIKE pattern. |
| `test/match-by-language` | Delete system seed | `migrations/0055_seed_match_when_test_prompts.sql`; cleanup pattern `test/%` | Test/demo prompt; real key listed separately from SQL LIKE pattern. |
| `test/strict-review` | Delete system seed | `migrations/0052_seed_test_prompts_with_sections.sql`; cleanup pattern `test/%` | Test/demo prompt; real key listed separately from SQL LIKE pattern. |
| `main/3` | Delete historical seed | `migrations/0038_prompt_templates_production_seed.sql` | Early deprecated key. |
| `main/prompt` | Delete historical seed | `migrations/0038_prompt_templates_production_seed.sql` | Early deprecated key. |
| `main/debug` | Delete historical seed | `migrations/0038_prompt_templates_production_seed.sql` | Replaced by `main/code-debug`. |
| `main/claude-style` | Delete historical seed | `migrations/0057_seed_claude_style_prompt.sql`; `migrations/0095_rename_claude_style_templates.sql` | Provider identity legacy key. |
| `main/claude-style-zh` | Delete historical seed | `migrations/0092_seed_main_claude_style_zh.sql`; `migrations/0095_rename_claude_style_templates.sql` | Renamed to `main/general-zh`; not retained as separate key. |
| `main/general-en` | Delete or disable legacy system seed | `migrations/0095_rename_claude_style_templates.sql` | Legacy cleanup candidate, not registry core. Do not default-enable unless English product mode is implemented. |
| `main/writing` | Delete system seed or move to marketplace/example | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Generic writing is not the default developer/DAG/enterprise mainline. |
| `main/translate` | Delete system seed or move to marketplace/example | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Generic translation is not the default developer/DAG/enterprise mainline. |
| `main/research` | Delete system seed or move to marketplace/example | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Generic research is not the default developer/DAG/enterprise mainline. |
| `main/brainstorm` | Delete system seed or move to marketplace/example | `migrations/0040_prompt_templates_production_v3.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Generic ideation is not the default developer/DAG/enterprise mainline. |
| `main/paper_summarizer` | Delete system seed or reintroduce via marketplace/example later | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Academic paper consumption is outside the default enterprise/DAG chain. |
| `main/topic_curator` | Delete system seed or reintroduce via marketplace/example later | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Generic topic curation is outside the default enterprise/DAG chain. |
| `main/learning_card` | Delete system seed or reintroduce via marketplace/example later | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Personal learning scenario. |
| `main/trip_briefer` | Delete system seed or reintroduce via marketplace/example later | `migrations/0087_seed_prompt_template_skill_cards.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Consumer travel scenario. |
| `main/code-generate` | Merge/delete duplicate code expert | `migrations/0039_prompt_templates_production_v2.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Consolidate into `main/code-task`; do not retain as a separate default expert. |
| `main/code-refactor` | Merge/delete duplicate code expert | `migrations/0039_prompt_templates_production_v2.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Consolidate into `main/code-task`; do not retain as a separate default expert. |
| `main/code-test` | Merge/delete duplicate code expert | `migrations/0039_prompt_templates_production_v2.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Consolidate into `main/code-task`; do not retain as a separate default expert. |
| `main/code-explain` | Merge/delete duplicate code expert | `migrations/0039_prompt_templates_production_v2.sql`; `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Consolidate into `main/code-task`; do not retain as a separate default expert. |

## Non-Main Runtime or Historical References

These keys appeared in the Task 1 inventory command but are not `main/*` whitelist entries.

| prompt_key | classification | source / evidence | notes |
|---|---|---|---|
| `coder/prompt` | Non-main runtime or historical reference | `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Mentioned by `when_to_use` fill logic only in the checked sources; do not move into registry. |
| `frontend` | Non-main runtime or historical reference | `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Mentioned by `when_to_use` fill logic only in the checked sources; do not move into registry. |
| `sql/expert` | Historical/example SQL reference | `migrations/0100_seed_recall_packs_and_when_to_use.sql` | Historical/example reference only. Do not add this as a template; default SQL expert remains `main/sql`. |
