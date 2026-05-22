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
		if migrationAddsBuiltinPromptBody(string(data)) {
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
	normalized := strings.ToLower(sql)
	if !assignsPromptTextBody(sql) && !insertsSectionBody(sql) {
		return false
	}
	return referencesBuiltinPromptMigration(normalized)
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
	return (containsLargeDollarQuotedLiteral(sql) || containsLargeSingleQuotedLiteral(sql)) &&
		(strings.Contains(normalized, "prompt_template_sections") ||
			strings.Contains(normalized, "insert into public.prompt_template_sections") ||
			strings.Contains(normalized, "insert into prompt_template_sections"))
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
