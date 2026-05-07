package skilllibrary

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// storeAdapter wraps *Store so it satisfies contract.SkillLibraryLister,
// converting []SkillEntry → []contract.SkillEntry on the fly.
type storeAdapter struct {
	store *Store
}

// NewLister returns a contract.SkillLibraryLister backed by the given Store.
// Returns nil when store is nil so callers can safely wire optional deps.
func NewLister(store *Store) contract.SkillLibraryLister {
	if store == nil {
		return nil
	}
	return &storeAdapter{store: store}
}

func (a *storeAdapter) List() ([]contract.SkillEntry, error) {
	entries, err := a.store.List()
	if err != nil {
		return nil, err
	}
	out := make([]contract.SkillEntry, len(entries))
	for i, e := range entries {
		out[i] = convertEntry(e)
	}
	return out, nil
}

func convertEntry(e SkillEntry) contract.SkillEntry {
	if e.Meta == nil {
		return contract.SkillEntry{}
	}
	return contract.SkillEntry{
		Meta: &contract.SkillEntryMeta{
			Name:           e.Meta.Name,
			Disabled:       e.Meta.Disabled,
			ReplacesNative: e.Meta.ReplacesNative,
		},
	}
}
