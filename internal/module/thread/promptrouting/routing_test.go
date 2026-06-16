package promptrouting

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestAutoRouteCandidatesPartitionsAndSorts verifies specific match_when rows
// always outrank fallback rows, while each pool remains priority ordered.
func TestAutoRouteCandidatesPartitionsAndSorts(t *testing.T) {
	t.Parallel()

	templates := []contract.PromptTemplate{
		{ID: 1, PromptKey: "specific-low", AgentKey: "agent-a", Enabled: true, MatchWhen: rawJSON(t, `{"language":"go"}`), Priority: 10},
		{ID: 2, PromptKey: "fallback-low", AgentKey: "agent-b", Enabled: true, MatchWhen: rawJSON(t, `{}`), Priority: 20},
		{ID: 3, PromptKey: "specific-high", AgentKey: "agent-c", Enabled: true, MatchWhen: rawJSON(t, `{"cwd_prefix":"/repo"}`), Priority: 90},
		{ID: 4, PromptKey: "disabled", AgentKey: "agent-d", Enabled: false, MatchWhen: rawJSON(t, `{"language":"go"}`), Priority: 1000},
		{ID: 5, PromptKey: "fallback-high", AgentKey: "agent-e", Enabled: true, MatchWhen: rawJSON(t, `{}`), Priority: 99},
		{ID: 6, PromptKey: "runtime-asset", AgentKey: "default_rule", Enabled: true, MatchWhen: rawJSON(t, `{"language":"go"}`), Priority: 1000},
		{ID: 7, PromptKey: "invalid", AgentKey: "agent-f", Enabled: true, MatchWhen: rawJSON(t, `[]`), Priority: 1000},
	}

	specific, fallback := AutoRouteCandidates(templates)

	if got, want := promptKeys(specific), []string{"specific-high", "specific-low"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("specific prompt keys = %#v, want %#v", got, want)
	}
	if got, want := promptKeys(fallback), []string{"fallback-high", "fallback-low"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback prompt keys = %#v, want %#v", got, want)
	}
}

// TestTemplateLookupRejectsRuntimeAssets verifies thread launch routing never
// selects prompt templates reserved for recall/default-rule runtime assets.
func TestTemplateLookupRejectsRuntimeAssets(t *testing.T) {
	t.Parallel()

	templates := []contract.PromptTemplate{
		{PromptKey: "asset", AgentKey: "default_rule", Enabled: true},
		{PromptKey: "disabled", AgentKey: "agent-a", Enabled: false},
		{PromptKey: "launchable", AgentKey: "agent-b", Enabled: true},
	}

	if TemplateLaunchable(templates[0]) {
		t.Fatal("TemplateLaunchable() accepted runtime asset template")
	}
	if got := FindEnabledByPromptKey(templates, "asset"); got != nil {
		t.Fatalf("FindEnabledByPromptKey(asset) = %#v, want nil", got)
	}
	if got := FindEnabledByPromptKey(templates, "disabled"); got != nil {
		t.Fatalf("FindEnabledByPromptKey(disabled) = %#v, want nil", got)
	}
	if got := FirstEnabledByAgentKey(templates, "AGENT-B"); got == nil || got.PromptKey != "launchable" {
		t.Fatalf("FirstEnabledByAgentKey() = %#v, want launchable", got)
	}
}

// TestConvertSectionsToBlocksFiltersAndMapsRegions verifies routing only
// exposes injectable prompt sections to the prompt assembler.
func TestConvertSectionsToBlocksFiltersAndMapsRegions(t *testing.T) {
	t.Parallel()

	sections := []contract.PromptTemplateSection{
		{SectionKey: "static", Region: " static ", Ordinal: 2, Body: "static body", EnableWhen: rawJSON(t, `{"language":"go"}`), Enabled: true},
		{SectionKey: "dynamic", Region: "other", Ordinal: 1, Body: "dynamic body", Enabled: true},
		{SectionKey: "disabled", Body: "nope", Enabled: false},
		{SectionKey: "recall", Body: "nope", TriggerType: " recall ", Enabled: true},
		{SectionKey: "blank", Body: " \n\t ", Enabled: true},
	}

	got := ConvertSectionsToBlocks(sections)

	if len(got) != 2 {
		t.Fatalf("ConvertSectionsToBlocks() len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Key != "static" || got[0].Region != contract.PromptRegionStatic || got[0].Ordinal != 2 || string(got[0].EnableWhen) != `{"language":"go"}` {
		t.Fatalf("static block = %#v, want static region with copied enable_when", got[0])
	}
	if got[1].Key != "dynamic" || got[1].Region != contract.PromptRegionDynamic || got[1].Ordinal != 1 {
		t.Fatalf("dynamic block = %#v, want dynamic region", got[1])
	}
}

func rawJSON(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(raw)) {
		t.Fatalf("invalid test JSON: %s", raw)
	}
	return json.RawMessage(raw)
}

func promptKeys(templates []contract.PromptTemplate) []string {
	keys := make([]string, 0, len(templates))
	for _, template := range templates {
		keys = append(keys, template.PromptKey)
	}
	return keys
}
