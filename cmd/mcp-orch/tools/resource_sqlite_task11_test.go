package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	commandcardstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/prompt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

func TestSQLiteCommandToolListSearchKeepsPayloadShape(t *testing.T) {
	db := openToolsSQLiteDB(t)
	store := commandcardstore.NewStore(sqlc.New(db))
	seedSQLiteCommandCard(t, store)

	result, err := HandleCommandList(store)(context.Background(), mustRawInput(t, commandListInput{Keyword: "task11:needle"}))
	if err != nil {
		t.Fatalf("HandleCommandList() error = %v", err)
	}
	commands := result.([]commandCardDTO)
	assertSingleCommandPayload(t, "list", commands)

	detail, err := HandleCommandGet(store)(context.Background(), mustRawInput(t, commandGetInput{CardKey: "cmd/search-template"}))
	if err != nil {
		t.Fatalf("HandleCommandGet() error = %v", err)
	}
	assertCommandDTOShape(t, "detail", detail.(commandCardDTO))
}

func TestSQLitePromptToolListSearchKeepsPayloadShape(t *testing.T) {
	ctx := promptToolTestContext()
	db := openToolsSQLiteDB(t)
	store := promptstore.NewStore(db)
	seedSQLitePromptTemplate(t, ctx, store)

	result, err := HandlePromptList(store, nil)(ctx, mustRawInput(t, promptListInput{Keyword: "task11-prompt-needle"}))
	if err != nil {
		t.Fatalf("HandlePromptList() error = %v", err)
	}
	prompts := result.([]promptTemplateDTO)
	assertSinglePromptPayload(t, "list", prompts)

	detail, err := HandlePromptGet(store, nil)(ctx, mustRawInput(t, promptGetInput{PromptKey: "prompt/search-text"}))
	if err != nil {
		t.Fatalf("HandlePromptGet() error = %v", err)
	}
	assertPromptDTOShape(t, "detail", detail.(promptTemplateDTO))
}

func seedSQLiteCommandCard(t *testing.T, store commandcardstore.Store) {
	t.Helper()
	_, err := store.Upsert(context.Background(), commandcardstore.CommandCard{
		CardKey:         "cmd/search-template",
		Title:           "Search Template",
		Description:     "Command card",
		CommandTemplate: "npm run task11:needle",
		ArgsSchema:      json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"}}}`),
		RiskLevel:       "normal",
		Enabled:         true,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})
	if err != nil {
		t.Fatalf("Command Upsert() error = %v", err)
	}
}

func seedSQLitePromptTemplate(t *testing.T, ctx context.Context, store promptstore.Store) {
	t.Helper()
	_, err := store.Upsert(ctx, promptstore.PromptTemplate{
		PromptKey:      "prompt/search-text",
		Title:          "Search Text",
		AgentKey:       "agent",
		ToolName:       "tool",
		PromptText:     "use task11-prompt-needle in the prompt body",
		Variables:      json.RawMessage(`{"topic":"sqlite"}`),
		Tags:           json.RawMessage(`["scope.global"]`),
		Description:    "Prompt template",
		WhenToUse:      "testing",
		Enabled:        true,
		ManuallyEdited: true,
		MatchWhen:      json.RawMessage(`{"kind":"needle"}`),
		CreatedBy:      "test",
		UpdatedBy:      "test",
	})
	if err != nil {
		t.Fatalf("Prompt Upsert() error = %v", err)
	}
}

func assertSingleCommandPayload(t *testing.T, label string, commands []commandCardDTO) {
	t.Helper()
	if len(commands) != 1 {
		t.Fatalf("%s command list len = %d, want 1", label, len(commands))
	}
	assertCommandDTOShape(t, label, commands[0])
}

func assertCommandDTOShape(t *testing.T, label string, got commandCardDTO) {
	t.Helper()
	if got.CommandTemplate != "npm run task11:needle" {
		t.Fatalf("%s command_template = %q, want full template", label, got.CommandTemplate)
	}
	if !strings.Contains(string(got.ArgsSchema), `"target"`) {
		t.Fatalf("%s args_schema = %s, want target field", label, got.ArgsSchema)
	}
}

func assertSinglePromptPayload(t *testing.T, label string, prompts []promptTemplateDTO) {
	t.Helper()
	if len(prompts) != 1 {
		t.Fatalf("%s prompt list len = %d, want 1", label, len(prompts))
	}
	assertPromptDTOShape(t, label, prompts[0])
}

func assertPromptDTOShape(t *testing.T, label string, got promptTemplateDTO) {
	t.Helper()
	if got.PromptText != "use task11-prompt-needle in the prompt body" {
		t.Fatalf("%s prompt_text = %q, want full prompt text", label, got.PromptText)
	}
	if !strings.Contains(string(got.Variables), `"topic"`) {
		t.Fatalf("%s variables = %s, want topic field", label, got.Variables)
	}
}

func openToolsSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tools.sqlite")
	db, err := sql.Open("sqlite", toolsSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	if err := sqliteruntime.RunMigrations(ctx, db, toolsSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	return db
}

func toolsSQLiteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	return path + "?" + q.Encode()
}

func toolsSQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}
