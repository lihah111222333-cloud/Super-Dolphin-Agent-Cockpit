package prompt

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	_ "modernc.org/sqlite"
)

func TestPromptTemplateMappingsIncludeManuallyEdited(t *testing.T) {
	ts := time.Unix(1, 0).UTC().UnixMilli()

	tests := []struct {
		name string
		got  PromptTemplate
	}{
		{name: "get", got: mappedGetPromptTemplate(ts)},
		{name: "list", got: mappedListPromptTemplate(ts)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPromptTemplateMapping(t, tt.got)
		})
	}
}

func mappedGetPromptTemplate(ts int64) PromptTemplate {
	return fromGetTemplate(sqlc.GetPromptTemplateRow{
		ID: 1, PromptKey: "main/morning_briefer", Title: "Morning Briefer", AgentKey: "morning_briefer",
		PromptText: "Prepare a briefing.", Variables: []byte(`{}`), Tags: []byte(`["briefing"]`),
		Description: "Daily briefing prompt.", WhenToUse: "Use for daily briefings.", Enabled: 1,
		ManuallyEdited: 1, MatchWhen: []byte(`{"cwd_prefix":"/repo"}`), Priority: 30,
		CreatedBy: "system.seed", UpdatedBy: "user", CreatedAt: ts, UpdatedAt: ts,
	})
}

func mappedListPromptTemplate(ts int64) PromptTemplate {
	return fromListTemplate(sqlc.ListPromptTemplatesRow{
		ID: 2, PromptKey: "main/paper_summarizer", Title: "Paper Summarizer", AgentKey: "paper_summarizer",
		PromptText: "Summarize a paper.", Variables: []byte(`{}`), Tags: []byte(`["research"]`),
		Description: "Research summary prompt.", WhenToUse: "Use for paper summaries.", Enabled: 1,
		ManuallyEdited: 1, MatchWhen: []byte(`{"language":"en"}`), Priority: 20,
		CreatedBy: "system.seed", UpdatedBy: "user", CreatedAt: ts, UpdatedAt: ts,
	})
}

func assertPromptTemplateMapping(t *testing.T, got PromptTemplate) {
	t.Helper()
	if !got.ManuallyEdited {
		t.Fatalf("ManuallyEdited = false, want true")
	}
	if !json.Valid(got.Variables) {
		t.Fatalf("Variables must remain valid JSON: %s", string(got.Variables))
	}
	if !json.Valid(got.Tags) {
		t.Fatalf("Tags must remain valid JSON: %s", string(got.Tags))
	}
	if strings.TrimSpace(got.WhenToUse) == "" {
		t.Fatalf("WhenToUse must be mapped")
	}
	if !json.Valid(got.MatchWhen) {
		t.Fatalf("MatchWhen must remain valid JSON: %s", string(got.MatchWhen))
	}
	if got.Priority <= 0 {
		t.Fatalf("Priority = %d, want positive mapped priority", got.Priority)
	}
}

