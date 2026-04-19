package codexapp

import (
	"reflect"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
		TurnAssembly: dto.TurnAssembly{UserContext: map[string]string{
			"currentDate": "Today's date is 2026-04-15.",
		}},
		ManualSkillSelection: true,
		OutputSchema:         []byte(`{"type":"object"}`),
		Overrides:            dto.TurnOverrides{Model: "gpt-5.4", Effort: "high"},
	}

	got := buildTurnStartParams("thread-1", req)
	want := turnStartParams{
		ThreadID:             "thread-1",
		Input:                []turnInputItem{{Type: "text", Text: "[skill:planner::full@v1]\nuse the planner\n[/skill:planner::full@v1]", Content: "[skill:planner::full@v1]\nuse the planner\n[/skill:planner::full@v1]"}, {Type: "text", Text: "hello", Content: "hello"}},
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

	attachment := contract.NewRelevantMemoryAttachment(
		"project/commit-style.md",
		"Memory (saved today): project/commit-style.md:",
		"Use concise imperative commit messages.",
		testAttachmentTime(),
		720,
		false,
	)
	req := dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{
			UserContext: map[string]string{
				"currentDate": "Today's date is 2026-04-15.",
			},
			Attachments: []dto.AttachmentEnvelope{attachment},
		},
	}

	got := buildTurnStartParams("thread-1", req)
	// system-reminder is now injected once at session start; per-turn only has attachment + user text.
	if len(got.Input) != 2 {
		t.Fatalf("len(buildTurnStartParams().Input) = %d, want 2", len(got.Input))
	}
	if got.Input[0].Text != contract.RenderAttachmentText(attachment) {
		t.Fatalf("attachment input = %q, want rendered attachment text", got.Input[0].Text)
	}
	if got.Input[1].Text != "hello" {
		t.Fatalf("final input = %q, want original user text", got.Input[1].Text)
	}
}

func TestBuildTurnStartParamsIncludesSystemContext(t *testing.T) {
	t.Parallel()

	systemContext := dto.SystemContext{"gitStatus": "## main\n M changed.go"}
	got := buildTurnStartParams("thread-1", dto.TurnRequest{
		Inputs:       []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{SystemContext: systemContext},
	})
	// SystemContext is now injected once at session start; per-turn only has user text.
	if len(got.Input) != 1 {
		t.Fatalf("len(buildTurnStartParams().Input) = %d, want 1", len(got.Input))
	}
	if got.Input[0].Text != "hello" {
		t.Fatalf("final input = %q, want original user text", got.Input[0].Text)
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
		TurnAssembly: dto.TurnAssembly{UserContext: map[string]string{
			"currentDate": "Today's date is 2026-04-15.",
		}},
		ManualSkillSelection: true,
	}

	got := buildTurnSteerParams("thread-1", req)
	want := map[string]any{
		"threadId":             "thread-1",
		"expectedTurnId":       "turn-1",
		"input":                []turnInputItem{{Type: "text", Text: "[skill:planner::full@v1]\nuse the planner\n[/skill:planner::full@v1]", Content: "[skill:planner::full@v1]\nuse the planner\n[/skill:planner::full@v1]"}, {Type: "text", Text: "hello", Content: "hello"}},
		"selectedSkills":       []string{"planner", "reviewer"},
		"manualSkillSelection": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTurnSteerParams() = %#v, want %#v", got, want)
	}
}
