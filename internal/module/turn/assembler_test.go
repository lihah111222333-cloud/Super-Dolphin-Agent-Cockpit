package turn

import (
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestInputAssemblerAssembleNormalizesFiltersAndDeduplicates verifies that turn
// input assembly keeps safe references, rejects unsafe files, and removes
// duplicates before provider submission.
func TestInputAssemblerAssembleNormalizesFiltersAndDeduplicates(t *testing.T) {
	assembler := &inputAssembler{}

	got := assembler.Assemble(PrepareInput{
		Prompt: "  hello  ",
		Inputs: []InputItem{
			{Type: "text", Content: "hello"},
			{Type: "image", URL: " https://example.test/img.png?token=secret ", Name: " remote image "},
			{Type: "image", Path: "/tmp/drop.txt"},
			{Type: "mention", Path: "https://example.test/readme.md?download=1"},
			{Type: "mention", Path: "https://example.test/tool.exe"},
			{Type: "filecontent", Content: " body ", Path: "/tmp/note.txt"},
		},
	})

	want := []InputItem{
		{Type: "text", Content: "hello"},
		{Type: "image", Name: "remote image", URL: "https://example.test/img.png?token=secret"},
		{Type: "mention", Path: "https://example.test/readme.md?download=1", Name: "readme.md"},
		{Type: "filecontent", Content: "body", Name: "note.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Assemble() mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// TestInputAssemblerPromptTextIncludesOnlyPromptAndTextualInputs verifies that
// provider prompt text is built only from textual turn inputs.
func TestInputAssemblerPromptTextIncludesOnlyPromptAndTextualInputs(t *testing.T) {
	assembler := &inputAssembler{}

	got := assembler.PromptText(PrepareInput{
		Prompt: " prompt ",
		Inputs: []InputItem{
			{Type: "text", Content: " body "},
			{Type: "filecontent", Content: " file content "},
			{Type: "image", URL: "https://example.test/img.png"},
			{Type: "mention", Path: "/tmp/readme.md"},
		},
	})

	want := "prompt\n\nbody\n\nfile content"
	if got != want {
		t.Fatalf("PromptText() = %q, want %q", got, want)
	}
}

// TestBuildTurnStartInputsSeparatesSkillMentionsAndTrimsInputFields verifies
// that RPC input items are split into skill selections and normalized inputs.
func TestBuildTurnStartInputsSeparatesSkillMentionsAndTrimsInputFields(t *testing.T) {
	items, skills := buildTurnStartInputs([]turnInputItemParams{
		{Type: " skill ", Name: " code-review "},
		{Text: " hello "},
		{URL: " https://example.test/image.png "},
		{Path: " /tmp/readme.md "},
		{},
	})

	wantSkills := []string{"code-review"}
	if !reflect.DeepEqual(skills, wantSkills) {
		t.Fatalf("skills = %#v, want %#v", skills, wantSkills)
	}
	wantItems := []InputItem{
		{Type: "text", Content: "hello"},
		{Type: "image", URL: "https://example.test/image.png"},
		{Type: "mention", Path: "/tmp/readme.md"},
	}
	if !reflect.DeepEqual(items, wantItems) {
		t.Fatalf("items = %#v, want %#v", items, wantItems)
	}
}

// TestBuildPrepareInputMergesRuntimeConfigAndManagedSubagentPolicy verifies
// runtime fallback merging and managed-subagent tool replacement.
func TestBuildPrepareInputMergesRuntimeConfigAndManagedSubagentPolicy(t *testing.T) {
	input := buildPrepareInput(prepareInputSpec{
		Provider:     " explicit-provider ",
		EnabledTools: []string{"orchestration_launch_agent", "spawn_agent", "shell"},
		ThreadRuntimeConfig: map[string]any{
			"model":         "gpt-5",
			"cwd":           "/tmp/project",
			"prompt_key":    "runtime-prompt",
			"is_worktree":   true,
			"session_flags": map[string]any{"persistent_subagent_default": true},
			"summary":       " runtime summary ",
		},
	}, prepareSkillSpec{}, nil)

	if input.Provider != "explicit-provider" {
		t.Fatalf("Provider = %q, want explicit-provider", input.Provider)
	}
	if input.Model != "gpt-5" || input.CWD != "/tmp/project" || input.PromptKey != "runtime-prompt" {
		t.Fatalf("runtime config was not merged: %#v", input)
	}
	if !input.IsWorktree {
		t.Fatalf("IsWorktree = false, want true")
	}
	if got := input.Summary; got != "runtime summary" {
		t.Fatalf("Summary = %q, want runtime summary", got)
	}
	if got := input.SessionFlags["persistent_subagent_default"]; !got {
		t.Fatalf("persistent_subagent_default flag = false, want true")
	}
	wantTools := []string{"orchestration_launch_agent", "shell"}
	if !reflect.DeepEqual(input.EnabledTools, wantTools) {
		t.Fatalf("EnabledTools = %#v, want %#v", input.EnabledTools, wantTools)
	}
}

// TestMergePrepareInputRuntimeKeepsExplicitOutputStyleAndFRCConfig verifies
// explicit prompt-style and FRC settings are not overwritten by runtime config.
func TestMergePrepareInputRuntimeKeepsExplicitOutputStyleAndFRCConfig(t *testing.T) {
	explicitStyle := &contract.OutputStyleConfig{Name: "brief"}
	explicitFRC := (&contract.FRCConfig{Enabled: true, KeepRecent: 3}).Normalize()

	input := mergePrepareInputRuntime(PrepareInput{
		OutputStyleConfig: explicitStyle,
		FRCConfig:         explicitFRC,
	}, map[string]any{
		"output_style_config": map[string]any{"name": "verbose"},
		"frc_config":          map[string]any{"enabled": false, "keep_recent": 9},
	})

	if input.OutputStyleConfig == nil || *input.OutputStyleConfig != *explicitStyle {
		t.Fatalf("OutputStyleConfig = %#v, want %#v", input.OutputStyleConfig, explicitStyle)
	}
	if input.FRCConfig != explicitFRC {
		t.Fatalf("FRCConfig was replaced")
	}
}
