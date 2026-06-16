package personalization

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

func TestServiceGetProfileReturnsEmptyWhenPreferenceMissing(t *testing.T) {
	store := newProfilePreferenceStore()
	svc := NewService(store)

	got, err := svc.GetProfile(context.Background(), "/repo/app")
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if got.Profile != (Profile{}) {
		t.Fatalf("GetProfile() = %#v, want empty profile", got.Profile)
	}
}

func TestServiceSaveProfileTrimsAndPersists(t *testing.T) {
	store := newProfilePreferenceStore()
	svc := NewService(store)

	saved, err := svc.SaveProfile(context.Background(), "/repo/app", Profile{
		DisplayName:        "  小海  ",
		Role:               "  后端工程师  ",
		Background:         "  熟悉 Go 和 React  ",
		CustomInstructions: "  回答要直接  ",
	})
	if err != nil {
		t.Fatalf("SaveProfile() error = %v", err)
	}
	if saved.Profile.DisplayName != "小海" || saved.Profile.Role != "后端工程师" {
		t.Fatalf("SaveProfile() = %#v, want trimmed fields", saved.Profile)
	}

	loaded, err := svc.GetProfile(context.Background(), "/repo/app")
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if loaded.Profile != saved.Profile {
		t.Fatalf("loaded profile = %#v, want %#v", loaded.Profile, saved.Profile)
	}
}

func TestServiceSaveProfileRejectsMissingCWD(t *testing.T) {
	svc := NewService(newProfilePreferenceStore())

	_, err := svc.SaveProfile(context.Background(), "", Profile{DisplayName: "小海"})
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("SaveProfile() error = %v, want cwd validation", err)
	}
}

func TestServiceSaveProfileRejectsOverlongField(t *testing.T) {
	svc := NewService(newProfilePreferenceStore())

	_, err := svc.SaveProfile(context.Background(), "/repo/app", Profile{
		DisplayName: strings.Repeat("名", maxShortProfileFieldRunes+1),
	})
	if err == nil || !strings.Contains(err.Error(), "displayName") {
		t.Fatalf("SaveProfile() error = %v, want displayName validation", err)
	}
}

func TestServiceGetProfileFailsOnInvalidJSON(t *testing.T) {
	store := newProfilePreferenceStore()
	store.values["/repo/app\x00"+profilePreferenceKey] = json.RawMessage(`{"displayName":`)
	svc := NewService(store)

	_, err := svc.GetProfile(context.Background(), "/repo/app")
	if err == nil {
		t.Fatal("GetProfile() error = nil, want invalid JSON error")
	}
}

func TestPromptProviderSkipsEmptyProfile(t *testing.T) {
	svc := NewService(newProfilePreferenceStore())
	provider := NewPromptProvider(svc)

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		BuildCtx: contract.BuildCtx{CWD: "/repo/app"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want nil", *text)
	}
}

func TestPromptProviderRendersNonEmptyProfile(t *testing.T) {
	svc := NewService(newProfilePreferenceStore())
	if _, err := svc.SaveProfile(context.Background(), "/repo/app", Profile{
		DisplayName:        "小海",
		Role:               "后端工程师",
		Background:         "熟悉 Go 和 React",
		CustomInstructions: "回答要直接",
	}); err != nil {
		t.Fatalf("SaveProfile() error = %v", err)
	}
	provider := NewPromptProvider(svc)

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		BuildCtx: contract.BuildCtx{CWD: "/repo/app"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want profile text")
	}
	for _, want := range []string{"# 用户个人资料", "小海", "后端工程师", "熟悉 Go 和 React", "回答要直接"} {
		if !strings.Contains(*text, want) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, want)
		}
	}
}

type profilePreferenceStore struct {
	values map[string]json.RawMessage
	err    error
}

func newProfilePreferenceStore() *profilePreferenceStore {
	return &profilePreferenceStore{values: map[string]json.RawMessage{}}
}

func (s *profilePreferenceStore) GetValue(_ context.Context, cwd, key string) (json.RawMessage, error) {
	if s.err != nil {
		return nil, s.err
	}
	value := s.values[cwd+"\x00"+key]
	if len(value) == 0 {
		return nil, nil
	}
	return append(json.RawMessage(nil), value...), nil
}

func (s *profilePreferenceStore) Upsert(_ context.Context, params uipreference.UpsertParams) error {
	if s.err != nil {
		return s.err
	}
	if params.Cwd == "" || params.Key == "" {
		return errors.New("missing preference identity")
	}
	s.values[params.Cwd+"\x00"+params.Key] = append(json.RawMessage(nil), params.Value...)
	return nil
}

func (s *profilePreferenceStore) List(context.Context, string) ([]uipreference.UIPreference, error) {
	return nil, errors.New("not used")
}
