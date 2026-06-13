package prompt

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

type promptQuerierStub struct {
	listFn               func(context.Context, sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error)
	getFn                func(context.Context, sqlc.GetPromptTemplateParams) (sqlc.GetPromptTemplateRow, error)
	deleteFn             func(context.Context, sqlc.DeletePromptTemplateParams) (int64, error)
	insertVersionFn      func(context.Context, sqlc.InsertPromptVersionParams) (int64, error)
	createFn             func(context.Context, sqlc.CreatePromptTemplateParams) (sqlc.CreatePromptTemplateRow, error)
	upsertFn             func(context.Context, sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error)
	listSectionsFn       func(context.Context, sqlc.ListPromptTemplateSectionsByTemplateParams) ([]sqlc.PromptTemplateSection, error)
	listSectionsBatchFn  func(context.Context, sqlc.ListPromptTemplateSectionsByTemplatesParams) ([]sqlc.PromptTemplateSection, error)
	listRecallFn         func(context.Context, sqlc.ListRecallSectionsParams) ([]sqlc.ListRecallSectionsRow, error)
	listDefaultRulesFn   func(context.Context, sqlc.ListDefaultRuleSectionsParams) ([]sqlc.ListDefaultRuleSectionsRow, error)
	upsertSectionFn      func(context.Context, sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error)
	lockRecallFn         func(context.Context, sqlc.LockRecallTopicInCWDParams) error
	upsertRecallTargetFn func(context.Context, sqlc.UpsertPromptRecallTopicTargetInCWDParams) error
	upsertDraftFn        func(context.Context, sqlc.UpsertPromptIntentDraftParams) (sqlc.UpsertPromptIntentDraftRow, error)
	getDraftFn           func(context.Context, sqlc.GetPromptIntentDraftParams) (sqlc.GetPromptIntentDraftRow, error)
	listDraftsFn         func(context.Context, sqlc.ListPromptIntentDraftsParams) ([]sqlc.ListPromptIntentDraftsRow, error)
	updateDraftStatusFn  func(context.Context, sqlc.UpdatePromptIntentDraftStatusParams) (sqlc.UpdatePromptIntentDraftStatusRow, error)
}

func (s *promptQuerierStub) ListPromptTemplates(ctx context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func (s *promptQuerierStub) GetPromptTemplate(ctx context.Context, arg sqlc.GetPromptTemplateParams) (sqlc.GetPromptTemplateRow, error) {
	if s.getFn != nil {
		return s.getFn(ctx, arg)
	}
	return sqlc.GetPromptTemplateRow{}, nil
}

func (s *promptQuerierStub) DeletePromptTemplate(ctx context.Context, arg sqlc.DeletePromptTemplateParams) (int64, error) {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, arg)
	}
	return 0, nil
}

func (s *promptQuerierStub) InsertPromptVersion(ctx context.Context, arg sqlc.InsertPromptVersionParams) (int64, error) {
	if s.insertVersionFn != nil {
		return s.insertVersionFn(ctx, arg)
	}
	return 0, nil
}

func (s *promptQuerierStub) CreatePromptTemplate(ctx context.Context, arg sqlc.CreatePromptTemplateParams) (sqlc.CreatePromptTemplateRow, error) {
	if s.createFn != nil {
		return s.createFn(ctx, arg)
	}
	return sqlc.CreatePromptTemplateRow{}, nil
}

func (s *promptQuerierStub) UpsertPromptTemplate(ctx context.Context, arg sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error) {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, arg)
	}
	return sqlc.UpsertPromptTemplateRow{}, nil
}

func (s *promptQuerierStub) ListPromptTemplateSectionsByTemplate(ctx context.Context, arg sqlc.ListPromptTemplateSectionsByTemplateParams) ([]sqlc.PromptTemplateSection, error) {
	if s.listSectionsFn != nil {
		return s.listSectionsFn(ctx, arg)
	}
	return nil, nil
}

