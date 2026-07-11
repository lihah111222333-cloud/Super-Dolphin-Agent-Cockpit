package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
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
	if len(res.Data) != 2 || res.Total != 2 || res.Showing != 2 || res.Truncated || res.Hint == "" {
		t.Fatalf("model list envelope = %+v", res)
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

func TestHandleListModels_MarksCodexAlwaysAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "")
	t.Setenv("SUPER_DOLPHIN_HOME", "")

	h := HandleListModels(WithModelRegistry(testModelRegistry()))
	out, err := h(context.Background(), json.RawMessage(`{"provider":"codex"}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	provider, raw := decodeFirstProviderModelsJSON(t, out)
	if provider.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", provider.Provider)
	}
	if len(provider.Models) == 0 {
		t.Fatalf("codex models missing: %+v", provider)
	}
	if provider.Available != true {
		t.Fatalf("codex available = %#v, want true; json=%s", provider.Available, raw)
	}
}

func TestHandleSharedFileList_NilStoreError(t *testing.T) {
	h := HandleSharedFileList(nil)
	_, err := h(context.Background(), json.RawMessage(`{}`))
	if err == nil || err.Error() != "shared file store is not configured" {
		t.Fatalf("expected nil store error, got %v", err)
	}
}

func TestHandleSharedFileList_ReturnsEnvelopeWithLegacyFiles(t *testing.T) {
	updatedAt := time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC)
	store := &stubSharedFileListStore{files: []sharedfilestore.SharedFile{{
		Path:      "reports/summary.md",
		UpdatedBy: "agent",
		UpdatedAt: updatedAt,
	}}}
	h := HandleSharedFileList(store)
	out, err := h(context.Background(), json.RawMessage(`{"prefix":"reports/","limit":5}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if store.got != (sharedfilestore.ListFilter{Prefix: "reports/", Limit: 5}) {
		t.Fatalf("shared file filter = %#v", store.got)
	}
	res, ok := out.(SharedFileListResult)
	if !ok {
		t.Fatalf("unexpected return type %T", out)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "reports/summary.md" {
		t.Fatalf("legacy files = %+v", res.Files)
	}
	assertEnvelopeCounts(t, "HandleSharedFileList()", len(res.Data), res.Total, res.Showing, res.Truncated, res.Hint)
	if res.Data[0].Path != "reports/summary.md" {
		t.Fatalf("shared file list data = %+v", res.Data)
	}
	if len(res.AllowedPrefixes) == 0 {
		t.Fatalf("shared file allowed prefixes missing = %+v", res)
	}
	if res.AllowedPrefixHint == "" {
		t.Fatalf("shared file allowed prefix hint missing = %+v", res)
	}
}

func TestSharedFileListDefaultLimit(t *testing.T) {
	store := &stubSharedFileListStore{}
	h := HandleSharedFileList(store)
	if _, err := h(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("HandleSharedFileList() error = %v", err)
	}
	if store.got.Limit != 50 {
		t.Fatalf("shared_file_list default limit = %d, want 50", store.got.Limit)
	}
}

func TestSharedFileListLimitMaxCap(t *testing.T) {
	store := &stubSharedFileListStore{}
	h := HandleSharedFileList(store)
	if _, err := h(context.Background(), json.RawMessage(`{"limit":500}`)); err != nil {
		t.Fatalf("HandleSharedFileList() max cap error = %v", err)
	}
	if store.got.Limit != 200 {
		t.Fatalf("shared_file_list capped limit = %d, want 200", store.got.Limit)
	}
	if _, err := h(context.Background(), json.RawMessage(`{"limit":-1}`)); err == nil {
		t.Fatal("HandleSharedFileList() negative limit error = nil, want rejection")
	}
}

type providerModelsJSON struct {
	Provider          string   `json:"provider"`
	Models            []string `json:"models"`
	Available         any      `json:"available"`
	UnavailableReason string   `json:"unavailable_reason"`
}

func decodeFirstProviderModelsJSON(t *testing.T, out any) (providerModelsJSON, []byte) {
	t.Helper()
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal(ListModelsResult) error = %v", err)
	}
	var decoded struct {
		Providers []providerModelsJSON `json:"providers"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal(ListModelsResult JSON) error = %v; json=%s", err, raw)
	}
	if len(decoded.Providers) != 1 {
		t.Fatalf("providers = %+v, want exactly one provider", decoded.Providers)
	}
	return decoded.Providers[0], raw
}

type stubModelRegistry struct {
	providers []ProviderModels
}

type stubSharedFileListStore struct {
	files []sharedfilestore.SharedFile
	got   sharedfilestore.ListFilter
}

func (s *stubSharedFileListStore) Get(context.Context, string) (*sharedfilestore.SharedFile, error) {
	return nil, nil
}

func (s *stubSharedFileListStore) List(_ context.Context, filter sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	s.got = filter
	return append([]sharedfilestore.SharedFile(nil), s.files...), nil
}

func (s *stubSharedFileListStore) Upsert(context.Context, sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	return nil, nil
}

func (s *stubSharedFileListStore) Delete(context.Context, string) (int64, error) {
	return 0, nil
}

func testModelRegistry() stubModelRegistry {
	return stubModelRegistry{providers: []ProviderModels{
		{Provider: "claude", Models: []string{"opus", "opus[1m]", "sonnet", "sonnet[1m]", "haiku"}},
		{Provider: "codex", Models: []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5", "codex-auto-review"}},
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
