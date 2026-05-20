package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleListModels_AllProviders(t *testing.T) {
	h := HandleListModels(WithModelRegistry(testModelRegistry()))
	out, err := h(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	res, ok := out.(ListModelsResult)
	if !ok {
		t.Fatalf("unexpected return type %T", out)
	}
	if len(res.Providers) != 2 {
		t.Fatalf("Providers count = %d, want 2 (claude + codex)", len(res.Providers))
	}
}

func TestHandleListModels_FilterByProvider(t *testing.T) {
	h := HandleListModels(WithModelRegistry(testModelRegistry()))
	out, err := h(context.Background(), json.RawMessage(`{"provider":"claude"}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	res := out.(ListModelsResult)
	if len(res.Providers) != 1 || res.Providers[0].Provider != "claude" {
		t.Fatalf("filter failed: %+v", res.Providers)
	}
	for _, m := range []string{"opus", "sonnet", "haiku"} {
		found := false
		for _, x := range res.Providers[0].Models {
			if x == m {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("claude models missing %s: %+v", m, res.Providers[0].Models)
		}
	}
}

func TestHandleListModels_UnknownProviderFailsFast(t *testing.T) {
	h := HandleListModels(WithModelRegistry(testModelRegistry()))
	_, err := h(context.Background(), json.RawMessage(`{"provider":"bogus"}`))
	if err == nil || !strings.Contains(err.Error(), "model provider") {
		t.Fatalf("err = %v, want unknown provider failure", err)
	}
}

func TestHandleListModels_MissingRegistryFailsFast(t *testing.T) {
	h := HandleListModels()
	_, err := h(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "model registry is not configured") {
		t.Fatalf("err = %v, want missing registry failure", err)
	}
}

func TestHandleListModels_UsesInjectedRegistry(t *testing.T) {
	registry := stubModelRegistry{providers: []ProviderModels{
		{Provider: "local", Models: []string{"dev-model"}},
	}}
	h := HandleListModels(WithModelRegistry(registry))
	out, err := h(context.Background(), json.RawMessage(`{"provider":"local"}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	res := out.(ListModelsResult)
	if len(res.Providers) != 1 {
		t.Fatalf("Providers count = %d, want 1", len(res.Providers))
	}
	if res.Providers[0].Provider != "local" || res.Providers[0].Models[0] != "dev-model" {
		t.Fatalf("injected provider = %+v", res.Providers[0])
	}
}

func TestHandleSharedFileList_NilStoreError(t *testing.T) {
	h := HandleSharedFileList(nil)
	_, err := h(context.Background(), json.RawMessage(`{}`))
	if err == nil || err.Error() != "shared file store is not configured" {
		t.Fatalf("expected nil store error, got %v", err)
	}
}

type stubModelRegistry struct {
	providers []ProviderModels
}

func testModelRegistry() stubModelRegistry {
	return stubModelRegistry{providers: []ProviderModels{
		{Provider: "claude", Models: []string{"opus", "opus[1m]", "sonnet", "sonnet[1m]", "haiku"}},
		{Provider: "codex", Models: []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.2", "codex-auto-review"}},
	}}
}

func (r stubModelRegistry) ListProviders() ([]ProviderModels, error) {
	return append([]ProviderModels(nil), r.providers...), nil
}

func (r stubModelRegistry) LookupProvider(name string) (ProviderModels, bool, error) {
	for _, provider := range r.providers {
		if provider.Provider == name {
			return provider, true, nil
		}
	}
	return ProviderModels{}, false, nil
}
