package prompt

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPromptTemplateMappingsIncludeManuallyEdited(t *testing.T) {
	ts := pgtype.Timestamptz{Time: time.Unix(1, 0).UTC(), Valid: true}

	tests := []struct {
		name string
		got  PromptTemplate
	}{
		{
			name: "get",
			got: fromGetTemplate(sqlc.GetPromptTemplateRow{
				ID:             1,
				PromptKey:      "main/morning_briefer",
				Title:          "Morning Briefer",
				AgentKey:       "morning_briefer",
				ToolName:       "",
				PromptText:     "Prepare a briefing.",
				Variables:      []byte(`{}`),
				Tags:           []byte(`["briefing"]`),
				Description:    "Daily briefing prompt.",
				Enabled:        true,
				ManuallyEdited: true,
				CreatedBy:      "system.seed",
				UpdatedBy:      "user",
				CreatedAt:      ts,
				UpdatedAt:      ts,
			}),
		},
		{
			name: "list",
			got: fromListTemplate(sqlc.ListPromptTemplatesRow{
				ID:             2,
				PromptKey:      "main/paper_summarizer",
				Title:          "Paper Summarizer",
				AgentKey:       "paper_summarizer",
				ToolName:       "",
				PromptText:     "Summarize a paper.",
				Variables:      []byte(`{}`),
				Tags:           []byte(`["research"]`),
				Description:    "Research summary prompt.",
				Enabled:        true,
				ManuallyEdited: true,
				CreatedBy:      "system.seed",
				UpdatedBy:      "user",
				CreatedAt:      ts,
				UpdatedAt:      ts,
			}),
		},
		{
			name: "upsert",
			got: fromUpsertTemplate(sqlc.UpsertPromptTemplateRow{
				ID:             3,
				PromptKey:      "main/email_drafter",
				Title:          "Email Drafter",
				AgentKey:       "email_drafter",
				ToolName:       "",
				PromptText:     "Draft an email.",
				Variables:      []byte(`{}`),
				Tags:           []byte(`["writing"]`),
				Description:    "Email drafting prompt.",
				Enabled:        true,
				ManuallyEdited: true,
				CreatedBy:      "system.seed",
				UpdatedBy:      "user",
				CreatedAt:      ts,
				UpdatedAt:      ts,
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.got.ManuallyEdited {
				t.Fatalf("ManuallyEdited = false, want true")
			}
			if !json.Valid(tt.got.Variables) {
				t.Fatalf("Variables must remain valid JSON: %s", string(tt.got.Variables))
			}
			if !json.Valid(tt.got.Tags) {
				t.Fatalf("Tags must remain valid JSON: %s", string(tt.got.Tags))
			}
		})
	}
}
