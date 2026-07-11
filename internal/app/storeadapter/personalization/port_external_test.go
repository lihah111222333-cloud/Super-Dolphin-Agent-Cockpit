package personalizationadapter_test

import (
	"context"
	"encoding/json"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/personalization"
)

type externalPersonalizationPreferenceStore struct{}

func (externalPersonalizationPreferenceStore) GetValue(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}

func (externalPersonalizationPreferenceStore) Upsert(context.Context, personalization.PreferenceUpsertParams) error {
	return nil
}

var _ personalization.PreferenceStore = externalPersonalizationPreferenceStore{}