func (s *promptQuerierStub) ListPromptTemplateSectionsByTemplates(ctx context.Context, arg sqlc.ListPromptTemplateSectionsByTemplatesParams) ([]sqlc.PromptTemplateSection, error) {
	if s.listSectionsBatchFn != nil {
		return s.listSectionsBatchFn(ctx, arg)
	}
	return nil, nil
}

func (s *promptQuerierStub) ListRecallSections(ctx context.Context, arg sqlc.ListRecallSectionsParams) ([]sqlc.ListRecallSectionsRow, error) {
	if s.listRecallFn != nil {
		return s.listRecallFn(ctx, arg)
	}
	return nil, nil
}

func (s *promptQuerierStub) ListDefaultRuleSections(ctx context.Context, arg sqlc.ListDefaultRuleSectionsParams) ([]sqlc.ListDefaultRuleSectionsRow, error) {
	if s.listDefaultRulesFn != nil {
		return s.listDefaultRulesFn(ctx, arg)
	}
	return nil, nil
}

func (s *promptQuerierStub) UpsertPromptTemplateSection(ctx context.Context, arg sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error) {
	if s.upsertSectionFn != nil {
		return s.upsertSectionFn(ctx, arg)
	}
	return sqlc.PromptTemplateSection{}, nil
}

func (s *promptQuerierStub) LockRecallTopicInCWD(ctx context.Context, arg sqlc.LockRecallTopicInCWDParams) error {
	if s.lockRecallFn != nil {
		return s.lockRecallFn(ctx, arg)
	}
	return nil
}

func (s *promptQuerierStub) UpsertPromptRecallTopicTargetInCWD(ctx context.Context, arg sqlc.UpsertPromptRecallTopicTargetInCWDParams) error {
	if s.upsertRecallTargetFn != nil {
		return s.upsertRecallTargetFn(ctx, arg)
	}
	return nil
}

func (s *promptQuerierStub) UpsertPromptIntentDraft(ctx context.Context, arg sqlc.UpsertPromptIntentDraftParams) (sqlc.UpsertPromptIntentDraftRow, error) {
	if s.upsertDraftFn != nil {
		return s.upsertDraftFn(ctx, arg)
	}
	return sqlc.UpsertPromptIntentDraftRow{}, nil
}

func (s *promptQuerierStub) GetPromptIntentDraft(ctx context.Context, arg sqlc.GetPromptIntentDraftParams) (sqlc.GetPromptIntentDraftRow, error) {
	if s.getDraftFn != nil {
		return s.getDraftFn(ctx, arg)
	}
	return sqlc.GetPromptIntentDraftRow{}, nil
}

func (s *promptQuerierStub) ListPromptIntentDrafts(ctx context.Context, arg sqlc.ListPromptIntentDraftsParams) ([]sqlc.ListPromptIntentDraftsRow, error) {
	if s.listDraftsFn != nil {
		return s.listDraftsFn(ctx, arg)
	}
	return nil, nil
}

func (s *promptQuerierStub) UpdatePromptIntentDraftStatus(ctx context.Context, arg sqlc.UpdatePromptIntentDraftStatusParams) (sqlc.UpdatePromptIntentDraftStatusRow, error) {
	if s.updateDraftStatusFn != nil {
		return s.updateDraftStatusFn(ctx, arg)
	}
	return sqlc.UpdatePromptIntentDraftStatusRow{}, nil
}

