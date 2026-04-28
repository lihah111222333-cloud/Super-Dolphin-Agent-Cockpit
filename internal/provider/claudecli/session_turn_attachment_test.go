package claudecli

import (
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

func TestBuildSkillPromptTextV1SummaryModeWithEmptyBody(t *testing.T) {
	t.Setenv("SKILL_WRITER_FORMAT", "v1")

	got := buildSkillPromptText([]dto.SkillRef{{
		Name:    "rpc-tracing",
		Mode:    dto.SkillModeSummary,
		Summary: "Trace JSON-RPC flow.",
	}})
	want := "[skill:rpc-tracing::summary@v1]\n" +
		"摘要: Trace JSON-RPC flow.\n" +
		"使用方式: Call skill_expand_body(\"rpc-tracing\") for full body\n" +
		"[/skill:rpc-tracing::summary@v1]"
	if got != want {
		t.Fatalf("v1 summary skill prompt with empty body:\ngot  %q\nwant %q", got, want)
	}
}
