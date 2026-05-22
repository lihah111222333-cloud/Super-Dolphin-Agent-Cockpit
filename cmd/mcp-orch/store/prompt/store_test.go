package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPromptTemplateMappingsIncludeManuallyEdited(t *testing.T) {
	ts := pgtype.Timestamptz{Time: time.Unix(1, 0).UTC(), Valid: true}

	tests := []struct {
		name string
		got  PromptTemplate
	}{
		{
			name: "get",
			got:  mappedGetPromptTemplate(ts),
		},
		{
			name: "list",
			got:  mappedListPromptTemplate(ts),
		},
		{
			name: "upsert",
			got:  mappedUpsertPromptTemplate(ts),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPromptTemplateMapping(t, tt.got)
		})
	}
}

func mappedGetPromptTemplate(ts pgtype.Timestamptz) PromptTemplate {
	return fromGetTemplate(sqlc.GetPromptTemplateRow{
		ID: 1, PromptKey: "main/morning_briefer", Title: "Morning Briefer", AgentKey: "morning_briefer",
		PromptText: "Prepare a briefing.", Variables: []byte(`{}`), Tags: []byte(`["briefing"]`),
		Description: "Daily briefing prompt.", WhenToUse: "Use for daily briefings.", Enabled: true,
		ManuallyEdited: true, MatchWhen: []byte(`{"cwd_prefix":"/repo"}`), Priority: 30,
		CreatedBy: "system.seed", UpdatedBy: "user", CreatedAt: ts, UpdatedAt: ts,
	})
}

func mappedListPromptTemplate(ts pgtype.Timestamptz) PromptTemplate {
	return fromListTemplate(sqlc.ListPromptTemplatesRow{
		ID: 2, PromptKey: "main/paper_summarizer", Title: "Paper Summarizer", AgentKey: "paper_summarizer",
		PromptText: "Summarize a paper.", Variables: []byte(`{}`), Tags: []byte(`["research"]`),
		Description: "Research summary prompt.", WhenToUse: "Use for paper summaries.", Enabled: true,
		ManuallyEdited: true, MatchWhen: []byte(`{"language":"en"}`), Priority: 20,
		CreatedBy: "system.seed", UpdatedBy: "user", CreatedAt: ts, UpdatedAt: ts,
	})
}