func TestListForwardsAgentKeyKeywordCWDAndLimit(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_234_567, 0).UTC()
	var captured sqlc.ListPromptTemplatesParams

	s := &store{q: &promptQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
			captured = arg
			return []sqlc.ListPromptTemplatesRow{{
				ID:          9,
				PromptKey:   "agent.review",
				Title:       "Review flow",
				AgentKey:    "reviewer",
				ToolName:    "review",
				PromptText:  "please review",
				Variables:   []byte(`{"lang":"go"}`),
				Tags:        []byte(`["qa"]`),
				Enabled:     int64(1),
				CreatedBy:   "u1",
				UpdatedBy:   "u2",
				CreatedAt:   platformdb.Millis(now),
				UpdatedAt:   platformdb.Millis(now),
				Description: "code review prompt",
				WhenToUse:   "Use for reviewing code.",
			}}, nil
		},
	}}

	got, err := s.List(context.Background(), ListFilter{AgentKey: "reviewer", Keyword: "review", CWD: "/repo_a", Limit: 10})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	assertListForwardedParams(t, captured)
	if len(got) != 1 {
		t.Fatalf("List() len = %d, want 1", len(got))
	}
	assertListedPrompt(t, got[0])
}

func assertListForwardedParams(t *testing.T, captured sqlc.ListPromptTemplatesParams) {
	t.Helper()
	if captured.AgentKey != "reviewer" || captured.Keyword != "review" || captured.CWD == nil || *captured.CWD != "/repo_a" || captured.LimitCount != 10 {
		t.Fatalf("List() forwarded wrong params: %+v", captured)
	}
}

func assertListedPrompt(t *testing.T, p PromptTemplate) {
	t.Helper()
	if p.ID != 9 || p.PromptKey != "agent.review" || p.AgentKey != "reviewer" || !p.Enabled {
		t.Fatalf("List() row mapped incorrectly: %+v", p)
	}
	if string(p.Variables) != `{"lang":"go"}` || string(p.Tags) != `["qa"]` {
		t.Fatalf("List() JSON fields mapped incorrectly: vars=%s tags=%s", p.Variables, p.Tags)
	}
	if p.WhenToUse != "Use for reviewing code." {
		t.Fatalf("List() when_to_use = %q, want review guidance", p.WhenToUse)
	}
}

func TestStoreListPromptsRespectsCWDFilter(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_234_568, 0).UTC()
	rows := []sqlc.ListPromptTemplatesRow{
		{ID: 1, PromptKey: "global", Title: "Global", AgentKey: "main", Tags: []byte(`[]`), Enabled: int64(1), CreatedAt: platformdb.Millis(now), UpdatedAt: platformdb.Millis(now)},
		{ID: 2, PromptKey: "repo-a", Title: "Repo A", AgentKey: "main", Tags: []byte(`["scope.cwd:/repo_a"]`), Enabled: int64(1), CreatedAt: platformdb.Millis(now), UpdatedAt: platformdb.Millis(now)},
		{ID: 3, PromptKey: "repo-b", Title: "Repo B", AgentKey: "main", Tags: []byte(`["scope.cwd:/repo_b"]`), Enabled: int64(1), CreatedAt: platformdb.Millis(now), UpdatedAt: platformdb.Millis(now)},
	}
	s := &store{q: &promptQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
			if arg.CWD == nil || *arg.CWD != "/repo_a" {
				t.Fatalf("ListPromptTemplates CWD = %v, want /repo_a", arg.CWD)
			}
			return rows[:2], nil
		},
	}}

	got, err := s.List(context.Background(), ListFilter{CWD: "/repo_a", Limit: 10})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].PromptKey != "global" || got[1].PromptKey != "repo-a" {
		t.Fatalf("List() cwd filtered rows = %+v, want global + repo-a", got)
	}
}

func TestStoreListPromptsFailsFastOnInvalidTagsJSON(t *testing.T) {
	t.Parallel()

	db := openPromptSQLite(t)
	createPromptTemplateTable(t, db)
	seedPromptTemplateSQLite(t, db, 1, "bad-tags", "main", "not-json", 100)

	s := &store{q: sqlc.New(db)}
	_, err := s.List(context.Background(), ListFilter{CWD: "/repo/a", Limit: 10})
	if err == nil {
		t.Fatal("List() error = nil, want invalid JSON tags to fail fast")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "json") && !strings.Contains(strings.ToLower(err.Error()), "malformed") {
		t.Fatalf("List() error = %v, want invalid JSON failure", err)
	}
}

