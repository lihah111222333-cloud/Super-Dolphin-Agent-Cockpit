package claudecli

import (
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestComposeTurnTextIncludesAttachmentTextAfterUserContext(t *testing.T) {
	attachment := contract.NewRelevantMemoryAttachment(
		"project/commit-style.md",
		"Memory (saved today): project/commit-style.md:",
		"Use concise imperative commit messages.",
		time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
		720,
		false,
	)
	got := composeTurnText(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{
			UserContext: map[string]string{
				"currentDate": "Today's date is 2026-04-15.",
			},
			Attachments: []dto.AttachmentEnvelope{attachment},
		},
	})
	// system-reminder is now injected once via baseInstructions at start,
	// so composeTurnText only includes attachments + user text.
	want := contract.RenderAttachmentText(attachment) + "\n\nhello"
	if got != want {
		t.Fatalf("composeTurnText() = %q, want %q", got, want)
	}
}

func TestComposeTurnTextPrependsSystemContextBeforeUserContext(t *testing.T) {
	systemContext := dto.SystemContext{"gitStatus": "## main\n M changed.go"}
	got := composeTurnText(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{
			SystemContext: systemContext,
			UserContext: map[string]string{
				"currentDate": "Today's date is 2026-04-15.",
			},
		},
	})
	// system-reminder and SystemContext are now injected once at start,
	// so composeTurnText only returns the user text.
	want := "hello"
	if got != want {
		t.Fatalf("composeTurnText() = %q, want %q", got, want)
	}
}

func TestComposeTurnTextIncludesSkillPromptCarrierAndNativeSkillList(t *testing.T) {
	t.Parallel()

	got := composeTurnText(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		Skills: []dto.SkillRef{
			{Name: "native-tool", Mode: dto.SkillModeNone, Source: dto.SkillSourceNative},
			{Name: "planner", Mode: dto.SkillModeSummary, Summary: "do planning"},
		},
		SkillPrompt: "[skill:planner]\n摘要: do planning\n使用方式: Call skill_expand_body(\"planner\") for full body",
	})
	if !strings.Contains(got, "hello") {
		t.Fatalf("composeTurnText() = %q, want user text", got)
	}
	if !strings.Contains(got, "skills:\n- native-tool\n- planner") {
		t.Fatalf("composeTurnText() = %q, want native name list fallback", got)
	}
	if !strings.Contains(got, "[skill:planner]\n摘要: do planning") {
		t.Fatalf("composeTurnText() = %q, want skill prompt carrier", got)
	}
}
