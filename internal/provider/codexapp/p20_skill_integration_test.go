package codexapp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestP20SkillIntegrationLaunchManifestAndTurnCarry(t *testing.T) {
	t.Parallel()

	refs := []dto.SkillRef{
		{Name: "planner", Mode: dto.SkillModeFull, Prompt: "full planning body", Source: dto.SkillSourceManual},
		{Name: "reviewer", Mode: dto.SkillModeSummary, Summary: "review code before merge", Source: dto.SkillSourceTrigger},
	}
	port := NewSkillInjectionPort()
	skillPrompt, ok := port.BuildTurnSection(refs)
	if !ok {
		t.Fatal("BuildTurnSection() ok = false, want true")
	}

	start := buildThreadStartParams(dto.StartSessionRequest{
		CWD:   " /repo ",
		Model: " gpt-5.4 ",
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "assembled base",
			Snapshot: dto.PromptAssemblySnapshot{
				SectionSnapshot: map[string]string{contract.DynamicSectionSkillCatalog: "skills:\n- planner\n- reviewer"},
			},
		},
	})
	if start.BaseInstructions != "assembled base\n\nskills:\n- planner\n- reviewer" {
		t.Fatalf("BaseInstructions = %q, want launch manifest appended", start.BaseInstructions)
	}
	if start.Cwd != "/repo" || start.Model != "gpt-5.4" {
		t.Fatalf("start params = %#v, want trimmed cwd/model", start)
	}

	turn := buildTurnStartParams("thread-1", dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		Skills: refs,
		SkillPrompt: skillPrompt,
		ManualSkillSelection: true,
	})
	if len(turn.SelectedSkills) != 2 || turn.SelectedSkills[0] != "planner" || turn.SelectedSkills[1] != "reviewer" {
		t.Fatalf("SelectedSkills = %#v, want [planner reviewer]", turn.SelectedSkills)
	}
	if len(turn.Input) != 2 || turn.Input[0].Text != skillPrompt || turn.Input[1].Text != "hello" {
		t.Fatalf("turn inputs = %#v, want skill prompt carrier + user text", turn.Input)
	}
}

func TestP20SkillIntegrationRolloutTrimDropsInjectedBlocksButKeepsSkillPrelude(t *testing.T) {
	t.Parallel()

	skillPrompt, ok := NewSkillInjectionPort().BuildTurnSection([]dto.SkillRef{
		{Name: "planner", Mode: dto.SkillModeFull, Prompt: "full planning body", Source: dto.SkillSourceManual},
		{Name: "reviewer", Mode: dto.SkillModeSummary, Summary: "review code before merge", Source: dto.SkillSourceTrigger},
	})
	if !ok {
		t.Fatal("BuildTurnSection() ok = false, want true")
	}
	raw, err := json.Marshal(map[string]any{
		"timestamp": "2026-04-20T00:00:00Z",
		"type":      "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": "<environment_context>\nignored\n</environment_context>\n" + skillPrompt + "\n\nhello world",
			}},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	msg, ok := parseRolloutLine(raw)
	if !ok {
		t.Fatal("parseRolloutLine() ok = false, want true")
	}
	if strings.Contains(msg.Content, "[skill:") || strings.Contains(msg.Content, "full planning body") {
		t.Fatalf("trimmed rollout still leaked injected block: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "skills:\n- planner\n- reviewer") {
		t.Fatalf("trimmed rollout = %q, want skill name prelude preserved", msg.Content)
	}
	if strings.Contains(msg.Content, "hello world") {
		t.Fatalf("trimmed rollout = %q, want only injected skill prelude to remain", msg.Content)
	}
}