func TestListPromptTemplatesUsesSQLiteJSONEachForScopeTags(t *testing.T) {
	t.Parallel()

	sqlText := readPromptRepoFile(t, filepath.Join("sql", "queries", "prompt_template.sql"))
	for _, want := range []string{"json_each(tags)", "value = 'scope.global'", "value = 'scope.cwd:' || sqlc.arg(cwd)"} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("ListPromptTemplates query missing JSON scope fragment %q", want)
		}
	}
}

func TestListReturnsEmptySliceWhenNoRows(t *testing.T) {
	t.Parallel()
	s := &store{q: &promptQuerierStub{}}
	got, err := s.List(context.Background(), ListFilter{CWD: "/repo_a"})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List() got = %v, want non-nil empty slice", got)
	}
}

func TestListRequiresCWD(t *testing.T) {
	t.Parallel()
	s := &store{q: &promptQuerierStub{
		listFn: func(context.Context, sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
			t.Fatal("ListPromptTemplates must not run without cwd")
			return nil, nil
		},
	}}

	_, err := s.List(context.Background(), ListFilter{})
	if err == nil {
		t.Fatal("List() error = nil, want cwd required")
	}
	if !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("List() error = %v, want cwd required", err)
	}
}

func TestListWrapsQuerierError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("pg connection reset")
	s := &store{q: &promptQuerierStub{
		listFn: func(context.Context, sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
			return nil, sentinel
		},
	}}
	_, err := s.List(context.Background(), ListFilter{CWD: "/repo_a"})
	if err == nil {
		t.Fatal("List() expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("List() error = %v, want wrap of sentinel", err)
	}
}

func TestStoreGetMapsRow(t *testing.T) {
	t.Parallel()
	now := promptStoreTestTime()
	var capturedKey string
	s := &store{q: &promptQuerierStub{
		getFn: func(_ context.Context, arg sqlc.GetPromptTemplateParams) (sqlc.GetPromptTemplateRow, error) {
			promptKey := arg.PromptKey
			capturedKey = promptKey
			return promptGetRow(promptKey, now), nil
		},
	}}

	got, err := s.Get(context.Background(), "main/scoped")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if capturedKey != "main/scoped" {
		t.Fatalf("Get() prompt key = %q, want main/scoped", capturedKey)
	}
	assertPromptGetResult(t, got)
}

func promptStoreTestTime() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

func promptGetRow(promptKey string, now time.Time) sqlc.GetPromptTemplateRow {
	return sqlc.GetPromptTemplateRow{
		ID:             7,
		PromptKey:      promptKey,
		Title:          "Scoped Prompt",
		AgentKey:       "main",
		ToolName:       "tool",
		PromptText:     "body",
		Variables:      []byte(`{"lang":"go"}`),
		Tags:           []byte(`{"unexpected":true}`),
		Description:    "desc",
		Enabled:        int64(1),
		ManuallyEdited: int64(1),
		CreatedBy:      "creator",
		UpdatedBy:      "editor",
		CreatedAt:      platformdb.Millis(now),
		UpdatedAt:      platformdb.Millis(now),
		WhenToUse:      "Use when editing scoped prompts.",
	}
}

func assertPromptGetResult(t *testing.T, got *PromptTemplate) {
	t.Helper()
	if got == nil || got.ID != 7 || got.Title != "Scoped Prompt" || got.AgentKey != "main" || !got.ManuallyEdited {
		t.Fatalf("Get() mapped row incorrectly: %+v", got)
	}
}

func TestStoreUpsertForwardsParamsAndMapsRow(t *testing.T) {
	t.Parallel()
	now := promptStoreTestTime()
	var captured sqlc.UpsertPromptTemplateParams
	s := &store{q: &promptQuerierStub{
		upsertFn: func(_ context.Context, arg sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error) {
			captured = arg
			return promptUpsertRow(arg, now), nil
		},
	}}

	got, err := s.Upsert(context.Background(), promptUpsertInput())
	if err != nil {
		t.Fatalf("Upsert() unexpected error: %v", err)
	}
	assertPromptUpsertParams(t, captured)
	assertPromptUpsertResult(t, got)
}

