package app_test

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
)

type externalThreadPromptStore struct{}

func (externalThreadPromptStore) List(context.Context, threadprompt.PromptListFilter) ([]threadprompt.PromptTemplate, error) {
	return nil, nil
}

func (externalThreadPromptStore) Get(context.Context, string) (*threadprompt.PromptTemplate, error) {
	return nil, nil
}

func (externalThreadPromptStore) InsertVersion(context.Context, threadprompt.PromptTemplateVersion) (int64, error) {
	return 0, nil
}

func (externalThreadPromptStore) ListSectionsByTemplateID(context.Context, int64) ([]threadprompt.PromptTemplateSection, error) {
	return nil, nil
}

func (externalThreadPromptStore) ListSectionsByTemplateIDs(context.Context, []int64) ([]threadprompt.PromptTemplateSection, error) {
	return nil, nil
}

func (externalThreadPromptStore) ListRecallSections(context.Context, string) ([]threadprompt.PromptTemplateSection, error) {
	return nil, nil
}

func (externalThreadPromptStore) ListDefaultRuleSections(context.Context, string) ([]threadprompt.PromptTemplateSection, error) {
	return nil, nil
}

var _ threadprompt.PromptStore = externalThreadPromptStore{}
