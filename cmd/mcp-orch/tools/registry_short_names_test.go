package tools

import (
	"sort"
	"strings"
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

	for _, canonical := range contract.OrchestrationToolCanonicalNames() {
		if !got[canonical] {
			t.Fatalf("registry.List() missing short orchestration tool %q; got %#v", canonical, got)
		}
	}
	for _, legacy := range []string{"orchestration_launch_agent", "orchestration_list_agents"} {
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

func TestRegistryLookupRejectsLegacyOrchestrationAliases(t *testing.T) {
	registry := NewRegistry(Dependencies{})

	for _, legacy := range []string{"orchestration_launch_agent", "orchestration_list_agents"} {
		if _, ok := registry.Lookup(legacy); ok {
			t.Fatalf("registry.Lookup(%q) = true, want false", legacy)
		}
	}
	for _, canonical := range contract.OrchestrationToolCanonicalNames() {
		canonicalTool, ok := registry.Lookup(canonical)
		if !ok {
			t.Fatalf("registry.Lookup(%q) = false, want true", canonical)
		}
		if canonicalTool.Name != canonical {
			t.Fatalf("registry.Lookup(%q).Name = %q, want %q", canonical, canonicalTool.Name, canonical)
		}
	}
}

func TestOrchestrationRegistryDefinitionsStayInReadOnlyDenylist(t *testing.T) {
	canonical := make(map[string]bool)
	for _, name := range contract.OrchestrationToolDenylist() {
		canonical[name] = true
	}
	denied := make(map[string]bool)
	for _, name := range contract.ReadOnlyAgentDeniedTools() {
		denied[name] = true
	}

	for _, def := range orchestrationToolDefinitions(ToolPorts{}) {
		if !canonical[def.Name] {
			t.Fatalf("orchestration tool definition %q missing from contract.OrchestrationToolDenylist()", def.Name)
		}
		if !denied[def.Name] {
			t.Fatalf("orchestration tool definition %q missing from contract.ReadOnlyAgentDeniedTools()", def.Name)
		}
	}
}

func TestReadOnlyDenylistCoversWritableRegistryTools(t *testing.T) {
	registry := NewRegistry(Dependencies{})
	defs, err := registry.List()
	if err != nil {
		t.Fatalf("registry.List() error = %v", err)
	}

	denied := toolNameSet(contract.ReadOnlyAgentDeniedTools())
	exemptions := readOnlyRegistryDenylistExemptions()
	missing := uncoveredWritableRegistryTools(defs, denied, exemptions)
	if len(missing) > 0 {
		t.Fatalf("writable/high-risk registry tools missing from contract.ReadOnlyAgentDeniedTools() or explicit exemption: %s", strings.Join(missing, ", "))
	}

	assertReadOnlyRegistryDenylistExemptions(t, registry, denied, exemptions)
}

func toolNameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func uncoveredWritableRegistryTools(defs []ToolDefinition, denied map[string]bool, exemptions map[string]string) []string {
	var missing []string
	for _, def := range defs {
		reason, required := registryToolRequiresReadOnlyDeny(def)
		if required && !denied[def.Name] && strings.TrimSpace(exemptions[def.Name]) == "" {
			missing = append(missing, def.Name+" ("+reason+")")
		}
	}
	sort.Strings(missing)
	return missing
}

func assertReadOnlyRegistryDenylistExemptions(t *testing.T, registry Registry, denied map[string]bool, exemptions map[string]string) {
	t.Helper()
	for tool, reason := range exemptions {
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("read-only denylist exemption %q must include a reason", tool)
		}
		if denied[tool] {
			t.Fatalf("read-only denylist exemption %q is stale: tool is already denied", tool)
		}
		def, ok := registry.Lookup(tool)
		if !ok {
			t.Fatalf("read-only denylist exemption %q is stale: tool is not exposed by registry", tool)
		}
		if _, required := registryToolRequiresReadOnlyDeny(def); !required {
			t.Fatalf("read-only denylist exemption %q is stale: tool is not classified as writable/high-risk", tool)
		}
	}
}

var legacyWritableRegistryToolClassifications = map[string]string{
	"workspace_create_run": "legacy workspace definition lacks ToolMetadata; creates persistent workspace run state",
	"workspace_merge_run":  "legacy workspace definition lacks ToolMetadata; writes workspace changes back to the source root",
	"workspace_abort_run":  "legacy workspace definition lacks ToolMetadata; mutates persistent workspace run state",
}

func readOnlyRegistryDenylistExemptions() map[string]string {
	return map[string]string{}
}

func registryToolRequiresReadOnlyDeny(def ToolDefinition) (string, bool) {
	if reason, ok := legacyWritableRegistryToolClassifications[def.Name]; ok {
		return reason, true
	}
	if def.Metadata.RiskClass == ToolRiskHigh {
		return "metadata risk_class=high", true
	}
	switch def.Metadata.Permission {
	case ToolPermissionWorkflowWrite, ToolPermissionSharedFileWrite, ToolPermissionCommandExecute:
		return "metadata permission=" + string(def.Metadata.Permission), true
	}
	if len(def.Metadata.PathPolicy.WriteFields) > 0 {
		return "metadata path_policy.write_fields", true
	}
	for _, capability := range def.Metadata.Capabilities {
		capability = strings.TrimSpace(capability)
		if strings.HasSuffix(capability, ".write") || strings.HasSuffix(capability, ".execute") {
			return "metadata capability=" + capability, true
		}
	}
	return "", false
}
