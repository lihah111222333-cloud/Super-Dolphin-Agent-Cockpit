package prompt

import (
	"context"
	"encoding/json"
	"testing"

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
