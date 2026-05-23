package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type promptCleanupTemplate struct {
	PromptKey      string
	CreatedBy      string
	UpdatedBy      string
	ManuallyEdited bool
	PromptText     string
	Enabled        *bool
}

type promptCleanupTemplateState struct {
	CreatedBy      string
	UpdatedBy      string
	ManuallyEdited bool
	PromptText     string
	Enabled        *bool
}

type promptRoutingFixture struct {
	input             string
	expectedPromptKey string
	note              string
	enabled           *bool
}

func setupPromptTemplateCleanupMigrationTest(t *testing.T) (context.Context, *pgx.Conn, string) {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("DATABASE_URL is required for DB-backed prompt template cleanup migration tests")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect DATABASE_URL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	schema := fmt.Sprintf("prompt_cleanup_0105_test_%d", time.Now().UnixNano())
	schemaIdent := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+schemaIdent); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	createPromptTemplateCleanupMigrationFixture(t, ctx, conn)
	return ctx, conn, schema
}

func createPromptTemplateCleanupMigrationFixture(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	_, err := conn.Exec(ctx, `
CREATE TABLE prompt_templates (
    id BIGSERIAL PRIMARY KEY,
    prompt_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL DEFAULT '',
    variables JSONB,
    tags JSONB,
    description TEXT NOT NULL DEFAULT '',
    when_to_use TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    manually_edited BOOLEAN NOT NULL DEFAULT FALSE,
    match_when JSONB,
    priority INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE prompt_template_sections (
    id BIGSERIAL PRIMARY KEY,
    template_id BIGINT NOT NULL REFERENCES prompt_templates(id) ON DELETE CASCADE,
    section_key TEXT NOT NULL,
    region TEXT NOT NULL DEFAULT 'dynamic',
    ordinal INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL DEFAULT '',
    enable_when JSONB,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trigger_type TEXT NOT NULL DEFAULT 'always',
    recall_topic TEXT NOT NULL DEFAULT '',
    UNIQUE(template_id, section_key)
);
CREATE TABLE prompt_routing_tests (
    id BIGSERIAL PRIMARY KEY,
    input TEXT NOT NULL UNIQUE,
    expected_prompt_key TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`)
	if err != nil {
		t.Fatalf("create prompt cleanup migration fixture: %v", err)
	}
}

func insertPromptCleanupTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, template promptCleanupTemplate) int64 {
	t.Helper()

	promptText := template.PromptText
	if promptText == "" {
		promptText = "prompt text for " + template.PromptKey
	}
	enabled := true
	if template.Enabled != nil {
		enabled = *template.Enabled
	}

	var id int64
	err := conn.QueryRow(ctx, `
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, prompt_text, tags, enabled, manually_edited, created_by, updated_by
) VALUES ($1, $1, 'main', $2, '["scope.global"]'::jsonb, $3, $4, $5, $6)
RETURNING id
`, template.PromptKey, promptText, enabled, template.ManuallyEdited, template.CreatedBy, template.UpdatedBy).Scan(&id)
	if err != nil {
		t.Fatalf("insert prompt template %s: %v", template.PromptKey, err)
	}
	return id
}

func insertRuntimeVisiblePromptCleanupTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) {
	t.Helper()

	insertPromptCleanupTemplate(t, ctx, conn, promptCleanupTemplate{
		PromptKey: promptKey,
		CreatedBy: "system.seed",
		UpdatedBy: "system.seed",
	})
}

func insertPromptCleanupSection(t *testing.T, ctx context.Context, conn *pgx.Conn, templateID int64, sectionKey, body string) {
	t.Helper()

	_, err := conn.Exec(ctx, `
INSERT INTO prompt_template_sections (template_id, section_key, region, ordinal, body, enabled)
VALUES ($1, $2, 'static', 0, $3, TRUE)
`, templateID, sectionKey, body)
	if err != nil {
		t.Fatalf("insert prompt section %s: %v", sectionKey, err)
	}
}

func insertPromptRoutingFixture(t *testing.T, ctx context.Context, conn *pgx.Conn, row promptRoutingFixture) {
	t.Helper()

	enabled := true
	if row.enabled != nil {
		enabled = *row.enabled
	}
	_, err := conn.Exec(ctx, `
INSERT INTO prompt_routing_tests (input, expected_prompt_key, note, enabled)
VALUES ($1, $2, $3, $4)
`, row.input, row.expectedPromptKey, row.note, enabled)
	if err != nil {
		t.Fatalf("insert prompt routing fixture %q: %v", row.input, err)
	}
}

func applyPromptTemplateCleanupMigration0105(t *testing.T, ctx context.Context, conn *pgx.Conn, schema string) {
	t.Helper()

	body := readMigrationFixture(t, promptTemplateDeleteUnusedBuiltinSeedsMigrationName)
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, body, promptTemplateDeleteUnusedBuiltinSeedsMigrationName)
}

