package codexapp

import (
	"reflect"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildTurnStartParams(t *testing.T) {
	t.Parallel()

	req := dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "text", Content: "hello"},
		},
		Skills: []dto.SkillRef{
			{Name: "planner", Prompt: "use the planner"},
			{Name: " reviewer "},
		},
		ManualSkillSelection: true,
		OutputSchema:         []byte(`{"type":"object"}`),
		Overrides:            dto.TurnOverrides{Model: "gpt-5.4", Effort: "high"},
	}

	got := buildTurnStartParams("thread-1", req)
	want := turnStartParams{
		ThreadID:             "thread-1",
		Input:                []turnInputItem{{Type: "text", Text: "[skill:planner]\nuse the planner", Content: "[skill:planner]\nuse the planner"}, {Type: "text", Text: "hello", Content: "hello"}},
		SelectedSkills:       []string{"planner", "reviewer"},
		ManualSkillSelection: true,
		Model:                "gpt-5.4",
		Effort:               "high",
		OutputSchema:         []byte(`{"type":"object"}`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTurnStartParams() = %#v, want %#v", got, want)
	}
}

func TestBuildTurnSteerParams(t *testing.T) {
	t.Parallel()

	req := dto.SteerRequest{
		ExpectedTurnID: " turn-1 ",
		Inputs: []dto.InputItem{
			{Type: "text", Content: "hello"},
		},
		Skills: []dto.SkillRef{
			{Name: "planner", Prompt: "use the planner"},
			{Name: " reviewer "},
		},
		ManualSkillSelection: true,
	}

	got := buildTurnSteerParams("thread-1", req)
	want := map[string]any{
		"threadId":             "thread-1",
		"expectedTurnId":       "turn-1",
		"input":                []turnInputItem{{Type: "text", Text: "[skill:planner]\nuse the planner", Content: "[skill:planner]\nuse the planner"}, {Type: "text", Text: "hello", Content: "hello"}},
		"selectedSkills":       []string{"planner", "reviewer"},
		"manualSkillSelection": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTurnSteerParams() = %#v, want %#v", got, want)
	}
}