func promptUpsertRow(arg sqlc.UpsertPromptTemplateParams, now time.Time) sqlc.UpsertPromptTemplateRow {
	return sqlc.UpsertPromptTemplateRow{
		ID:             8,
		PromptKey:      arg.PromptKey,
		Title:          arg.Title,
		AgentKey:       arg.AgentKey,
		ToolName:       arg.ToolName,
		PromptText:     arg.PromptText,
		Variables:      arg.Variables,
		Tags:           arg.Tags,
		Description:    arg.Description,
		Enabled:        arg.Enabled,
		ManuallyEdited: arg.ManuallyEdited,
		CreatedBy:      arg.CreatedBy,
		UpdatedBy:      arg.UpdatedBy,
		CreatedAt:      platformdb.Millis(now),
		UpdatedAt:      platformdb.Millis(now),
		WhenToUse:      arg.WhenToUse,
	}
}

func promptUpsertInput() PromptTemplate {
	return PromptTemplate{
		PromptKey:      "main/scoped",
		Title:          "Scoped Prompt",
		AgentKey:       "main",
		ToolName:       "tool",
		PromptText:     "body",
		Variables:      []byte(`{"lang":"go"}`),
		Tags:           []byte(`["scope.cwd:/repo"]`),
		Description:    "desc",
		Enabled:        true,
		ManuallyEdited: true,
		CreatedBy:      "creator",
		UpdatedBy:      "editor",
		WhenToUse:      "Use when editing scoped prompts.",
	}
}

func assertPromptUpsertParams(t *testing.T, captured sqlc.UpsertPromptTemplateParams) {
	t.Helper()
	if captured.PromptKey != "main/scoped" || captured.Title != "Scoped Prompt" || captured.UpdatedBy != "editor" {
		t.Fatalf("Upsert() forwarded wrong params: %+v", captured)
	}
	if captured.ManuallyEdited == 0 {
		t.Fatalf("Upsert() manually_edited = false, want true")
	}
	if captured.WhenToUse != "Use when editing scoped prompts." {
		t.Fatalf("Upsert() when_to_use = %q, want guidance", captured.WhenToUse)
	}
}

func assertPromptUpsertResult(t *testing.T, got *PromptTemplate) {
	t.Helper()
	if got == nil || got.ID != 8 || string(got.Tags) != `["scope.cwd:/repo"]` || !got.ManuallyEdited {
		t.Fatalf("Upsert() mapped row incorrectly: %+v", got)
	}
	if got.WhenToUse != "Use when editing scoped prompts." {
		t.Fatalf("Upsert() when_to_use = %q, want guidance", got.WhenToUse)
	}
}

func TestStoreDeleteTreatsRowsAffectedAsSuccess(t *testing.T) {
	t.Parallel()
	var capturedKey string
	s := &store{q: &promptQuerierStub{
		deleteFn: func(_ context.Context, arg sqlc.DeletePromptTemplateParams) (int64, error) {
			capturedKey = arg.PromptKey
			return 1, nil
		},
	}}

	if err := s.Delete(context.Background(), "main/scoped"); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedKey != "main/scoped" {
		t.Fatalf("Delete() prompt key = %q, want main/scoped", capturedKey)
	}
}

func TestStoreInsertVersionForwardsParams(t *testing.T) {
	t.Parallel()
	now := promptStoreTestTime()
	var captured sqlc.InsertPromptVersionParams
	s := &store{q: &promptQuerierStub{
		insertVersionFn: func(_ context.Context, arg sqlc.InsertPromptVersionParams) (int64, error) {
			captured = arg
			return 42, nil
		},
	}}

	sourceUpdatedAt := now.Add(2 * time.Minute)
	id, err := s.InsertVersion(context.Background(), promptVersionInput(sourceUpdatedAt))
	if err != nil {
		t.Fatalf("InsertVersion() unexpected error: %v", err)
	}
	if id != 42 {
		t.Fatalf("InsertVersion() returned wrong id: got %d want 42", id)
	}
	assertPromptVersionParams(t, captured, sourceUpdatedAt)
}

