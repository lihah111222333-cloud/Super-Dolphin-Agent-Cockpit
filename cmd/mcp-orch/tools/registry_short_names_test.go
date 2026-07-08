package tools

import "testing"

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

	for _, want := range []string{"launch_agent", "send_message", "stop_agent", "recover_agent", "interrupt_agent", "list_agents", "get_agent_report"} {
		if !got[want] {
			t.Fatalf("registry.List() missing short orchestration tool %q; got %#v", want, got)
		}
	}
	for _, legacy := range []string{"orchestration_launch_agent", "orchestration_send_message", "orchestration_stop_agent", "orchestration_recover_agent", "orchestration_interrupt_agent", "orchestration_list_agents", "orchestration_get_agent_report"} {
		if got[legacy] {
			t.Fatalf("registry.List() exposed legacy orchestration alias %q; got %#v", legacy, got)
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

func TestRegistryLookupAcceptsLegacyOrchestrationAliases(t *testing.T) {
	registry := NewRegistry(Dependencies{})

	for legacy, canonical := range legacyOrchestrationAliases {
		legacyTool, ok := registry.Lookup(legacy)
		if !ok {
			t.Fatalf("registry.Lookup(%q) = false, want true", legacy)
		}
		canonicalTool, ok := registry.Lookup(canonical)
		if !ok {
			t.Fatalf("registry.Lookup(%q) = false, want true", canonical)
		}
		if legacyTool.Name != canonical {
			t.Fatalf("registry.Lookup(%q).Name = %q, want canonical %q", legacy, legacyTool.Name, canonical)
		}
		if legacyTool.Name != canonicalTool.Name {
			t.Fatalf("registry.Lookup(%q).Name = %q, registry.Lookup(%q).Name = %q", legacy, legacyTool.Name, canonical, canonicalTool.Name)
		}
	}
}
