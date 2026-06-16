package personalization

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type fakePreferenceStore struct {
	values map[string]json.RawMessage
}

func newFakePreferenceStore() *fakePreferenceStore {
	return &fakePreferenceStore{values: map[string]json.RawMessage{}}
}

// GetValue returns the saved preference value or the shared not-found sentinel.
func (s *fakePreferenceStore) GetValue(_ context.Context, cwd, key string) (json.RawMessage, error) {
	value, ok := s.values[cwd+"|"+key]
	if !ok {
		return nil, contract.ErrNotFound
	}
	return append(json.RawMessage(nil), value...), nil
}

// Upsert saves the preference value by cwd/key for later reads.
func (s *fakePreferenceStore) Upsert(_ context.Context, params contract.UIPreferenceUpsertParams) error {
	s.values[params.Cwd+"|"+params.Key] = append(json.RawMessage(nil), params.Value...)
	return nil
}

// List satisfies contract.UIPreferenceStore for tests that only read one value.
func (s *fakePreferenceStore) List(context.Context, string) ([]contract.UIPreference, error) {
	return nil, nil
}

// TestServiceSaveAndGetProfileNormalizesFields covers profile normalization
// across save and read paths.
func TestServiceSaveAndGetProfileNormalizesFields(t *testing.T) {
	store := newFakePreferenceStore()
	svc := NewService(store)

	saved, err := svc.SaveProfile(context.Background(), " /repo ", Profile{
		DisplayName:        " Alice ",
		Role:               " Engineer ",
		Background:         " Builds tools ",
		CustomInstructions: " Be concise ",
	})
	if err != nil {
		t.Fatalf("SaveProfile returned error: %v", err)
	}
	if saved.Profile.DisplayName != "Alice" || saved.Profile.Role != "Engineer" {
		t.Fatalf("profile was not normalized: %+v", saved.Profile)
	}

	loaded, err := svc.GetProfile(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("GetProfile returned error: %v", err)
	}
	if loaded.Profile != saved.Profile {
		t.Fatalf("loaded profile mismatch: got %+v want %+v", loaded.Profile, saved.Profile)
	}
}

// TestServiceGetProfileReturnsEmptyWhenMissing covers the first-run profile
// read when no preference has been saved.
func TestServiceGetProfileReturnsEmptyWhenMissing(t *testing.T) {
	svc := NewService(newFakePreferenceStore())

	result, err := svc.GetProfile(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("GetProfile returned error: %v", err)
	}
	if result.Profile != (Profile{}) {
		t.Fatalf("expected empty profile, got %+v", result.Profile)
	}
}

// TestServiceSaveProfileRejectsOverlongShortField covers the short-field
// length guard used before profile persistence.
func TestServiceSaveProfileRejectsOverlongShortField(t *testing.T) {
	svc := NewService(newFakePreferenceStore())

	_, err := svc.SaveProfile(context.Background(), "/repo", Profile{
		DisplayName: strings.Repeat("x", maxShortProfileFieldRunes+1),
	})
	if err == nil {
		t.Fatal("SaveProfile succeeded with overlong displayName")
	}
	if !strings.Contains(err.Error(), "displayName") {
		t.Fatalf("unexpected error: %v", err)
	}
}