func TestUpsertPromptTemplatePersistsRoutingMetadata(t *testing.T) {
	t.Parallel()

	db := newPromptTestDB(t)
	store := NewStore(db)

	got, err := store.Upsert(context.Background(), PromptTemplate{
		PromptKey:      "main/sql",
		Title:          "SQL Expert",
		AgentKey:       "main",
		PromptText:     "SQL body",
		Variables:      json.RawMessage(`{}`),
		Tags:           json.RawMessage(`["sql"]`),
		Description:    "SQL description",
		WhenToUse:      "Use when SQL workflow guidance is needed.",
		Enabled:        true,
		ManuallyEdited: true,
		MatchWhen:      json.RawMessage(`{"cwd_prefix":"/repo"}`),
		Priority:       70,
		CreatedBy:      "tester",
		UpdatedBy:      "tester",
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got.WhenToUse != "Use when SQL workflow guidance is needed." {
		t.Fatalf("WhenToUse = %q", got.WhenToUse)
	}
	if string(got.MatchWhen) != `{"cwd_prefix":"/repo"}` {
		t.Fatalf("MatchWhen = %s", got.MatchWhen)
	}
	if got.Priority != 70 {
		t.Fatalf("Priority = %d", got.Priority)
	}
	if !got.Enabled || !got.ManuallyEdited {
		t.Fatalf("enabled/manually_edited = %v/%v, want true/true", got.Enabled, got.ManuallyEdited)
	}
	assertPromptUpdatePreservesIdentity(t, store, got)
}

func assertPromptUpdatePreservesIdentity(t *testing.T, store Store, got *PromptTemplate) {
	t.Helper()
	updated, err := store.Upsert(context.Background(), PromptTemplate{
		PromptKey:      got.PromptKey,
		Title:          "SQL Expert Updated",
		AgentKey:       "main",
		PromptText:     "Updated SQL body",
		Variables:      json.RawMessage(`{"dialect":"sqlite"}`),
		Tags:           json.RawMessage(`["sql","scope.global"]`),
		Description:    "Updated SQL description",
		WhenToUse:      "Use for SQLite workflow guidance.",
		Enabled:        true,
		ManuallyEdited: true,
		MatchWhen:      json.RawMessage(`{"cwd_prefix":"/repo/sql"}`),
		Priority:       80,
		CreatedBy:      "replacement-must-not-win",
		UpdatedBy:      "reviewer",
	})
	if err != nil {
		t.Fatalf("second Upsert() error = %v", err)
	}
	if updated.ID != got.ID || !updated.CreatedAt.Equal(got.CreatedAt) || updated.CreatedBy != got.CreatedBy {
		t.Fatalf("update replaced identity fields: before=%+v after=%+v", got, updated)
	}
	if updated.Title != "SQL Expert Updated" || updated.UpdatedBy != "reviewer" || updated.Priority != 80 {
		t.Fatalf("update fields = %+v, want updated values", updated)
	}
}

func TestGetSectionByRecallTopicUsesSQLiteScopedRecall(t *testing.T) {
	t.Parallel()

	db := newPromptTestDB(t)
	store := NewStore(db)
	templateID := insertPromptTemplate(t, db, "test/prompt-recall", "Recall Integration", true, []string{"scope.cwd:/repo/a"})
	disabledTemplateID := insertPromptTemplate(t, db, "test/prompt-recall-disabled", "Disabled Recall Integration", false, []string{"scope.cwd:/repo/a"})
	insertRecallSection(t, db, templateID, "enabled_recall", "Recall body", true, "recall", "topic_enabled")
	insertRecallSection(t, db, templateID, "disabled_recall", "Disabled body", false, "recall", "topic_disabled")
	insertRecallSection(t, db, disabledTemplateID, "disabled_parent_recall", "Disabled parent body", true, "recall", "topic_disabled_parent")
	insertRecallSection(t, db, templateID, "always_topic", "Always body", true, "always", "topic_always")

	got, err := store.GetSectionByRecallTopic(context.Background(), "/repo/a", "topic_enabled")
	if err != nil {
		t.Fatalf("GetSectionByRecallTopic(enabled recall) error = %v", err)
	}
	if got != "Recall body" {
		t.Fatalf("body = %q, want Recall body", got)
	}
	assertRecallTopicsNotFound(t, store, "/repo/a", "topic_disabled", "topic_disabled_parent", "topic_always", "topic_missing")
}

func TestGetSectionByRecallTopicPrefersCWDOverGlobal(t *testing.T) {
	t.Parallel()

	db := newPromptTestDB(t)
	store := NewStore(db)
	globalID := insertPromptTemplate(t, db, "test/prompt-global", "Global Recall", true, []string{"scope.global"})
	cwdID := insertPromptTemplate(t, db, "test/prompt-cwd", "CWD Recall", true, []string{"scope.cwd:/repo/a"})
	insertRecallSection(t, db, globalID, "global_recall", "Global body", true, "recall", "topic_shared")
	insertRecallSection(t, db, cwdID, "cwd_recall", "CWD body", true, "recall", "topic_shared")

	got, err := store.GetSectionByRecallTopic(context.Background(), "/repo/a", "topic_shared")
	if err != nil {
		t.Fatalf("GetSectionByRecallTopic(shared) error = %v", err)
	}
	if got != "CWD body" {
		t.Fatalf("body = %q, want CWD body", got)
	}
}

func TestGetSectionByRecallTopicRequiresCWD(t *testing.T) {
	t.Parallel()

	store := NewStore(newPromptTestDB(t))
	_, err := store.GetSectionByRecallTopic(context.Background(), " ", "context_budget")
	if err == nil {
		t.Fatal("GetSectionByRecallTopic() error = nil, want cwd required")
	}
	if !strings.Contains(err.Error(), "cwd") {
		t.Fatalf("error = %v, want cwd required", err)
	}
}

func TestGetSectionByRecallTopicWrapsNoRowsAsNotFound(t *testing.T) {
	t.Parallel()

	store := NewStore(newPromptTestDB(t))
	_, err := store.GetSectionByRecallTopic(context.Background(), "/repo/a", "missing")
	if err == nil {
		t.Fatal("GetSectionByRecallTopic() error = nil, want wrapped not found")
	}
	if !platformdb.IsNotFound(err) {
		t.Fatalf("error = %v, want platformdb.IsNotFound", err)
	}
}

func TestGetSectionByRecallTopicFallsBackToBuiltinRegistry(t *testing.T) {
	t.Parallel()

	store := NewStore(newPromptTestDB(t))
	got, err := store.GetSectionByRecallTopic(context.Background(), "/repo/a", "lsp-basics")
	if err != nil {
		t.Fatalf("GetSectionByRecallTopic() error = %v", err)
	}
	if !strings.Contains(got, "LSP") {
		t.Fatalf("builtin recall body = %q, want LSP guidance", got)
	}
}

func TestListSectionsByTemplateIDUsesSQLiteQuery(t *testing.T) {
	t.Parallel()

	db := newPromptTestDB(t)
	store := NewStore(db)
	templateID := insertPromptTemplate(t, db, "test/sections", "Sections", true, []string{"scope.global"})
	insertPromptSection(t, db, templateID, "workflow", "dynamic", 10, "Workflow body", true, "keyword", "")
	insertPromptSection(t, db, templateID, "identity", "static", 0, "Identity body", true, "always", "")
	insertPromptSection(t, db, templateID, "disabled", "static", 1, "Disabled body", false, "always", "")
	insertPromptSection(t, db, templateID, "recall", "dynamic", 2, "Recall body", true, "recall", "topic")

	got, err := store.ListSectionsByTemplateID(context.Background(), templateID)
	if err != nil {
		t.Fatalf("ListSectionsByTemplateID() error = %v", err)
	}
	assertListSectionsResult(t, got)
}

func assertListSectionsResult(t *testing.T, got []PromptTemplateSection) {
	t.Helper()
	if len(got) != 2 {
		t.Fatalf("len(sections) = %d, want 2", len(got))
	}
	if got[0].SectionKey != "identity" || got[0].Region != "static" || got[0].Ordinal != 0 || got[0].Body != "Identity body" {
		t.Fatalf("first section = %+v, want identity static ordinal 0", got[0])
	}
	if got[1].SectionKey != "workflow" || got[1].TriggerType != "keyword" || !got[1].Enabled {
		t.Fatalf("second section = %+v, want enabled keyword workflow", got[1])
	}
}

func TestListSectionsByTemplateIDWrapsQueryError(t *testing.T) {
	t.Parallel()

	db := newPromptTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	store := NewStore(db)

	_, err := store.ListSectionsByTemplateID(context.Background(), 42)
	if err == nil {
		t.Fatal("ListSectionsByTemplateID() error = nil, want wrapped error")
	}
	if !strings.Contains(err.Error(), "list_sections") {
		t.Fatalf("error = %v, want list_sections context", err)
	}
}

func assertRecallTopicsNotFound(t *testing.T, store Store, cwd string, topics ...string) {
	t.Helper()
	for _, topic := range topics {
		_, err := store.GetSectionByRecallTopic(context.Background(), cwd, topic)
		if !platformdb.IsNotFound(err) {
			t.Fatalf("GetSectionByRecallTopic(%q) error = %v, want not found", topic, err)
		}
	}
}

func newPromptTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	mustExecPromptSQL(t, db, `
CREATE TABLE prompt_templates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  prompt_key TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  agent_key TEXT NOT NULL,
  tool_name TEXT NOT NULL DEFAULT '',
  prompt_text TEXT NOT NULL,
  variables TEXT NOT NULL DEFAULT '{}',
  tags TEXT NOT NULL DEFAULT '[]',
  description TEXT NOT NULL DEFAULT '',
  when_to_use TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  manually_edited INTEGER NOT NULL DEFAULT 0,
  match_when TEXT NOT NULL DEFAULT '{}',
  priority INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL DEFAULT '',
  updated_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE prompt_template_sections (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  template_id INTEGER NOT NULL,
  section_key TEXT NOT NULL,
  region TEXT NOT NULL,
  ordinal INTEGER NOT NULL DEFAULT 0,
  body TEXT NOT NULL,
  enable_when TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  trigger_type TEXT NOT NULL DEFAULT '',
  recall_topic TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(template_id) REFERENCES prompt_templates(id) ON DELETE CASCADE
);`)
	return db
}

func insertPromptTemplate(t *testing.T, db *sql.DB, promptKey, title string, enabled bool, tags []string) int64 {
	t.Helper()
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	now := time.Unix(1, 0).UTC().UnixMilli()
	result, err := db.ExecContext(context.Background(), `
INSERT INTO prompt_templates (
  prompt_key, title, agent_key, tool_name, prompt_text, variables, tags,
  description, when_to_use, enabled, manually_edited, match_when, priority,
  created_by, updated_by, created_at, updated_at
) VALUES (?, ?, 'test', '', 'fallback', '{}', ?, '', '', ?, 0, '{}', 0, 'test', 'test', ?, ?)`,
		promptKey, title, string(tagsJSON), boolInt64(enabled), now, now)
	if err != nil {
		t.Fatalf("insert prompt template(%s): %v", promptKey, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func insertRecallSection(t *testing.T, db *sql.DB, templateID int64, sectionKey, body string, enabled bool, triggerType, recallTopic string) {
	t.Helper()
	insertPromptSection(t, db, templateID, sectionKey, "dynamic", 0, body, enabled, triggerType, recallTopic)
}

func insertPromptSection(t *testing.T, db *sql.DB, templateID int64, sectionKey, region string, ordinal int64, body string, enabled bool, triggerType, recallTopic string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
INSERT INTO prompt_template_sections (
  template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic
) VALUES (?, ?, ?, ?, ?, '{}', ?, ?, ?)`,
		templateID, sectionKey, region, ordinal, body, boolInt64(enabled), triggerType, recallTopic)
	if err != nil {
		t.Fatalf("insert prompt_template_sections(%s): %v", sectionKey, err)
	}
}

func mustExecPromptSQL(t *testing.T, db *sql.DB, body string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), body); err != nil {
		t.Fatalf("exec sqlite schema: %v", err)
	}
}
