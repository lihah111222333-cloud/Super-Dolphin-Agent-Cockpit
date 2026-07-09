package tools

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestRegistryAdvertisesShortOrchestrationNames(t *testing.T) {
	registry := NewRegistry(Dependencies{})
	got := make(map[string]bool)
	list, err := registry.List()
	if err != nil {
		t.Fatalf("registry.List() error = %v", err)
	}
	for _, tool := range list {
		got[tool.Name] = true
	}

	for _, alias := range contract.OrchestrationToolAliases() {
		if !got[alias.Canonical] {
			t.Fatalf("registry.List() missing short orchestration tool %q; got %#v", alias.Canonical, got)
		}
		if got[alias.LegacyPeerRealName] {
			t.Fatalf("registry.List() exposed legacy orchestration alias %q; got %#v", alias.LegacyPeerRealName, got)
		}
	}
}

func TestRegistryAdvertisesDAGConsoleReadTools(t *testing.T) {
	registry := NewRegistry(Dependencies{})
	got := make(map[string]bool)
	list, err := registry.List()
	if err != nil {
		t.Fatalf("registry.List() error = %v", err)
	}
	for _, tool := range list {
		got[tool.Name] = true
	}

	for _, want := range []string{"task_list_dags", "task_get_dag", "task_list_runs", "task_start_dag", "task_terminate_dag", "task_delete_dag"} {
		if !got[want] {
			t.Fatalf("registry.List() missing DAG console tool %q; got %#v", want, got)
		}
	}
}

func TestRegistryExposesOnlyVideoWithAudioGeneration(t *testing.T) {
	registry := NewRegistry(Dependencies{})

	if _, ok := registry.Lookup("video_with_audio"); !ok {
		t.Fatal("registry.Lookup(video_with_audio) = false, want true")
	}
	if _, ok := registry.Lookup("video_generate"); ok {
		t.Fatal("registry.Lookup(video_generate) = true, want false")
	}
}

func TestRegistryLookupRejectsLegacyOrchestrationAliases(t *testing.T) {
	registry := NewRegistry(Dependencies{})

	for _, alias := range contract.OrchestrationToolAliases() {
		if _, ok := registry.Lookup(alias.LegacyPeerRealName); ok {
			t.Fatalf("registry.Lookup(%q) = true, want false", alias.LegacyPeerRealName)
		}
		canonicalTool, ok := registry.Lookup(alias.Canonical)
		if !ok {
			t.Fatalf("registry.Lookup(%q) = false, want true", alias.Canonical)
		}
		if canonicalTool.Name != alias.Canonical {
			t.Fatalf("registry.Lookup(%q).Name = %q, want %q", alias.Canonical, canonicalTool.Name, alias.Canonical)
		}
	}
}