func applyPromptTemplateCleanupSQLBlock(t *testing.T, ctx context.Context, conn *pgx.Conn, schema, sql, name string) {
	t.Helper()

	if _, err := conn.Exec(ctx, rewritePromptCleanupPublicSchema(sql, schema)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

func rewritePromptCleanupPublicSchema(sql, schema string) string {
	schemaPrefix := pgx.Identifier{schema}.Sanitize() + "."
	return strings.ReplaceAll(sql, "public.", schemaPrefix)
}

func truncatePromptCleanupTemplates(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	if _, err := conn.Exec(ctx, `TRUNCATE prompt_template_sections, prompt_templates RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate prompt cleanup templates: %v", err)
	}
}

func protectedPromptCleanupCreatedBy(promptKey string) string {
	switch {
	case promptKey == "examples/sections-demo":
		return "system"
	case strings.HasPrefix(promptKey, "test/"):
		return "test-seed"
	case promptKey == "main/general-en":
		return "test-seed"
	default:
		return "system.seed"
	}
}

func requirePromptCleanupTemplateMissing(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) {
	t.Helper()

	var count int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM prompt_templates WHERE prompt_key = $1`, promptKey).Scan(&count); err != nil {
		t.Fatalf("count prompt template %s: %v", promptKey, err)
	}
	if count != 0 {
		t.Fatalf("prompt template %s count = %d, want 0", promptKey, count)
	}
}

func requirePromptCleanupTemplateExists(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) {
	t.Helper()

	var count int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM prompt_templates WHERE prompt_key = $1`, promptKey).Scan(&count); err != nil {
		t.Fatalf("count prompt template %s: %v", promptKey, err)
	}
	if count != 1 {
		t.Fatalf("prompt template %s count = %d, want 1", promptKey, count)
	}
}

func requirePromptCleanupTemplateState(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string, want promptCleanupTemplateState) {
	t.Helper()

	var gotCreatedBy string
	var gotUpdatedBy string
	var gotManuallyEdited bool
	var gotPromptText string
	var gotEnabled bool
	err := conn.QueryRow(ctx, `
SELECT created_by, updated_by, manually_edited, prompt_text, enabled
FROM prompt_templates
WHERE prompt_key = $1
`, promptKey).Scan(&gotCreatedBy, &gotUpdatedBy, &gotManuallyEdited, &gotPromptText, &gotEnabled)
	if err != nil {
		t.Fatalf("query prompt template %s: %v", promptKey, err)
	}
	requirePromptCleanupStringIfSet(t, promptKey, "created_by", gotCreatedBy, want.CreatedBy)
	requirePromptCleanupStringIfSet(t, promptKey, "updated_by", gotUpdatedBy, want.UpdatedBy)
	requirePromptCleanupBool(t, promptKey, "manually_edited", gotManuallyEdited, want.ManuallyEdited)
	requirePromptCleanupStringIfSet(t, promptKey, "prompt_text", gotPromptText, want.PromptText)
	requirePromptCleanupOptionalBool(t, promptKey, "enabled", gotEnabled, want.Enabled)
}

func requirePromptCleanupStringIfSet(t *testing.T, promptKey, field, got, want string) {
	t.Helper()

	if want == "" {
		return
	}
	if got != want {
		t.Fatalf("prompt template %s %s = %q, want %q", promptKey, field, got, want)
	}
}

func requirePromptCleanupBool(t *testing.T, promptKey, field string, got, want bool) {
	t.Helper()

	if got != want {
		t.Fatalf("prompt template %s %s = %v, want %v", promptKey, field, got, want)
	}
}

func requirePromptCleanupOptionalBool(t *testing.T, promptKey, field string, got bool, want *bool) {
	t.Helper()

	if want == nil {
		return
	}
	if got != *want {
		t.Fatalf("prompt template %s %s = %v, want %v", promptKey, field, got, *want)
	}
}

func requirePromptCleanupSectionBodyCount(t *testing.T, ctx context.Context, conn *pgx.Conn, body string, want int) {
	t.Helper()

	var count int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM prompt_template_sections WHERE body = $1`, body).Scan(&count); err != nil {
		t.Fatalf("count prompt section body %q: %v", body, err)
	}
	if count != want {
		t.Fatalf("prompt section body %q count = %d, want %d", body, count, want)
	}
}

func requirePromptCleanupTemplateIDSectionCount(t *testing.T, ctx context.Context, conn *pgx.Conn, templateID int64, want int) {
	t.Helper()

	var count int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM prompt_template_sections WHERE template_id = $1`, templateID).Scan(&count); err != nil {
		t.Fatalf("count prompt sections for template %d: %v", templateID, err)
	}
	if count != want {
		t.Fatalf("prompt sections for template %d count = %d, want %d", templateID, count, want)
	}
}

func requirePromptCleanupTemplateSection(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, sectionKey, bodyFragment string) {
	t.Helper()

	var body string
	err := conn.QueryRow(ctx, `
SELECT s.body
FROM prompt_template_sections s
JOIN prompt_templates t ON t.id = s.template_id
WHERE t.prompt_key = $1 AND s.section_key = $2
`, promptKey, sectionKey).Scan(&body)
	if err != nil {
		t.Fatalf("query prompt section %s/%s: %v", promptKey, sectionKey, err)
	}
	if !strings.Contains(body, bodyFragment) {
		t.Fatalf("prompt section %s/%s body = %q, want fragment %q", promptKey, sectionKey, body, bodyFragment)
	}
}

func requirePromptCleanupTemplateMissingRuntimeScope(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) {
	t.Helper()

	var hasRuntimeScope bool
	err := conn.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
      FROM prompt_templates t,
           jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) tag(value)
     WHERE t.prompt_key = $1
       AND (tag.value = 'scope.global' OR tag.value LIKE 'scope.cwd:%')
)
`, promptKey).Scan(&hasRuntimeScope)
	if err != nil {
		t.Fatalf("query runtime scope for %s: %v", promptKey, err)
	}
	if hasRuntimeScope {
		t.Fatalf("prompt template %s has runtime scope tag after rollback restore", promptKey)
	}
}

