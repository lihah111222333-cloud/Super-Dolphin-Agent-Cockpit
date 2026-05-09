package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHandleListModels_AllProviders(t *testing.T) {
	h := HandleListModels()
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
	h := HandleListModels()
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

func TestHandleListModels_UnknownProviderReturnsEmpty(t *testing.T) {
	h := HandleListModels()
	out, err := h(context.Background(), json.RawMessage(`{"provider":"bogus"}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	res := out.(ListModelsResult)
	if len(res.Providers) != 0 {
		t.Fatalf("unknown provider should return empty, got %+v", res.Providers)
	}
}

func TestHandleSharedFileList_NilStoreError(t *testing.T) {
	h := HandleSharedFileList(nil)
	_, err := h(context.Background(), json.RawMessage(`{}`))
	if err == nil || err.Error() != "shared file store is not configured" {
		t.Fatalf("expected nil store error, got %v", err)
	}
}
