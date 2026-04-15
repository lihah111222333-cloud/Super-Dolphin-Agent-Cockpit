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
		Input:                []turnInputItem{{Type: "text", Text: "[skill:planner]\nuse the planner", Content: "[skill:planner]\nuse the planner"}, {Type: "text", Text: "<system-reminder>\n\n# currentDate\nToday's date is 2026-04-15.\n\n</system-reminder>", Content: "<system-reminder>\n\n# currentDate\nToday's date is 2026-04-15.\n\n</system-reminder>"}, {Type: "text", Text: "hello", Content: "hello"}},
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
	if len(got.Input) != 3 {
		t.Fatalf("len(buildTurnStartParams().Input) = %d, want 3", len(got.Input))
	}
	if got.Input[0].Text != "<system-reminder>\n\n# currentDate\nToday's date is 2026-04-15.\n\n</system-reminder>" {
		t.Fatalf("user context input = %q, want rendered structured user context", got.Input[0].Text)
	}
	if got.Input[1].Text != contract.RenderAttachmentText(attachment) {
		t.Fatalf("attachment input = %q, want rendered attachment text", got.Input[1].Text)
	}
	if got.Input[2].Text != "hello" {
		t.Fatalf("final input = %q, want original user text", got.Input[2].Text)
	}
}

func TestBuildTurnStartParamsIncludesSystemContext(t *testing.T) {
	t.Parallel()

	systemContext := dto.SystemContext{"gitStatus": "## main\n M changed.go"}
	got := buildTurnStartParams("thread-1", dto.TurnRequest{
		Inputs:       []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{SystemContext: systemContext},
	})
	if len(got.Input) != 2 {
		t.Fatalf("len(buildTurnStartParams().Input) = %d, want 2", len(got.Input))
	}
	if got.Input[0].Text != contract.FormatSystemContextBlock(systemContext) {
		t.Fatalf("system context input = %q, want formatted system context", got.Input[0].Text)
	}
	if got.Input[1].Text != "hello" {
		t.Fatalf("final input = %q, want original user text", got.Input[1].Text)
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
		"input":                []turnInputItem{{Type: "text", Text: "[skill:planner]\nuse the planner", Content: "[skill:planner]\nuse the planner"}, {Type: "text", Text: "<system-reminder>\n\n# currentDate\nToday's date is 2026-04-15.\n\n</system-reminder>", Content: "<system-reminder>\n\n# currentDate\nToday's date is 2026-04-15.\n\n</system-reminder>"}, {Type: "text", Text: "hello", Content: "hello"}},
		"selectedSkills":       []string{"planner", "reviewer"},
		"manualSkillSelection": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTurnSteerParams() = %#v, want %#v", got, want)
	}
}
