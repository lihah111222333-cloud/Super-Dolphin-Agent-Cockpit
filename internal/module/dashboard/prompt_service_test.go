package dashboard

import (
	"context"
	"encoding/json"
	"testing"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func TestPromptServiceListPromptsFiltersVisibility(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/global",
		Title:       "Global",
		AgentKey:    "main",
		PromptText:  "global",
		Description: "global",
		Variables:   json.RawMessage("{}"),
		Tags:        json.RawMessage("[]"),
		Enabled:     true,
	})
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/repo",
		Title:       "Repo",
		AgentKey:    "main",
		PromptText:  "repo",
		Description: "repo",
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), "/repo"),
		Enabled:     true,
	})
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/other",
		Title:       "Other",
		AgentKey:    "main",
		PromptText:  "other",
		Description: "other",
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), "/other"),
		Enabled:     true,
	})
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/disabled",
		Title:       "Disabled",
		AgentKey:    "main",
		PromptText:  "disabled",
		Description: "disabled",
		Variables:   json.RawMessage("{}"),
		Tags:        json.RawMessage("[]"),
		Enabled:     false,
	})

	svc := newPromptService(store, promptStubTxRunner{base: store})
	prompts, err := svc.ListPrompts(context.Background(), "/repo", "")
	if err != nil {
		t.Fatalf("ListPrompts() error = %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("ListPrompts() len = %d, want 2", len(prompts))
	}
	if prompts[0].PromptKey != "main/repo" || prompts[1].PromptKey != "main/global" {
		t.Fatalf("ListPrompts() keys = [%s %s], want [main/repo main/global]", prompts[0].PromptKey, prompts[1].PromptKey)
	}
}

func TestPromptServiceGetPromptAppliesVisibility(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/repo",
		Title:       "Repo",
		AgentKey:    "main",
		PromptText:  "repo",
		Description: "repo",
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), "/repo"),
		Enabled:     true,
	})
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/other",
		Title:       "Other",
		AgentKey:    "main",
		PromptText:  "other",
		Description: "other",
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), "/other"),
		Enabled:     true,
	})
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/disabled",
		Title:       "Disabled",
		AgentKey:    "main",
		PromptText:  "disabled",
		Description: "disabled",
		Variables:   json.RawMessage("{}"),
		Tags:        json.RawMessage("[]"),
		Enabled:     false,
	})

	svc := newPromptService(store, promptStubTxRunner{base: store})

	got, err := svc.GetPrompt(context.Background(), "/repo", "main/repo")
	if err != nil {
		t.Fatalf("GetPrompt(repo) error = %v", err)
	}
	if got == nil || got.PromptKey != "main/repo" {
		t.Fatalf("GetPrompt(repo) = %#v", got)
	}

	_, err = svc.GetPrompt(context.Background(), "/repo", "main/other")
	if !platformdb.IsNotFound(err) {
		t.Fatalf("GetPrompt(other) error = %v, want not found", err)
	}

	_, err = svc.GetPrompt(context.Background(), "", "main/disabled")
	if !platformdb.IsNotFound(err) {
		t.Fatalf("GetPrompt(disabled) error = %v, want not found", err)
	}
}
