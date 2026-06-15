package codexapp

import (
	"reflect"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildTurnStartParams(t *testing.T) {
	req := dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "text", Content: "hello"},
		},
		Skills: []dto.SkillRef{
			// Skill metadata is now injected into baseInstructions (P3 Task 5);
			// per-turn skill body inlining is removed. SelectedSkills still forwarded.
			{Name: "planner", Prompt: "use the planner"},
			{Name: " reviewer "},
		},
		TurnAssembly: dto.TurnAssembly{UserContext: map[string]string{
			"currentDate": "Today's date is 2026-04-15.",
		}},
		ManualSkillSelection: true,
		OutputSchema:         []byte(`{"type":"object"}`),
		Overrides:            dto.TurnOverrides{Model: "gpt-5.5", Effort: "high"},
	}

	got := buildTurnStartParams("thread-1", req)
	// No per-turn skill block: Input contains only the user text.
	want := turnStartParams{
		ThreadID:             "thread-1",
		Input:                []turnInputItem{{Type: "text", Text: "hello", Content: "hello"}},
		SelectedSkills:       []string{"planner", "reviewer"},
		ManualSkillSelection: true,
		Model:                "gpt-5.5",
		Effort:               "high",
		OutputSchema:         []byte(`{"type":"object"}`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTurnStartParams() = %#v, want %#v", got, want)
	}
}

func TestBuildTurnStartParamsNormalizesMinimalEffortToLow(t *testing.T) {
	t.Parallel()

	got := buildTurnStartParams("thread-1", dto.TurnRequest{
		Inputs:    []dto.InputItem{{Type: "text", Content: "hello"}},
		Overrides: dto.TurnOverrides{Effort: " minimal "},
	})
	if got.Effort != "low" {
		t.Fatalf("Effort = %q, want low", got.Effort)
	}
}

func TestBuildThreadStartParamsNormalizesSandboxModeForAppServer(t *testing.T) {
	params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		CWD: t.TempDir(),
		Config: map[string]any{
			"sandbox": map[string]any{
				"mode":           "workspace-write",
				"writable_roots": []string{"/repo"},
				"network_access": false,
			},
		},
	})
	if string(params.Sandbox) != `"workspace-write"` {
		t.Fatalf("Sandbox = %s, want workspace-write mode string", string(params.Sandbox))
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
	req := dto.SteerRequest{
		ExpectedTurnID: " turn-1 ",
		Inputs: []dto.InputItem{
			{Type: "text", Content: "hello"},
		},
		Skills: []dto.SkillRef{
			// Skill metadata is now in baseInstructions; no per-turn body injection.
			{Name: "planner", Prompt: "use the planner"},
			{Name: " reviewer "},
		},
		TurnAssembly: dto.TurnAssembly{UserContext: map[string]string{
			"currentDate": "Today's date is 2026-04-15.",
		}},
		ManualSkillSelection: true,
	}

	got := buildTurnSteerParams("thread-1", req)
	// No per-turn skill block: input contains only the user text.
	want := map[string]any{
		"threadId":             "thread-1",
		"expectedTurnId":       "turn-1",
		"input":                []turnInputItem{{Type: "text", Text: "hello", Content: "hello"}},
		"selectedSkills":       []string{"planner", "reviewer"},
		"manualSkillSelection": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTurnSteerParams() = %#v, want %#v", got, want)
	}
}

func TestBuildTurnInterruptParamsIncludesTurnID(t *testing.T) {
	got := buildTurnInterruptParams(" thread-1 ", " turn-1 ", " ui_stop ")
	want := map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"source":   "ui_stop",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTurnInterruptParams() = %#v, want %#v", got, want)
	}
}