func mappedUpsertPromptTemplate(ts pgtype.Timestamptz) PromptTemplate {
	return fromUpsertTemplate(sqlc.UpsertPromptTemplateRow{
		ID: 3, PromptKey: "main/email_drafter", Title: "Email Drafter", AgentKey: "email_drafter",
		PromptText: "Draft an email.", Variables: []byte(`{}`), Tags: []byte(`["writing"]`),
		Description: "Email drafting prompt.", WhenToUse: "Use for email drafts.", Enabled: true,
		ManuallyEdited: true, MatchWhen: []byte(`{}`), Priority: 10,
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

	db := &capturePromptUpsertDB{row: []any{
		int64(9), "main/sql", "SQL Expert", "main", "",
		"SQL body", []byte(`{}`), []byte(`["sql"]`),
		"SQL description", "Use when SQL workflow guidance is needed.",
		true, true, []byte(`{"cwd_prefix":"/repo"}`), int32(70),
		"tester", "tester",
		pgtype.Timestamptz{Time: time.Unix(1, 0).UTC(), Valid: true},
		pgtype.Timestamptz{Time: time.Unix(2, 0).UTC(), Valid: true},
	}}
	store := NewStore(sqlc.New(db))

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
	for _, want := range []string{"when_to_use", "match_when", "priority"} {
		if !strings.Contains(db.sql, want) {
			t.Fatalf("upsert SQL missing %q:\n%s", want, db.sql)
		}
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
	if !upsertArgsContain(db.args, "Use when SQL workflow guidance is needed.", []byte(`{"cwd_prefix":"/repo"}`), int32(70)) {
		t.Fatalf("upsert args missing routing metadata: %#v", db.args)
	}
}

func TestGetSectionByRecallTopicUsesGeneratedRecallQuery(t *testing.T) {
	t.Parallel()

	db := &captureRecallSectionDB{body: "Recall package body"}
	store := NewStore(sqlc.New(db))

	got, err := store.GetSectionByRecallTopic(context.Background(), "/repo/a", "context_budget")
	if err != nil {
		t.Fatalf("GetSectionByRecallTopic() error = %v", err)
	}
	if got != "Recall package body" {
		t.Fatalf("body = %q, want recall body", got)
	}
	assertRecallSectionSQL(t, db.query)
	if len(db.args) != 2 || db.args[0] != "context_budget" || db.args[1] != "/repo/a" {
		t.Fatalf("query args = %#v, want [context_budget /repo/a]", db.args)
	}
}

func TestGetSectionByRecallTopicRequiresCWD(t *testing.T) {
	t.Parallel()

	db := &captureRecallSectionDB{body: "unused"}
	store := NewStore(sqlc.New(db))

	_, err := store.GetSectionByRecallTopic(context.Background(), " ", "context_budget")
	if err == nil {
		t.Fatal("GetSectionByRecallTopic() error = nil, want cwd required")
	}
	if !strings.Contains(err.Error(), "cwd") {
		t.Fatalf("error = %v, want cwd required", err)
	}
	if db.query != "" {
		t.Fatalf("query executed despite empty cwd:\n%s", db.query)
	}
}

func TestGetSectionByRecallTopicWrapsNoRowsAsNotFound(t *testing.T) {
	t.Parallel()

	db := &captureRecallSectionDB{err: pgx.ErrNoRows}
	store := NewStore(sqlc.New(db))

	_, err := store.GetSectionByRecallTopic(context.Background(), "/repo/a", "missing")
	if err == nil {
		t.Fatal("GetSectionByRecallTopic() error = nil, want wrapped not found")
	}
	if !platformdb.IsNotFound(err) {
		t.Fatalf("error = %v, want platformdb.IsNotFound", err)
	}
	assertRecallSectionSQL(t, db.query)
}

func TestGetSectionByRecallTopicFallsBackToBuiltinRegistry(t *testing.T) {
	t.Parallel()

	db := &captureRecallSectionDB{err: pgx.ErrNoRows}
	store := NewStore(sqlc.New(db))

	got, err := store.GetSectionByRecallTopic(context.Background(), "/repo/a", "lsp-basics")
	if err != nil {
		t.Fatalf("GetSectionByRecallTopic() error = %v", err)
	}
	if !strings.Contains(got, "LSP") {
		t.Fatalf("builtin recall body = %q, want LSP guidance", got)
	}
	assertRecallSectionSQL(t, db.query)
}

func TestListSectionsByTemplateIDUsesGeneratedInjectableQuery(t *testing.T) {
	t.Parallel()

	db := &captureListSectionsDB{rows: &stubPromptSectionRows{rows: [][]any{
		{int64(1), int64(42), "identity", "static", int32(0), "Identity body", "always", "", true},
		{int64(2), int64(42), "workflow", "dynamic", int32(10), "Workflow body", "keyword", "", true},
	}}}
	store := NewStore(sqlc.New(db))

	got, err := store.ListSectionsByTemplateID(context.Background(), 42)
	if err != nil {
		t.Fatalf("ListSectionsByTemplateID() error = %v", err)
	}
	assertListSectionsResult(t, got)
	assertListSectionsSQL(t, db.query)
	assertListSectionsArgs(t, db.args)
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

func assertListSectionsArgs(t *testing.T, args []any) {
	t.Helper()
	if len(args) != 1 || args[0] != int64(42) {
		t.Fatalf("query args = %#v, want [42]", args)
	}
}

func TestListSectionsByTemplateIDWrapsQueryError(t *testing.T) {
	t.Parallel()

	db := &captureListSectionsDB{err: errors.New("query failed")}
	store := NewStore(sqlc.New(db))

	_, err := store.ListSectionsByTemplateID(context.Background(), 42)
	if err == nil {
		t.Fatal("ListSectionsByTemplateID() error = nil, want wrapped error")
	}
	if !strings.Contains(err.Error(), "list_sections") {
		t.Fatalf("error = %v, want list_sections context", err)
	}
	assertListSectionsSQL(t, db.query)
}

func assertRecallSectionSQL(t *testing.T, sql string) {
	t.Helper()

	for _, want := range []string{
		"SELECT s.body",
		"FROM prompt_template_sections s",
		"JOIN prompt_templates t ON t.id = s.template_id",
		"s.recall_topic = $1",
		"s.trigger_type = 'recall'",
		"s.enabled = TRUE",
		"t.enabled = TRUE",
		"(t.tags ? ('scope.cwd:' || $2::text) OR t.tags ? 'scope.global')",
		"ORDER BY CASE",
		"WHEN t.tags ? ('scope.cwd:' || $2::text) THEN 0",
		"WHEN t.tags ? 'scope.global' THEN 1",
		"s.ordinal",
		"s.id",
		"LIMIT 1",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("recall SQL missing %q:\n%s", want, sql)
		}
	}
}

func assertListSectionsSQL(t *testing.T, sql string) {
	t.Helper()

	for _, want := range []string{
		"SELECT s.id, s.template_id, s.section_key, s.region, s.ordinal, s.body",
		"FROM prompt_template_sections s",
		"s.template_id = $1",
		"s.enabled = TRUE",
		"s.trigger_type <> 'recall'",
		"ORDER BY CASE s.region WHEN 'static' THEN 0 ELSE 1 END, s.ordinal, s.id",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("list sections SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestGetSectionByRecallTopicPGIntegration(t *testing.T) {
	if os.Getenv("PROMPT_RECALL_INTEGRATION") != "1" {
		t.Skip("set PROMPT_RECALL_INTEGRATION=1 and DATABASE_URL to run against a DB with migrations through 0096 already applied")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when PROMPT_RECALL_INTEGRATION=1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	topic := fmt.Sprintf("codex_x2_recall_%d", time.Now().UnixNano())
	disabledTopic := topic + "_disabled"
	disabledParentTopic := topic + "_disabled_parent"
	alwaysTopic := topic + "_always"
	const cwd = "/repo/a"
	templateID := insertPromptTemplate(t, ctx, tx, "test/prompt-recall-integration-"+topic, "Recall Integration", true, cwd)
	disabledTemplateID := insertPromptTemplate(t, ctx, tx, "test/prompt-recall-integration-disabled-"+topic, "Disabled Recall Integration", false, cwd)
	insertRecallSection(t, ctx, tx, templateID, "enabled_recall", "Recall body", true, "recall", topic)
	insertRecallSection(t, ctx, tx, templateID, "disabled_recall", "Disabled body", false, "recall", disabledTopic)
	insertRecallSection(t, ctx, tx, disabledTemplateID, "disabled_parent_recall", "Disabled parent body", true, "recall", disabledParentTopic)
	insertRecallSection(t, ctx, tx, templateID, "always_topic", "Always body", true, "always", alwaysTopic)

	store := NewStore(sqlc.New(tx))
	got, err := store.GetSectionByRecallTopic(ctx, cwd, topic)
	if err != nil {
		t.Fatalf("GetSectionByRecallTopic(enabled recall) error = %v", err)
	}
	if got != "Recall body" {
		t.Fatalf("body = %q, want Recall body", got)
	}
	assertRecallTopicsNotFound(t, ctx, store, cwd, disabledTopic, disabledParentTopic, alwaysTopic, topic+"_missing")
}

func insertPromptTemplate(t *testing.T, ctx context.Context, tx pgx.Tx, promptKey, title string, enabled bool, cwd string) int64 {
	t.Helper()

	var templateID int64
	err := tx.QueryRow(ctx, `
INSERT INTO public.prompt_templates
    (prompt_key, agent_key, title, tool_name, prompt_text, variables, tags, enabled,
     created_by, updated_by, description, manually_edited, when_to_use)
VALUES ($1, 'test', $2, '', 'fallback', '{}'::jsonb, jsonb_build_array('scope.cwd:' || $4::text), $3,
        'test', 'test', '', FALSE, '')
RETURNING id`, promptKey, title, enabled, cwd).Scan(&templateID)
	if err != nil {
		t.Fatalf("insert prompt template(%s) error = %v", promptKey, err)
	}
	return templateID
}

func assertRecallTopicsNotFound(t *testing.T, ctx context.Context, store Store, cwd string, topics ...string) {
	t.Helper()

	for _, topic := range topics {
		_, err := store.GetSectionByRecallTopic(ctx, cwd, topic)
		if !platformdb.IsNotFound(err) {
			t.Fatalf("GetSectionByRecallTopic(%q) error = %v, want not found", topic, err)
		}
	}
}

func insertRecallSection(t *testing.T, ctx context.Context, tx pgx.Tx, templateID int64, sectionKey, body string, enabled bool, triggerType, recallTopic string) {
	t.Helper()

	_, err := tx.Exec(ctx, `
INSERT INTO public.prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic)
VALUES ($1, $2, 'dynamic', 0, $3, '{}'::jsonb, $4, $5, $6)`,
		templateID, sectionKey, body, enabled, triggerType, recallTopic,
	)
	if err != nil {
		t.Fatalf("insert prompt_template_sections(%s) error = %v", sectionKey, err)
	}
}

type captureRecallSectionDB struct {
	query string
	args  []any
	body  string
	err   error
}

func (*captureRecallSectionDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call")
}

func (*captureRecallSectionDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (db *captureRecallSectionDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.query = sql
	db.args = append([]any(nil), args...)
	return captureRecallSectionRow{body: db.body, err: db.err}
}

type captureRecallSectionRow struct {
	body string
	err  error
}

func (r captureRecallSectionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return fmt.Errorf("scan dest len = %d, want 1", len(dest))
	}
	body, ok := dest[0].(*string)
	if !ok {
		return fmt.Errorf("scan dest[0] type = %T, want *string", dest[0])
	}
	*body = r.body
	return nil
}

type captureListSectionsDB struct {
	query string
	args  []any
	rows  pgx.Rows
	err   error
}

func (*captureListSectionsDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call")
}

func (db *captureListSectionsDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.query = sql
	db.args = append([]any(nil), args...)
	if db.err != nil {
		return nil, db.err
	}
	if db.rows == nil {
		return &stubPromptSectionRows{}, nil
	}
	return db.rows, nil
}

func (*captureListSectionsDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return captureRecallSectionRow{err: fmt.Errorf("unexpected QueryRow call")}
}

type capturePromptUpsertDB struct {
	sql  string
	args []any
	row  []any
	err  error
}

func (*capturePromptUpsertDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call")
}

func (*capturePromptUpsertDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (db *capturePromptUpsertDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.sql = sql
	db.args = append([]any(nil), args...)
	return capturePromptUpsertRow{values: db.row, err: db.err}
}

type capturePromptUpsertRow struct {
	values []any
	err    error
}

func (r capturePromptUpsertRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("dest len = %d, values len = %d", len(dest), len(r.values))
	}
	for i := range dest {
		if err := assignPromptValue(dest[i], r.values[i]); err != nil {
			return fmt.Errorf("scan column %d: %w", i, err)
		}
	}
	return nil
}

func upsertArgsContain(args []any, whenToUse string, matchWhen []byte, priority int32) bool {
	var hasWhen, hasMatch, hasPriority bool
	for _, arg := range args {
		if arg == whenToUse {
			hasWhen = true
		}
		if got, ok := arg.([]byte); ok && string(got) == string(matchWhen) {
			hasMatch = true
		}
		if arg == priority {
			hasPriority = true
		}
	}
	return hasWhen && hasMatch && hasPriority
}

type stubPromptSectionRows struct {
	rows [][]any
	idx  int
	err  error
}

func (*stubPromptSectionRows) Close()                                       {}
func (r *stubPromptSectionRows) Err() error                                 { return r.err }
func (*stubPromptSectionRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*stubPromptSectionRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*stubPromptSectionRows) RawValues() [][]byte                          { return nil }
func (*stubPromptSectionRows) Conn() *pgx.Conn                              { return nil }

func (r *stubPromptSectionRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *stubPromptSectionRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return fmt.Errorf("scan without current row")
	}
	values := r.rows[r.idx-1]
	if len(dest) != len(values) {
		return fmt.Errorf("dest len = %d, values len = %d", len(dest), len(values))
	}
	for i := range dest {
		if err := assignPromptSectionValue(dest[i], values[i]); err != nil {
			return fmt.Errorf("scan column %d: %w", i, err)
		}
	}
	return nil
}

func (r *stubPromptSectionRows) Values() ([]any, error) {
	if r.idx == 0 || r.idx > len(r.rows) {
		return nil, fmt.Errorf("values without current row")
	}
	return append([]any(nil), r.rows[r.idx-1]...), nil
}

func assignPromptSectionValue(dest any, value any) error {
	return assignPromptValue(dest, value)
}

func assignPromptValue(dest any, value any) error {
	if assignPromptScalar(dest, value) {
		return nil
	}
	return assignPromptExtended(dest, value)
}

func assignPromptScalar(dest any, value any) bool {
	switch target := dest.(type) {
	case *int64:
		typed, ok := value.(int64)
		if !ok {
			return false
		}
		*target = typed
		return true
	case *int32:
		typed, ok := value.(int32)
		if !ok {
			return false
		}
		*target = typed
		return true
	case *string:
		typed, ok := value.(string)
		if !ok {
			return false
		}
		*target = typed
		return true
	case *bool:
		typed, ok := value.(bool)
		if !ok {
			return false
		}
		*target = typed
		return true
	default:
		return false
	}
}

func assignPromptExtended(dest any, value any) error {
	switch target := dest.(type) {
	case *[]byte:
		typed, ok := value.([]byte)
		if !ok {
			return fmt.Errorf("cannot assign %T to *[]byte", value)
		}
		*target = append((*target)[:0], typed...)
	case *pgtype.Timestamptz:
		typed, ok := value.(pgtype.Timestamptz)
		if !ok {
			return fmt.Errorf("cannot assign %T to *pgtype.Timestamptz", value)
		}
		*target = typed
	default:
		return fmt.Errorf("unsupported dest type %T", dest)
	}
	return nil
}
