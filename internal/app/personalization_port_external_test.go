package app_test

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/module/personalization"
)

type externalPersonalizationPreferenceStore struct{}

func (externalPersonalizationPreferenceStore) GetValue(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}

func (externalPersonalizationPreferenceStore) Upsert(context.Context, personalization.PreferenceUpsertParams) error {
	return nil
}

var _ personalization.PreferenceStore = externalPersonalizationPreferenceStore{}
