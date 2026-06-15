package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	builtinPromptBodyMigrationMessage = "new builtin prompt bodies must live under internal/platform/shared/builtinprompts/assets, not migrations"
	builtinRegistryCutoverMigration   = 104
	rosterRepairMigrationName         = "0106_prompt_template_runtime_metadata.sql"
	rosterRepairCardMaxRunes          = 600
)

func TestBuiltinPromptBodiesDoNotReturnToNewMigrations(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}

	var violations []string
	for _, path := range paths {
		name := filepath.Base(path)
		if !isNewPromptBuiltinMigration(name) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if migrationAddsBuiltinPromptBodyForFile(name, string(data)) {
			violations = append(violations, fmt.Sprintf("%s: %s", name, builtinPromptBodyMigrationMessage))
		}
	}
	failIfViolations(t, violations)
}

func TestPromptBuiltinMigrationBodyDetectorAllowsMetadataDisableAndRename(t *testing.T) {
	t.Parallel()

	allowed := []string{
		`UPDATE public.prompt_templates SET enabled = FALSE, updated_by = 'system.registry-migration' WHERE prompt_key IN ('main/default') AND created_by IN ('system.seed', 'seed');`,
		`UPDATE public.prompt_templates SET prompt_key = 'main/general-zh', updated_by = 'system.seed' WHERE prompt_key = 'main/claude-style-zh';`,
		`UPDATE public.prompt_templates SET tags = tags || '["builtin:system"]'::jsonb WHERE created_by IN ('system.seed', 'seed');`,
	}
	for _, sql := range allowed {
		if migrationAddsBuiltinPromptBody(sql) {
			t.Fatalf("metadata/disable/rename migration was rejected: %s", sql)
		}
	}
}