func requireNoEnabledRoutingRowsForPromptKey(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) {
	t.Helper()

	var count int
	err := conn.QueryRow(ctx, `
SELECT COUNT(*)
FROM prompt_routing_tests
WHERE enabled = TRUE AND expected_prompt_key = $1
`, promptKey).Scan(&count)
	if err != nil {
		t.Fatalf("count routing rows for %s: %v", promptKey, err)
	}
	if count != 0 {
		t.Fatalf("enabled routing rows for %s = %d, want 0", promptKey, count)
	}
}

func requirePromptRoutingMissing(t *testing.T, ctx context.Context, conn *pgx.Conn, input string) {
	t.Helper()

	var count int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM prompt_routing_tests WHERE input = $1`, input).Scan(&count); err != nil {
		t.Fatalf("count routing input %q: %v", input, err)
	}
	if count != 0 {
		t.Fatalf("routing input %q count = %d, want 0", input, count)
	}
}

func requirePromptRoutingState(t *testing.T, ctx context.Context, conn *pgx.Conn, input, expectedPromptKey string, wantEnabled bool) {
	t.Helper()

	var gotKey string
	var gotEnabled bool
	err := conn.QueryRow(ctx, `
SELECT expected_prompt_key, enabled
FROM prompt_routing_tests
WHERE input = $1
`, input).Scan(&gotKey, &gotEnabled)
	if err != nil {
		t.Fatalf("query routing input %q: %v", input, err)
	}
	if gotKey != expectedPromptKey || gotEnabled != wantEnabled {
		t.Fatalf("routing input %q = key:%q enabled:%v, want key:%q enabled:%v",
			input, gotKey, gotEnabled, expectedPromptKey, wantEnabled)
	}
}

func requireEnabledRoutingRowsPointToRuntimeVisibleTemplates(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	rows, err := conn.Query(ctx, `
SELECT r.input, r.expected_prompt_key
FROM prompt_routing_tests r
LEFT JOIN prompt_templates t
  ON t.prompt_key = r.expected_prompt_key
 AND t.enabled = TRUE
 AND EXISTS (
     SELECT 1
       FROM jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) tag(value)
      WHERE tag.value = 'scope.global' OR tag.value LIKE 'scope.cwd:%'
 )
WHERE r.enabled = TRUE
  AND t.id IS NULL
ORDER BY r.input
`)
	if err != nil {
		t.Fatalf("query routing runtime visibility: %v", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var input, expectedPromptKey string
		if err := rows.Scan(&input, &expectedPromptKey); err != nil {
			t.Fatalf("scan routing runtime visibility: %v", err)
		}
		missing = append(missing, fmt.Sprintf("%s -> %s", input, expectedPromptKey))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate routing runtime visibility: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("enabled routing rows point to non-runtime-visible prompt templates: %s", strings.Join(missing, ", "))
	}
}

func extractRollbackSQLBlock(t *testing.T, marker string) string {
	t.Helper()

	content := readMigrationFixture(t, "ROLLBACK.md")
	markerIndex := strings.Index(content, marker)
	if markerIndex < 0 {
		t.Fatalf("ROLLBACK.md missing marker %q", marker)
	}
	afterMarker := content[markerIndex:]
	fenceStart := strings.Index(afterMarker, "```sql")
	if fenceStart < 0 {
		t.Fatalf("ROLLBACK.md marker %q missing sql fence", marker)
	}
	bodyStart := fenceStart + len("```sql")
	fenceEnd := strings.Index(afterMarker[bodyStart:], "```")
	if fenceEnd < 0 {
		t.Fatalf("ROLLBACK.md marker %q has unterminated sql fence", marker)
	}
	return strings.TrimSpace(afterMarker[bodyStart : bodyStart+fenceEnd])
}

func boolPtr(v bool) *bool {
	return &v
}
