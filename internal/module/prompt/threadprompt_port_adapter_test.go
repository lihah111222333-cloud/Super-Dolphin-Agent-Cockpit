package prompt

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
)

type threadPromptStoreTestAdapter struct {
	store Store
}

func adaptPromptStoreForThreadPromptTest(store Store) threadprompt.PromptStore {
	return &threadPromptStoreTestAdapter{store: store}
}

func (a *threadPromptStoreTestAdapter) List(ctx context.Context, filter threadprompt.PromptListFilter) ([]threadprompt.PromptTemplate, error) {
	rows, err := a.store.List(ctx, ListFilter{AgentKey: filter.AgentKey, Keyword: filter.Keyword, CWD: filter.CWD, Limit: filter.Limit})
	if err != nil {
		return nil, err
	}
	return promptTemplatesForThreadPromptTest(rows), nil
}

func (a *threadPromptStoreTestAdapter) Get(ctx context.Context, promptKey string) (*threadprompt.PromptTemplate, error) {
	row, err := a.store.Get(ctx, promptKey)
	if row == nil {
		return nil, err
	}
	converted := promptTemplateForThreadPromptTest(*row)
	return &converted, err
}

func (a *threadPromptStoreTestAdapter) InsertVersion(ctx context.Context, row threadprompt.PromptTemplateVersion) (int64, error) {
	return a.store.InsertVersion(ctx, TemplateVersion{
		ID: row.ID, PromptKey: row.PromptKey, Title: row.Title, AgentKey: row.AgentKey,
		ToolName: row.ToolName, PromptText: row.PromptText, Variables: cloneThreadPromptTestJSON(row.Variables),
		Tags: cloneThreadPromptTestJSON(row.Tags), Description: row.Description, Enabled: row.Enabled,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, SourceUpdatedAt: row.SourceUpdatedAt,
		CreatedAt: row.CreatedAt, ArchivedAt: row.ArchivedAt,
	})
}

func (a *threadPromptStoreTestAdapter) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]threadprompt.PromptTemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateID(ctx, templateID)
	return promptSectionsForThreadPromptTest(rows), err
}

func (a *threadPromptStoreTestAdapter) ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]threadprompt.PromptTemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateIDs(ctx, templateIDs)
	return promptSectionsForThreadPromptTest(rows), err
}

func (a *threadPromptStoreTestAdapter) ListRecallSections(ctx context.Context, cwd string) ([]threadprompt.PromptTemplateSection, error) {
	rows, err := a.store.ListRecallSections(ctx, cwd)
	return promptSectionsForThreadPromptTest(rows), err
}

func (a *threadPromptStoreTestAdapter) ListDefaultRuleSections(ctx context.Context, cwd string) ([]threadprompt.PromptTemplateSection, error) {
	rows, err := a.store.ListDefaultRuleSections(ctx, cwd)
	return promptSectionsForThreadPromptTest(rows), err
}

func promptTemplatesForThreadPromptTest(rows []Template) []threadprompt.PromptTemplate {
	out := make([]threadprompt.PromptTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, promptTemplateForThreadPromptTest(row))
	}
	return out
}

func promptTemplateForThreadPromptTest(row Template) threadprompt.PromptTemplate {
	return threadprompt.PromptTemplate{
		ID: row.ID, PromptKey: row.PromptKey, Title: row.Title, AgentKey: row.AgentKey,
		ToolName: row.ToolName, PromptText: row.PromptText, WhenToUse: row.WhenToUse,
		Variables: cloneThreadPromptTestJSON(row.Variables), Tags: cloneThreadPromptTestJSON(row.Tags), Enabled: row.Enabled,
		ManuallyEdited: row.ManuallyEdited, MatchWhen: cloneThreadPromptTestJSON(row.MatchWhen), Priority: row.Priority,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, Description: row.Description,
	}
}

func promptSectionsForThreadPromptTest(rows []TemplateSection) []threadprompt.PromptTemplateSection {
	out := make([]threadprompt.PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		out = append(out, threadprompt.PromptTemplateSection{
			ID: row.ID, TemplateID: row.TemplateID, SectionKey: row.SectionKey, Region: row.Region,
			Ordinal: row.Ordinal, Body: row.Body, EnableWhen: cloneThreadPromptTestJSON(row.EnableWhen), Enabled: row.Enabled,
			TriggerType: row.TriggerType, RecallTopic: row.RecallTopic, TemplatePromptKey: row.TemplatePromptKey,
			TemplateTitle: row.TemplateTitle, TemplateDescription: row.TemplateDescription,
			TemplateWhenToUse: row.TemplateWhenToUse, TemplateTags: cloneThreadPromptTestJSON(row.TemplateTags),
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	return out
}

func cloneThreadPromptTestJSON(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}
