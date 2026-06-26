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
			// skill 元数据已在会话级 baseInstructions 注入，单个 turn 不再内联正文。
			// SelectedSkills 仍需透传，供 provider 侧保留选择痕迹。
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
	// 单个 turn 不再追加 skill 正文块，Input 只保留用户文本。
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
	// system-reminder 只在会话启动时注入，单个 turn 仅保留附件和用户文本。
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
	// SystemContext 只在会话启动时注入，单个 turn 只发送用户文本。
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
			// skill 元数据由 baseInstructions 承载，steer 请求同样不注入正文。
			{Name: "planner", Prompt: "use the planner"},
			{Name: " reviewer "},
		},
		TurnAssembly: dto.TurnAssembly{UserContext: map[string]string{
			"currentDate": "Today's date is 2026-04-15.",
		}},
		ManualSkillSelection: true,
	}

	got := buildTurnSteerParams("thread-1", req)
	// steer 输入不追加 per-turn skill 块，只包含用户文本和选择元数据。
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
