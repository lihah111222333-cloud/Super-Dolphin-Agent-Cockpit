package prompt

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestCreateAndUpsertPromptTemplateNormalizeEmptyMatchWhenForSQLite(t *testing.T) {
	t.Parallel()

	db := openPromptSQLite(t)
	createPromptTemplateTable(t, db)
	s := &store{q: sqlc.New(db)}

	createInput := promptUpsertInput()
	createInput.PromptKey = "main/create-empty-match-when"
	created, err := s.CreatePromptTemplate(context.Background(), createInput)
	if err != nil {
		t.Fatalf("CreatePromptTemplate() unexpected error: %v", err)
	}
	assertEmptyPromptMatchWhen(t, created.MatchWhen)

	gotCreated, err := s.Get(context.Background(), createInput.PromptKey)
	if err != nil {
		t.Fatalf("Get(created) unexpected error: %v", err)
	}
	assertEmptyPromptMatchWhen(t, gotCreated.MatchWhen)

	upsertInput := promptUpsertInput()
	upsertInput.PromptKey = "main/upsert-empty-match-when"
	upsertInput.MatchWhen = []byte(" \t\r\n ")
	upserted, err := s.Upsert(context.Background(), upsertInput)
	if err != nil {
		t.Fatalf("Upsert() unexpected error: %v", err)
	}
	assertEmptyPromptMatchWhen(t, upserted.MatchWhen)

	gotUpserted, err := s.Get(context.Background(), upsertInput.PromptKey)
	if err != nil {
		t.Fatalf("Get(upserted) unexpected error: %v", err)
	}
	assertEmptyPromptMatchWhen(t, gotUpserted.MatchWhen)
}

func TestInsertPromptVersionWritesSQLiteRequiredTimestamps(t *testing.T) {
	t.Parallel()

	db := openPromptSQLite(t)
	createPromptVersionTable(t, db)
	s := &store{q: sqlc.New(db)}
	sourceUpdatedAt := time.Unix(1_700_000_000, 0).UTC()

	id, err := s.InsertVersion(context.Background(), PromptTemplateVersion{
		PromptKey:       "main/sqlite-version",
		Title:           "SQLite version",
		AgentKey:        "codex",
		PromptText:      "body",
		Variables:       json.RawMessage(`{}`),
		Tags:            json.RawMessage(`[]`),
		Description:     "snapshot",
		Enabled:         true,
		CreatedBy:       "test",
		UpdatedBy:       "test",
		SourceUpdatedAt: &sourceUpdatedAt,
	})
	if err != nil {
		t.Fatalf("InsertVersion() error = %v", err)
	}
	if id == 0 {
		t.Fatal("InsertVersion() id = 0, want persisted row")
	}
	var createdAt, archivedAt int64
	if err := db.QueryRow(`SELECT created_at, archived_at FROM prompt_versions WHERE id = ?`, id).Scan(&createdAt, &archivedAt); err != nil {
		t.Fatalf("read prompt_versions timestamps: %v", err)
	}
	if createdAt <= 0 || archivedAt <= 0 {
		t.Fatalf("timestamps created_at=%d archived_at=%d, want both > 0", createdAt, archivedAt)
	}
}

func assertEmptyPromptMatchWhen(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if !json.Valid(raw) {
		t.Fatalf("match_when = %q, want valid JSON", raw)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("match_when = %q, want object JSON: %v", raw, err)
	}
	if len(decoded) != 0 {
		t.Fatalf("match_when = %s, want empty object", raw)
	}
}

func createPromptVersionTable(t *testing.T, db *sql.DB) {
	t.Helper()
	execPromptSQL(t, db, `CREATE TABLE prompt_versions (
		id INTEGER PRIMARY KEY,
		prompt_key TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		agent_key TEXT NOT NULL DEFAULT '',
		tool_name TEXT NOT NULL DEFAULT '',
		prompt_text TEXT NOT NULL,
		variables TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(variables)),
		tags TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tags)),
		description TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
		created_by TEXT NOT NULL DEFAULT '',
		updated_by TEXT NOT NULL DEFAULT '',
		source_updated_at INTEGER,
		created_at INTEGER NOT NULL,
		archived_at INTEGER NOT NULL
	);`)
}
