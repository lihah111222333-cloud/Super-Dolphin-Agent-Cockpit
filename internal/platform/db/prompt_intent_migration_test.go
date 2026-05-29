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

func TestPromptIntentMigration0101MarksOnlyKnownSeedRecallGlobal(t *testing.T) {
	t.Parallel()

	ctx, conn, schema := setupPromptIntentMigrationTest(t)
	seedID := insertPromptIntentTemplate(t, ctx, conn, "main/general-zh", "system.seed", "system.seed", `[]`)
	insertPromptIntentSection(t, ctx, conn, seedID, "recall_lsp_basics", "lsp-basics")
	userID := insertPromptIntentTemplate(t, ctx, conn, "user/recall", "user", "user", `["scope.cwd:/repo/user"]`)
	insertPromptIntentSection(t, ctx, conn, userID, "recall_user", "user-topic")

	applyPromptIntentMigration0101(t, ctx, conn)

	if !promptIntentTemplateHasTag(t, ctx, conn, "main/general-zh", "scope.global") {
		t.Fatal("known seed recall template did not gain scope.global")
	}
	if promptIntentTemplateHasTag(t, ctx, conn, "user/recall", "scope.global") {
		t.Fatal("project-scoped user recall gained scope.global")
	}
	if !promptIntentTableExists(t, ctx, conn, schema, "prompt_intent_drafts") {
		t.Fatal("prompt_intent_drafts table was not created")
	}
	if promptIntentIndexExists(t, ctx, conn, schema, "idx_prompt_sections_recall_topic") {
		t.Fatal("old global recall unique index still exists")
	}
	if !promptIntentIndexExists(t, ctx, conn, schema, "idx_prompt_sections_recall_topic_lookup") {
		t.Fatal("scoped recall lookup index was not created")
	}
}

func TestPromptIntentMigration0101MarksExistingSeedTemplatesGlobal(t *testing.T) {
	t.Parallel()

	ctx, conn, _ := setupPromptIntentMigrationTest(t)
	insertPromptIntentTemplate(t, ctx, conn, "main/code-review", "system.seed", "user", `["代码审查"]`)
	insertPromptIntentTemplate(t, ctx, conn, "user/custom", "user", "user", `["custom"]`)

	applyPromptIntentMigration0101(t, ctx, conn)

	if !promptIntentTemplateHasTag(t, ctx, conn, "main/code-review", "scope.global") {
		t.Fatal("existing seed template did not gain scope.global")
	}
	if promptIntentTemplateHasTag(t, ctx, conn, "user/custom", "scope.global") {
		t.Fatal("custom template gained scope.global")
	}
}

func TestPromptIntentMigration0101QuarantinesUnscopedNonSeedRecall(t *testing.T) {
	t.Parallel()

	ctx, conn, _ := setupPromptIntentMigrationTest(t)
	userID := insertPromptIntentTemplate(t, ctx, conn, "user/recall", "user", "user", `[]`)
	insertPromptIntentSection(t, ctx, conn, userID, "recall_user", "user-topic")

	applyPromptIntentMigration0101(t, ctx, conn)

	if promptIntentTemplateEnabled(t, ctx, conn, "user/recall") {
		t.Fatal("unscoped user recall template stayed enabled")
	}
	if !promptIntentTemplateHasTag(t, ctx, conn, "user/recall", "quarantine:unscoped-recall") {
		t.Fatal("unscoped user recall template did not gain quarantine tag")
	}
	if promptIntentTemplateHasTag(t, ctx, conn, "user/recall", "scope.global") {
		t.Fatal("unscoped user recall template gained scope.global")
	}
}

func TestPromptIntentMigration0101QuarantineDoesNotBlockDDLOrSeedScope(t *testing.T) {
	t.Parallel()

	ctx, conn, schema := setupPromptIntentMigrationTest(t)
	seedID := insertPromptIntentTemplate(t, ctx, conn, "main/general-zh", "system.seed", "system.seed", `[]`)
	insertPromptIntentSection(t, ctx, conn, seedID, "recall_lsp_basics", "lsp-basics")
	userID := insertPromptIntentTemplate(t, ctx, conn, "user/recall", "user", "user", `[]`)
	insertPromptIntentSection(t, ctx, conn, userID, "recall_user", "user-topic")

	applyPromptIntentMigration0101(t, ctx, conn)

	if promptIntentIndexExists(t, ctx, conn, schema, "idx_prompt_sections_recall_topic") {
		t.Fatal("old global recall unique index still exists")
	}
	if !promptIntentIndexExists(t, ctx, conn, schema, "idx_prompt_sections_recall_topic_lookup") {
		t.Fatal("new recall lookup index was not created")
	}
	if !promptIntentTableExists(t, ctx, conn, schema, "prompt_intent_drafts") {
		t.Fatal("prompt_intent_drafts table was not created")
	}
	if !promptIntentTemplateHasTag(t, ctx, conn, "main/general-zh", "scope.global") {
		t.Fatal("known seed recall template did not gain scope.global")
	}
	if promptIntentTemplateEnabled(t, ctx, conn, "user/recall") {
		t.Fatal("unscoped user recall template stayed enabled")
	}
}

