package contract

import (
	"encoding/json"
	"testing"
)

// TestPromptSnapshotUnmarshalAcceptsLegacyShape verifies old snake_case rows still decode.
func TestPromptSnapshotUnmarshalAcceptsLegacyShape(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"display_name": " Agent ",
		"base_instructions": " Base ",
		"developer_instructions": " Dev ",
		"provider": "codex",
		"version": 2,
		"hash": "hash",
		"section_snapshot": {
			" intro ": " hello ",
			"drop": "",
			"": "ignored"
		},
		"generation": 7
	}`)

	var snapshot PromptSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if snapshot.DisplayName != "Agent" {
		t.Fatalf("DisplayName = %q, want Agent", snapshot.DisplayName)
	}
	if snapshot.BaseInstructions != "Base" {
		t.Fatalf("BaseInstructions = %q, want Base", snapshot.BaseInstructions)
	}
	if snapshot.DeveloperInstructions != "Dev" {
		t.Fatalf("DeveloperInstructions = %q, want Dev", snapshot.DeveloperInstructions)
	}
	if snapshot.Provider != "codex" || snapshot.Version != 2 || snapshot.Hash != "hash" {
		t.Fatalf("snapshot metadata = provider %q version %d hash %q", snapshot.Provider, snapshot.Version, snapshot.Hash)
	}
	if snapshot.Generation != 7 {
		t.Fatalf("Generation = %d, want 7", snapshot.Generation)
	}
	if got := snapshot.SectionSnapshot["intro"]; got != "hello" {
		t.Fatalf("SectionSnapshot[intro] = %q, want hello", got)
	}
	if len(snapshot.SectionSnapshot) != 1 {
		t.Fatalf("SectionSnapshot len = %d, want 1", len(snapshot.SectionSnapshot))
	}
}

// TestPromptSnapshotUnmarshalModernFieldsWin verifies legacy fallback does not overwrite modern JSON.
func TestPromptSnapshotUnmarshalModernFieldsWin(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"displayName": "Modern",
		"baseInstructions": "modern base",
		"sectionSnapshot": {"modern": "yes"},
		"display_name": "Legacy",
		"base_instructions": "legacy base",
		"section_snapshot": {"legacy": "no"}
	}`)

	var snapshot PromptSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if snapshot.DisplayName != "Modern" || snapshot.BaseInstructions != "modern base" {
		t.Fatalf("modern fields were not preserved: %#v", snapshot)
	}
	if _, ok := snapshot.SectionSnapshot["legacy"]; ok {
		t.Fatalf("legacy section should not overwrite modern section map: %#v", snapshot.SectionSnapshot)
	}
	if got := snapshot.SectionSnapshot["modern"]; got != "yes" {
		t.Fatalf("SectionSnapshot[modern] = %q, want yes", got)
	}
}
