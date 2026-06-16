package uipreference

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store is the persistence surface for scoped UI preferences.
type Store = contract.UIPreferenceStore

// UpsertParams drives Store.Upsert.
type UpsertParams = contract.UIPreferenceUpsertParams

// UIPreference is the stored preference projection.
type UIPreference = contract.UIPreference