func TestPromptIntentMigration0101QuarantinesUnknownRecallSectionInsideSeedTemplate(t *testing.T) {
	t.Parallel()

	ctx, conn, _ := setupPromptIntentMigrationTest(t)
	seedID := insertPromptIntentTemplate(t, ctx, conn, "main/general-zh", "system.seed", "system.seed", `[]`)
	insertPromptIntentSection(t, ctx, conn, seedID, "recall_lsp_basics", "lsp-basics")
	insertPromptIntentSection(t, ctx, conn, seedID, "recall_unknown", "unknown-topic")

	applyPromptIntentMigration0101(t, ctx, conn)

	if promptIntentTemplateEnabled(t, ctx, conn, "main/general-zh") {
		t.Fatal("seed template with unknown recall section stayed enabled")
	}
	if !promptIntentTemplateHasTag(t, ctx, conn, "main/general-zh", "quarantine:unscoped-recall") {
		t.Fatal("seed template with unknown recall section did not gain quarantine tag")
	}
	if promptIntentTemplateHasTag(t, ctx, conn, "main/general-zh", "scope.global") {
		t.Fatal("seed template with unknown recall section gained scope.global")
	}
}

func setupPromptIntentMigrationTest(t *testing.T) (context.Context, *pgx.Conn, string) {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("DATABASE_URL is required for DB-backed prompt intent migration tests")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect DATABASE_URL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	var schema string
	var schemaIdent string
	for attempt := 0; attempt < 10; attempt++ {
		schema = fmt.Sprintf("prompt_intent_0101_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), attempt)
		schemaIdent = pgx.Identifier{schema}.Sanitize()
		if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err == nil {
			break
		} else if !strings.Contains(err.Error(), "duplicate key value") || attempt == 9 {
			t.Fatalf("create test schema: %v", err)
		}
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+schemaIdent); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	createPromptIntentMigrationFixture(t, ctx, conn)
	return ctx, conn, schema
}

func createPromptIntentMigrationFixture(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	_, err := conn.Exec(ctx, `
CREATE TABLE prompt_templates (
    id BIGSERIAL PRIMARY KEY,
    prompt_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL DEFAULT '',
    variables JSONB NOT NULL DEFAULT '{}'::jsonb,
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    description TEXT NOT NULL DEFAULT '',
    when_to_use TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    manually_edited BOOLEAN NOT NULL DEFAULT FALSE,
    match_when JSONB NOT NULL DEFAULT '{}'::jsonb,
    priority INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE prompt_template_sections (
    id BIGSERIAL PRIMARY KEY,
    template_id BIGINT NOT NULL,
    section_key TEXT NOT NULL,
    region TEXT NOT NULL DEFAULT 'dynamic',
    ordinal INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL DEFAULT '',
    enable_when JSONB,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trigger_type TEXT NOT NULL DEFAULT 'always',
    recall_topic TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX idx_prompt_sections_recall_topic
    ON prompt_template_sections (recall_topic)
    WHERE trigger_type = 'recall' AND recall_topic <> '';
`)
	if err != nil {
		t.Fatalf("create migration fixture: %v", err)
	}
}

func insertPromptIntentTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, createdBy, updatedBy, tags string) int64 {
	t.Helper()

	var id int64
	err := conn.QueryRow(ctx, `
INSERT INTO prompt_templates (prompt_key, created_by, updated_by, tags)
VALUES ($1, $2, $3, $4::jsonb)
RETURNING id
`, promptKey, createdBy, updatedBy, tags).Scan(&id)
	if err != nil {
		t.Fatalf("insert prompt template %s: %v", promptKey, err)
	}
	return id
}

func insertPromptIntentSection(t *testing.T, ctx context.Context, conn *pgx.Conn, templateID int64, sectionKey, recallTopic string) {
	t.Helper()

	_, err := conn.Exec(ctx, `
INSERT INTO prompt_template_sections (template_id, section_key, trigger_type, recall_topic, body)
VALUES ($1, $2, 'recall', $3, 'body')
`, templateID, sectionKey, recallTopic)
	if err != nil {
		t.Fatalf("insert prompt section %s/%s: %v", sectionKey, recallTopic, err)
	}
}

func applyPromptIntentMigration0101(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	if err := execPromptIntentMigration0101(t, ctx, conn); err != nil {
		t.Fatalf("apply 0101 migration: %v", err)
	}
}

func execPromptIntentMigration0101(t *testing.T, ctx context.Context, conn *pgx.Conn) error {
	t.Helper()
	_, err := conn.Exec(ctx, readMigrationFixture(t, "0101_prompt_intent_drafts.sql"))
	return err
}

func promptIntentTemplateHasTag(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, tag string) bool {
	t.Helper()

	var exists bool
	err := conn.QueryRow(ctx, `SELECT tags ? $2 FROM prompt_templates WHERE prompt_key = $1`, promptKey, tag).Scan(&exists)
	if err != nil {
		t.Fatalf("query prompt template tag %s/%s: %v", promptKey, tag, err)
	}
	return exists
}

func promptIntentTemplateEnabled(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) bool {
	t.Helper()

	var enabled bool
	err := conn.QueryRow(ctx, `SELECT enabled FROM prompt_templates WHERE prompt_key = $1`, promptKey).Scan(&enabled)
	if err != nil {
		t.Fatalf("query prompt template enabled %s: %v", promptKey, err)
	}
	return enabled
}

func promptIntentIndexExists(t *testing.T, ctx context.Context, conn *pgx.Conn, schema, indexName string) bool {
	t.Helper()

	var exists bool
	err := conn.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
      FROM pg_indexes
     WHERE schemaname = $1
       AND indexname = $2
)
`, schema, indexName).Scan(&exists)
	if err != nil {
		t.Fatalf("query index %s: %v", indexName, err)
	}
	return exists
}

func promptIntentTableExists(t *testing.T, ctx context.Context, conn *pgx.Conn, schema, tableName string) bool {
	t.Helper()

	var exists bool
	err := conn.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
      FROM information_schema.tables
     WHERE table_schema = $1
       AND table_name = $2
)
`, schema, tableName).Scan(&exists)
	if err != nil {
		t.Fatalf("query table %s: %v", tableName, err)
	}
	return exists
}
