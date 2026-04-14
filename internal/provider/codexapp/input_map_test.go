package codexapp

import (
	"reflect"
	"testing"
	"time"

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
		TurnAssembly:         dto.TurnAssembly{UserContextText: "remember the workspace state"},
		ManualSkillSelection: true,
		OutputSchema:         []byte(`{"type":"object"}`),
		Overrides:            dto.TurnOverrides{Model: "gpt-5.4", Effort: "high"},
	}

	got := buildTurnStartParams("thread-1", req)
	want := turnStartParams{
		ThreadID:             "thread-1",
		Input:                []turnInputItem{{Type: "text", Text: "[skill:planner]\nuse the planner", Content: "[skill:planner]\nuse the planner"}, {Type: "text", Text: "remember the workspace state", Content: "remember the workspace state"}, {Type: "text", Text: "hello", Content: "hello"}},
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

func TestBuildTurnStartParamsIncludesAttachments(t *testing.T) {
	t.Parallel()

	attachment := dto.NewRelevantMemoryAttachment(
		"project/commit-style.md",
		"Memory (saved today): project/commit-style.md:",
		"Use concise imperative commit messages.",
		testAttachmentTime(),
		720,
		false,
	).Envelope()
	req := dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{
			UserContextText: "remember the workspace state",
			Attachments:     []dto.AttachmentEnvelope{attachment},
		},
	}

	got := buildTurnStartParams("thread-1", req)
	if len(got.Input) != 3 {
		t.Fatalf("len(buildTurnStartParams().Input) = %d, want 3", len(got.Input))
	}
	if got.Input[1].Text != attachment.RenderText() {
		t.Fatalf("attachment input = %q, want rendered attachment text", got.Input[1].Text)
	}
	if got.Input[2].Text != "hello" {
		t.Fatalf("final input = %q, want original user text", got.Input[2].Text)
	}
}

func testAttachmentTime() time.Time {
	return time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
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
		TurnAssembly:         dto.TurnAssembly{UserContextText: "remember the workspace state"},
		ManualSkillSelection: true,
	}

	got := buildTurnSteerParams("thread-1", req)
	want := map[string]any{
		"threadId":             "thread-1",
		"expectedTurnId":       "turn-1",
		"input":                []turnInputItem{{Type: "text", Text: "[skill:planner]\nuse the planner", Content: "[skill:planner]\nuse the planner"}, {Type: "text", Text: "remember the workspace state", Content: "remember the workspace state"}, {Type: "text", Text: "hello", Content: "hello"}},
		"selectedSkills":       []string{"planner", "reviewer"},
		"manualSkillSelection": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTurnSteerParams() = %#v, want %#v", got, want)
	}
}
