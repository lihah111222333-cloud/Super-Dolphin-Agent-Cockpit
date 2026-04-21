package router

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	routerpkg "github.com/anthropic-ai/super-agent-v3/internal/router"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

type fakeReader struct {
	rows []promptstore.PromptTemplate
	err  error
}

func (f *fakeReader) List(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	return f.rows, f.err
}

func tpl(promptKey, agentKey string, tags []string) promptstore.PromptTemplate {
	b, _ := json.Marshal(tags)
	return promptstore.PromptTemplate{
		PromptKey:  promptKey,
		AgentKey:   agentKey,
		Title:      "T-" + promptKey,
		PromptText: "body-" + promptKey,
		Tags:       b,
		Enabled:    true,
		UpdatedAt:  time.Now(),
	}
}

func TestClassify_EmptyInputIsNoop(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, routerpkg.NewRuleRouter(), &fakeReader{})
	got, err := svc.Classify(context.Background(), ClassifyRequest{UserInput: "   "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Matched {
		t.Fatalf("empty input must not match: %+v", got)
	}
}

func TestClassify_NilBackendIsGracefulNoop(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, nil, &fakeReader{rows: []promptstore.PromptTemplate{tpl("p1", "a1", []string{"x"})}})
	got, err := svc.Classify(context.Background(), ClassifyRequest{UserInput: "hello x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Matched {
		t.Fatalf("nil backend must not match: %+v", got)
	}
}

func TestClassify_NilStoreIsGracefulNoop(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, routerpkg.NewRuleRouter(), nil)
	got, _ := svc.Classify(context.Background(), ClassifyRequest{UserInput: "anything"})
	if got.Matched {
		t.Fatalf("nil store must not match: %+v", got)
	}
}

func TestClassify_ListErrorIsGracefulNoop(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, routerpkg.NewRuleRouter(), &fakeReader{err: errors.New("pgx: boom")})
	got, err := svc.Classify(context.Background(), ClassifyRequest{UserInput: "hello sql"})
	if err != nil {
		t.Fatalf("expected no error on store failure (graceful): %v", err)
	}
	if got.Matched {
		t.Fatalf("list failure must not match: %+v", got)
	}
}

func TestClassify_ReturnsTitleAndReasonOnMatch(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{rows: []promptstore.PromptTemplate{
		tpl("main/sql", "sql_expert", []string{"sql", "database"}),
		tpl("main/ui", "ui_expert", []string{"react"}),
	}}
	svc := NewService(nil, routerpkg.NewRuleRouter(), reader)
	got, err := svc.Classify(context.Background(), ClassifyRequest{UserInput: "write a sql migration"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Matched || got.AgentKey != "sql_expert" || got.PromptKey != "main/sql" {
		t.Fatalf("want sql_expert match, got %+v", got)
	}
	if got.Title != "T-main/sql" {
		t.Fatalf("want title 'T-main/sql', got %q", got.Title)
	}
	if got.Confidence != 1.0 {
		t.Fatalf("rule-router confidence should be 1.0, got %v", got.Confidence)
	}
	if got.Reason == "" {
		t.Fatalf("reason should not be empty on match")
	}
}

func TestClassify_NoEnabledCandidatesIsNoMatch(t *testing.T) {
	t.Parallel()
	row := tpl("main/sql", "sql_expert", []string{"sql"})
	row.Enabled = false
	reader := &fakeReader{rows: []promptstore.PromptTemplate{row}}
	svc := NewService(nil, routerpkg.NewRuleRouter(), reader)
	got, _ := svc.Classify(context.Background(), ClassifyRequest{UserInput: "sql please"})
	if got.Matched {
		t.Fatalf("disabled templates must not produce a match: %+v", got)
	}
}