func promptVersionInput(sourceUpdatedAt time.Time) PromptTemplateVersion {
	return PromptTemplateVersion{
		PromptKey:       "main/scoped",
		Title:           "Scoped Prompt",
		AgentKey:        "main",
		ToolName:        "tool",
		PromptText:      "body",
		Variables:       []byte(`{"lang":"go"}`),
		Tags:            []byte(`["scope.cwd:/repo"]`),
		Description:     "desc",
		Enabled:         true,
		CreatedBy:       "creator",
		UpdatedBy:       "editor",
		SourceUpdatedAt: &sourceUpdatedAt,
	}
}

func assertPromptVersionParams(t *testing.T, captured sqlc.InsertPromptVersionParams, sourceUpdatedAt time.Time) {
	t.Helper()
	if captured.PromptKey != "main/scoped" || captured.ToolName != "tool" || captured.SourceUpdatedAt == nil || *captured.SourceUpdatedAt != platformdb.Millis(sourceUpdatedAt) {
		t.Fatalf("InsertVersion() forwarded wrong params: %+v", captured)
	}
}

func TestStoreWithTxExecutesCallback(t *testing.T) {
	t.Parallel()
	s := &store{q: &promptQuerierStub{}}
	callbackCalls := 0

	err := s.WithTx(context.Background(), func(txStore Store) error {
		callbackCalls++
		if txStore == nil {
			t.Fatal("WithTx() txStore = nil")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx() unexpected error: %v", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("WithTx() callbackCalls = %d, want 1", callbackCalls)
	}
}

func TestStoreDeleteWrapsNotFound(t *testing.T) {
	t.Parallel()
	s := &store{q: &promptQuerierStub{
		deleteFn: func(context.Context, sqlc.DeletePromptTemplateParams) (int64, error) {
			return 0, nil
		},
	}}
	err := s.Delete(context.Background(), "missing")
	if err == nil || !platformdb.IsNotFound(err) {
		t.Fatalf("Delete() error = %v, want not found", err)
	}
}

func openPromptSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	return db
}

func createPromptTemplateTable(t *testing.T, db *sql.DB) {
	t.Helper()
	execPromptSQL(t, db, `CREATE TABLE prompt_templates (
		id INTEGER PRIMARY KEY,
		prompt_key TEXT NOT NULL UNIQUE,
		title TEXT NOT NULL,
		agent_key TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		prompt_text TEXT NOT NULL,
		variables TEXT NOT NULL,
		tags TEXT NOT NULL,
		description TEXT NOT NULL,
		when_to_use TEXT NOT NULL,
		enabled INTEGER NOT NULL,
		manually_edited INTEGER NOT NULL,
		match_when TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(match_when)),
		priority INTEGER NOT NULL,
		created_by TEXT NOT NULL,
		updated_by TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL
	);`)
}

func seedPromptTemplateSQLite(t *testing.T, db *sql.DB, id int64, key, agentKey, tags string, updatedAt int64) {
	t.Helper()
	execPromptSQL(t, db, `INSERT INTO prompt_templates (
		id, prompt_key, title, agent_key, tool_name, prompt_text,
		variables, tags, description, when_to_use, enabled, manually_edited,
		match_when, priority, created_by, updated_by, created_at, updated_at
	) VALUES (?, ?, ?, ?, '', 'body', '{}', ?, '', '', 1, 0, '{}', 0, 'test', 'test', ?, ?);`,
		id, key, key, agentKey, tags, updatedAt, updatedAt)
}

func execPromptSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec sql: %v\n%s", err, query)
	}
}

func readPromptRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			data, err := os.ReadFile(filepath.Join(dir, rel))
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			return string(data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("go.mod not found walking up from %s", file)
	return ""
}
