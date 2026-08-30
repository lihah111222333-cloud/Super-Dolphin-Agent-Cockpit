package personalization

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
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
	store := newProfilePreferenceStore()
	store.values["/repo/app\x00"+profilePreferenceKey] = mustProfileJSON(t, Profile{
		DisplayName:        "小海",
		Role:               "后端工程师",
		Background:         "熟悉 Go 和 React",
		CustomInstructions: "回答要直接",
	})
	svc := NewService(store)
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
		return nil, platformdb.ErrNotFound
	}
	return append(json.RawMessage(nil), value...), nil
}

func mustProfileJSON(t *testing.T, profile Profile) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("json.Marshal(profile) error = %v", err)
	}
	return raw
}
