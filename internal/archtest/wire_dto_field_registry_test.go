package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

type skillInfoConsumerField struct {
	Field     string `json:"field"`
	Validator string `json:"validator"`
	Consumer  string `json:"consumer"`
}

type embeddedWireDTOFields struct {
	Nested string `json:"nested"`
}

type embeddedPointerWireDTOFields struct {
	*embeddedWireDTOFields
	ID string `json:"id"`
}

func TestRegisteredWireDTOJSONFieldsDereferencesEmbeddedPointer(t *testing.T) {
	t.Parallel()

	fields := collectRegisteredWireDTOJSONFields(t, reflect.TypeFor[embeddedPointerWireDTOFields]())
	for _, field := range []string{"id", "nested"} {
		if !fields[field] {
			t.Fatalf("embedded pointer producer field %q was not collected: %v", field, fields)
		}
	}
	if len(fields) != 2 {
		t.Fatalf("embedded pointer producer fields = %v, want exactly id and nested", fields)
	}
}

// TestSkillInfoConsumerRegistryMatchesProducerJSONFields 将 SkillInfo producer 字段绑定到前端 validator registry。
func TestSkillInfoConsumerRegistryMatchesProducerJSONFields(t *testing.T) {
	t.Parallel()

	fields := collectRegisteredWireDTOJSONFields(t, reflect.TypeFor[contract.SkillInfo]())
	registry := loadSkillInfoConsumerRegistry(t)
	registered := make(map[string]string, len(registry))
	for _, entry := range registry {
		if strings.TrimSpace(entry.Field) == "" || strings.TrimSpace(entry.Validator) == "" || strings.TrimSpace(entry.Consumer) == "" {
			t.Fatalf("SkillInfo consumer registry contains incomplete entry: %+v", entry)
		}
		if _, exists := registered[entry.Field]; exists {
			t.Fatalf("SkillInfo consumer registry contains duplicate field %q", entry.Field)
		}
		registered[entry.Field] = entry.Validator
	}

	assertRegisteredWireDTOFieldsCovered(t, "internal/contract.SkillInfo -> skillSlashCommandAdapter", fields, registered)
	assertRegisteredWireDTORegistryCurrent(t, "internal/contract.SkillInfo -> skillSlashCommandAdapter", fields, registered)
}

func TestAgentBoardWireFieldRegistryMatchesEveryProducerLayer(t *testing.T) {
	t.Parallel()
	producer := collectRegisteredWireDTOJSONFields(t, reflect.TypeFor[agentdto.BoardView]())
	registered := map[string]string{
		"id": "runtime.id", "threadId": "runtime.threadID", "parentAgentId": "launch.parentID", "name": "launch.name",
		"assignment": "launch.name+prompt+startedAt", "progress": "state+updatedAt", "outcome": "terminal/lifecycle event",
	}
	assertRegisteredWireDTOFieldsCovered(t, "agent.BoardView -> UI boundary", producer, registered)
	assertRegisteredWireDTORegistryCurrent(t, "agent.BoardView -> UI boundary", producer, registered)

	for layer, check := range map[string]struct {
		typ    reflect.Type
		fields []string
	}{
		"contract.AgentSnapshot": {reflect.TypeFor[contract.AgentSnapshot](), []string{"assignment", "progress", "outcome"}},
		"ui.UIThreadPatch":       {reflect.TypeFor[uidto.UIThreadPatch](), []string{"agent"}},
		"agent.StateChanged":     {reflect.TypeFor[agentdto.StateChanged](), []string{"board"}},
		"agent.AgentLaunched":    {reflect.TypeFor[agentdto.AgentLaunched](), []string{"board", "provider_thread_id"}},
		"agent.AgentStopped":     {reflect.TypeFor[agentdto.AgentStopped](), []string{"board"}},
		"agent.AgentRecovering":  {reflect.TypeFor[agentdto.AgentRecovering](), []string{"board"}},
		"agent.AgentFailed":      {reflect.TypeFor[agentdto.AgentFailed](), []string{"board"}},
		"thread.Started":         {reflect.TypeFor[threaddto.Started](), []string{"board"}},
	} {
		fields := collectRegisteredWireDTOJSONFields(t, check.typ)
		for _, field := range check.fields {
			if !fields[field] {
				t.Fatalf("%s missing Agent board field %q", layer, field)
			}
		}
	}

	now := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	board := agentdto.BoardView{
		ID: "agent-1", ThreadID: "thread-1", ParentAgentID: "agent-root", Name: "worker",
		Assignment: &agentdto.Assignment{Title: "任务", Description: "说明", AssignedAt: now},
		Progress:   agentdto.Progress{Status: "failed", UpdatedAt: now},
		Outcome:    &agentdto.Outcome{Kind: agentdto.OutcomeKindFailure, Reason: "boom", CompletedAt: now},
	}
	data, err := json.Marshal(board)
	if err != nil {
		t.Fatalf("json.Marshal(BoardView) error = %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("json.Unmarshal(BoardView) error = %v", err)
	}
	for field := range producer {
		if _, ok := wire[field]; !ok {
			t.Fatalf("BoardView serialization missing producer field %q: %s", field, data)
		}
	}
}

func loadSkillInfoConsumerRegistry(t *testing.T) []skillInfoConsumerField {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve wire DTO field registry test path")
	}
	path := filepath.Join(
		filepath.Dir(currentFile),
		"..", "..", "frontend-app", "src", "features", "slash-commands", "adapters", "skillInfoFieldRegistry.json",
	)
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read SkillInfo consumer registry: %v", err)
	}
	var registry []skillInfoConsumerField
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatalf("decode SkillInfo consumer registry: %v", err)
	}
	if len(registry) == 0 {
		t.Fatal("SkillInfo consumer registry must not be empty")
	}
	return registry
}

func collectRegisteredWireDTOJSONFields(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	descriptors, err := wireDTOJSONFields(typ)
	if err != nil {
		t.Fatalf("collect wire DTO producer fields: %v", err)
	}
	fields := make(map[string]bool, len(descriptors))
	for _, descriptor := range descriptors {
		fields[descriptor.jsonName] = true
	}
	return fields
}

func assertRegisteredWireDTOFieldsCovered(t *testing.T, name string, fields map[string]bool, registered map[string]string) {
	t.Helper()
	var missing []string
	for field := range fields {
		if _, ok := registered[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%s has JSON fields missing from consumer registry: %v", name, missing)
	}
}

func assertRegisteredWireDTORegistryCurrent(t *testing.T, name string, fields map[string]bool, registered map[string]string) {
	t.Helper()
	var stale []string
	for field := range registered {
		if !fields[field] {
			stale = append(stale, field)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("%s consumer registry references fields that no longer exist: %v", name, stale)
	}
}