func TestRegistryBackedSystemSeedRowsAreDisabledAfterCutover(t *testing.T) {
	content := readPromptMigration(t, "0104_disable_registry_backed_system_seed_prompts.sql")
	for _, marker := range []string{
		"('main/default')",
		"('main/general-zh')",
		"SET enabled = FALSE",
		"updated_by = 'system.registry-migration'",
		"t.created_by IN ('system.seed', 'seed')",
		"(t.updated_by IN ('system.seed', 'seed', 'migration') OR t.updated_by LIKE 'system.%')",
		"t.manually_edited = FALSE",
	} {
		if !strings.Contains(content, marker) {
			t.Fatalf("0104 missing registry-backed disable marker %q", marker)
		}
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsInlinePromptBodies(t *testing.T) {
	t.Parallel()

	sql := `
INSERT INTO public.prompt_templates (prompt_key, prompt_text, created_by, updated_by)
VALUES ('main/default', $prompt$You are Super-Dolphin.

Follow the repository instructions and keep this long builtin body out of migrations.
$prompt$, 'system.seed', 'system.seed');`
	if !migrationAddsBuiltinPromptBody(sql) {
		t.Fatal(builtinPromptBodyMigrationMessage)
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsSingleQuotedPromptBodies(t *testing.T) {
	t.Parallel()

	sql := `
UPDATE public.prompt_templates
SET prompt_text = 'You are Super-Dolphin.

Follow the repository instructions and keep this long builtin body out of migrations.'
WHERE prompt_key = 'main/default' AND created_by = 'system.seed';`
	if !migrationAddsBuiltinPromptBody(sql) {
		t.Fatal(builtinPromptBodyMigrationMessage)
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsSingleQuotedSectionBodies(t *testing.T) {
	t.Parallel()

	sql := `
INSERT INTO public.prompt_template_sections (template_id, section_key, body, trigger_type)
SELECT id, 'identity', 'You are Super-Dolphin.

Follow the repository instructions and keep this long builtin section body out of migrations.', 'always'
FROM public.prompt_templates
WHERE prompt_key = 'main/default' AND created_by = 'system.seed';`
	if !migrationAddsBuiltinPromptBody(sql) {
		t.Fatal(builtinPromptBodyMigrationMessage)
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsBuiltinRegistryPromptBodies(t *testing.T) {
	t.Parallel()

	cases := []string{
		`
		INSERT INTO public.prompt_templates (prompt_key, prompt_text, created_by, updated_by)
		VALUES ('main/default', 'You are Super-Dolphin.

		Follow the repository instructions and keep this long builtin body out of migrations.', 'builtin.registry', 'builtin.registry');`,
		`
		INSERT INTO public.prompt_template_sections (template_id, section_key, body, trigger_type)
		SELECT id, 'identity', 'You are Super-Dolphin.

		Follow the repository instructions and keep this long builtin section body out of migrations.', 'always'
		FROM public.prompt_templates
		WHERE prompt_key = 'main/default';`,
	}
	for _, sql := range cases {
		if !migrationAddsBuiltinPromptBody(sql) {
			t.Fatalf("%s\nSQL:\n%s", builtinPromptBodyMigrationMessage, sql)
		}
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsDAGDesignerPromptBodies(t *testing.T) {
	t.Parallel()

	sql := `
UPDATE public.prompt_templates
SET prompt_text = 'You are the DAG designer.

This long replacement body belongs in canonical prompt assets, not in a post-cutover SQL migration.
It must be rejected even when the row is updated by a migration rather than system.seed.'
WHERE prompt_key = 'main/dag_designer_zh'
  AND updated_by = 'migration:0106';`
	if !migrationAddsBuiltinPromptBody(sql) {
		t.Fatal("post-cutover DAG designer prompt body migration was allowed")
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsEnterprisePromptBodies(t *testing.T) {
	t.Parallel()

	sql := `
UPDATE public.prompt_templates
SET prompt_text = 'You are a morning briefing analyst.

This long enterprise preset body belongs in canonical prompt assets, not in a post-cutover SQL migration.
It must be rejected even when the migration does not mention the original seed author.'
WHERE prompt_key = 'main/morning_briefer'
  AND updated_by = 'migration:0106';`
	if !migrationAddsBuiltinPromptBody(sql) {
		t.Fatal("post-cutover enterprise prompt body migration was allowed")
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsMethodologyPromptBodies(t *testing.T) {
	t.Parallel()

	sql := `
UPDATE public.prompt_templates
SET prompt_text = 'You are a planning expert.

This long methodology body belongs in canonical prompt assets, not in a post-cutover SQL migration.
It must be rejected even when the migration only filters by prompt_key.'
WHERE prompt_key = 'main/planning'
  AND updated_by = 'migration:0106';`
	if !migrationAddsBuiltinPromptBody(sql) {
		t.Fatal("post-cutover methodology prompt body migration was allowed")
	}
}

func TestPromptBuiltinMigrationBodyDetectorAllows0106ShortRosterRepairCards(t *testing.T) {
	t.Parallel()

	sql := `
INSERT INTO public.prompt_templates (
    prompt_key, agent_key, title, tool_name, prompt_text, tags, enabled,
    description, when_to_use, manually_edited, created_by, updated_by
) VALUES
    (
        'main/git-ops',
        'git-ops',
        'Git 操作专家',
        '',
        'Git 操作专家：基于 diff、log、冲突或提交上下文，产出可验证的 git 操作建议；危险历史改写必须要求用户确认。',
        jsonb_build_array('scope.global','intent:expert','domain:developer'),
        TRUE,
        'Git 操作：diff、log、blame、commit、冲突和回滚建议。',
        '当需要解释 git diff/log/blame、写 commit message、处理冲突、revert 或 cherry-pick 时使用。',
        FALSE,
        'system.seed',
        'migration:0106'
    )
ON CONFLICT (prompt_key) DO NOTHING;`
	if migrationAddsBuiltinPromptBodyForFile(rosterRepairMigrationName, sql) {
		t.Fatalf("0106 short roster repair expert card was rejected:\n%s", sql)
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsRosterRepairCardsOutside0106(t *testing.T) {
	t.Parallel()

	sql := rosterRepairInsertSQL("main/git-ops", "Git 操作专家：短职责说明。")
	if !migrationAddsBuiltinPromptBodyForFile("0107_prompt_template_extra_roster.sql", sql) {
		t.Fatal("short roster repair card outside 0106 was allowed")
	}
}

func TestPromptBuiltinMigrationBodyDetectorRejectsUnsafeRosterRepairCards(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		sql  string
	}{
		{name: "default_core_prompt", sql: rosterRepairInsertSQL("main/default", "通用助手短职责说明。")},
		{name: "general_zh_core_prompt", sql: rosterRepairInsertSQL("main/general-zh", "中文通用助手短职责说明。")},
		{name: "dag_designer_prompt", sql: rosterRepairInsertSQL("main/dag_designer_zh", "DAG designer short card.")},
		{name: "enterprise_prompt", sql: rosterRepairInsertSQL("main/morning_briefer", "Enterprise workflow short card.")},
		{name: "unknown_key", sql: rosterRepairInsertSQL("main/unknown-expert", "Unknown expert short card.")},
		{name: "too_long", sql: rosterRepairInsertSQL("main/docs", strings.Repeat("长", rosterRepairCardMaxRunes+1))},
		{name: "provider_identity", sql: rosterRepairInsertSQL("main/git-ops", "You are Claude and must follow Anthropic tool rules.")},
		{name: "external_tool_protocol", sql: rosterRepairInsertSQL("main/git-ops", "Use tool_use JSON with mcp__git before answering.")},
		{name: "markdown_outline", sql: rosterRepairInsertSQL("main/docs", "# Docs Expert\\n\\n## Steps")},
		{name: "dollar_quoted_body", sql: rosterRepairDollarQuotedInsertSQL()},
		{name: "large_update_without_author_marker", sql: rosterRepairLargeUpdateSQL()},
		{name: "section_body", sql: rosterRepairSectionBodySQL()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !migrationAddsBuiltinPromptBodyForFile(rosterRepairMigrationName, tc.sql) {
				t.Fatalf("unsafe roster repair migration was allowed:\n%s", tc.sql)
			}
		})
	}
}

func rosterRepairInsertSQL(promptKey, promptText string) string {
	return fmt.Sprintf(`
INSERT INTO public.prompt_templates (prompt_key, prompt_text, enabled, tags, description, when_to_use, created_by, updated_by, manually_edited)
VALUES ('%s', '%s', TRUE, jsonb_build_array('scope.global','intent:expert'), 'short description', 'short when', 'system.seed', 'migration:0106', FALSE)
ON CONFLICT (prompt_key) DO NOTHING;`, promptKey, promptText)
}

func rosterRepairSectionBodySQL() string {
	return `
INSERT INTO public.prompt_template_sections (template_id, section_key, body)
SELECT id, 'identity', 'Short builtin section body.'
FROM public.prompt_templates
WHERE prompt_key = 'main/git-ops' AND created_by = 'system.seed';`
}

func rosterRepairDollarQuotedInsertSQL() string {
	return `
INSERT INTO public.prompt_templates (prompt_key, prompt_text, enabled, tags, description, when_to_use, created_by, updated_by, manually_edited)
VALUES ('main/git-ops', $card$You are Claude.

Use tool_use JSON before answering.$card$, TRUE, jsonb_build_array('scope.global','intent:expert'), 'short description', 'short when', 'system.seed', 'migration:0106', FALSE)
ON CONFLICT (prompt_key) DO NOTHING;`
}

func rosterRepairLargeUpdateSQL() string {
	return `
UPDATE public.prompt_templates
SET prompt_text = '` + strings.Repeat("restored docs expert body ", 8) + `'
WHERE prompt_key = 'main/docs';`
}

func TestPromptBuiltinMigrationBodyDetectorAllowsUserPromptBodies(t *testing.T) {
	t.Parallel()

	sql := `
	INSERT INTO public.prompt_templates (prompt_key, prompt_text, created_by, updated_by)
	VALUES ('user/imported/reference', 'This is user-owned imported reference material.

	It may be long, but it is not a builtin system prompt body.', 'rpc.prompts', 'rpc.prompts');`
	if migrationAddsBuiltinPromptBody(sql) {
		t.Fatalf("user-owned prompt body migration was rejected: %s", sql)
	}
}

func isNewPromptBuiltinMigration(name string) bool {
	match := regexp.MustCompile(`^(\d+)_.*\.sql$`).FindStringSubmatch(name)
	if match == nil {
		return false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return false
	}
	// Migrations before the cutover are historical DB-backed seeds. 0104 disables
	// the registry-backed rows; after that point, builtin bodies must stay in
	// builtinprompts/assets instead of returning to SQL migrations.
	return n >= builtinRegistryCutoverMigration
}

func migrationAddsBuiltinPromptBody(sql string) bool {
	return migrationAddsBuiltinPromptBodyForFile("", sql)
}

func migrationAddsBuiltinPromptBodyForFile(name, sql string) bool {
	sql = strings.ReplaceAll(sql, "\r\n", "\n")
	if name == rosterRepairMigrationName {
		sql = stripAllowedRosterRepairCardInserts(sql)
		sql = strip0106DAGDesignerPromptTextReplacementLiterals(sql)
	}
	if name == "0108_refresh_dag_designer_prompt_final_node_key.sql" {
		sql = strip0108DAGDesignerPromptTextReplacementLiterals(sql)
	}
	normalized := strings.ToLower(sql)
	if !assignsPromptTextBody(sql) && !insertsSectionBody(sql) {
		return false
	}
	return referencesBuiltinPromptMigration(normalized)
}

func stripAllowedRosterRepairCardInserts(sql string) string {
	insertPattern := regexp.MustCompile(`(?is)INSERT\s+INTO\s+(?:public\.)?prompt_templates\b.*?;`)
	return insertPattern.ReplaceAllStringFunc(sql, func(statement string) string {
		if allowedRosterRepairCardInsert(statement) {
			return ""
		}
		return statement
	})
}

func allowedRosterRepairCardInsert(statement string) bool {
	normalized := strings.ToLower(statement)
	required := []string{
		"prompt_text",
		"created_by",
		"updated_by",
		"manually_edited",
		"enabled",
		"tags",
		"when_to_use",
		"description",
		"system.seed",
		"migration:0106",
		"on conflict",
		"do nothing",
	}
	if !containsAll(normalized, required) ||
		strings.Contains(normalized, "prompt_template_sections") ||
		containsDollarQuotedLiteral(statement) {
		return false
	}
	keys := rosterRepairPromptKeys(statement)
	return rosterRepairPromptKeysAllowed(keys) && rosterRepairLiteralsAllowed(statement)
}

func containsAll(text string, needles []string) bool {
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}

func rosterRepairPromptKeysAllowed(keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	for _, key := range keys {
		if key != "main/git-ops" && key != "main/docs" {
			return false
		}
	}
	return true
}

func rosterRepairLiteralsAllowed(statement string) bool {
	for _, literal := range singleQuotedLiteralValues(statement) {
		if len([]rune(literal)) > rosterRepairCardMaxRunes || containsRosterRepairForbiddenText(literal) {
			return false
		}
	}
	return true
}

func rosterRepairPromptKeys(statement string) []string {
	matches := regexp.MustCompile(`'main/[^']+'`).FindAllString(statement, -1)
	keys := make([]string, 0, len(matches))
	for _, match := range matches {
		keys = append(keys, strings.Trim(match, "'"))
	}
	return keys
}

func singleQuotedLiteralValues(sql string) []string {
	matches := regexp.MustCompile(`(?s)'(?:''|[^'])*'`).FindAllString(sql, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		value := strings.TrimPrefix(strings.TrimSuffix(match, "'"), "'")
		value = strings.ReplaceAll(value, "''", "'")
		values = append(values, value)
	}
	return values
}

func containsDollarQuotedLiteral(sql string) bool {
	return regexp.MustCompile(`(?s)\$[A-Za-z0-9_]*\$.*?\$[A-Za-z0-9_]*\$`).MatchString(sql)
}

func containsRosterRepairForbiddenText(value string) bool {
	normalized := strings.ToLower(value)
	for _, forbidden := range []string{
		"you are claude",
		"anthropic",
		"openai",
		"chatgpt",
		"tool_use",
		"function_call",
		"mcp__",
		"<tool",
		"json schema",
		"launch_agent(",
		"\n",
		"# ",
		"##",
	} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

func referencesBuiltinPromptMigration(normalized string) bool {
	indicators := []string{
		"system.seed",
		"'seed'",
		"builtin.registry",
		"system.registry",
		"builtin:system",
		"main/default",
		"main/general-zh",
		"main/dag_designer_zh",
		"main/dag_designer_en",
		"main/morning_briefer",
		"main/pr_summarizer",
		"main/weekly_reviewer",
		"main/data_inspector",
		"main/email_drafter",
		"main/health_reporter",
		"main/source_monitor",
		"main/note_organizer",
		"main/todo_prioritizer",
		"main/planning",
		"main/code-review",
		"main/code-debug",
		"main/git-ops",
		"main/docs",
	}
	for _, indicator := range indicators {
		if strings.Contains(normalized, indicator) {
			return true
		}
	}
	return false
}

func assignsPromptTextBody(sql string) bool {
	normalized := strings.ToLower(sql)
	if !strings.Contains(normalized, "prompt_text") {
		return false
	}
	return containsLargeDollarQuotedLiteral(sql) ||
		containsLargeSingleQuotedLiteral(sql) ||
		strings.Contains(normalized, "insert into public.prompt_templates") ||
		strings.Contains(normalized, "insert into prompt_templates")
}

func insertsSectionBody(sql string) bool {
	normalized := strings.ToLower(sql)
	if !strings.Contains(normalized, "body") {
		return false
	}
	return strings.Contains(normalized, "prompt_template_sections") ||
		strings.Contains(normalized, "insert into public.prompt_template_sections") ||
		strings.Contains(normalized, "insert into prompt_template_sections")
}

func containsLargeDollarQuotedLiteral(sql string) bool {
	matches := regexp.MustCompile(`(?s)\$[A-Za-z0-9_]*\$.*?\$[A-Za-z0-9_]*\$`).FindAllString(sql, -1)
	for _, match := range matches {
		if len(match) >= 120 || strings.Contains(match, "\n\n") {
			return true
		}
	}
	return false
}

func containsLargeSingleQuotedLiteral(sql string) bool {
	matches := regexp.MustCompile(`(?s)'(?:''|[^'])*'`).FindAllString(sql, -1)
	for _, match := range matches {
		if len(match) >= 120 || strings.Contains(match, "\n\n") {
			return true
		}
	}
	return false
}
