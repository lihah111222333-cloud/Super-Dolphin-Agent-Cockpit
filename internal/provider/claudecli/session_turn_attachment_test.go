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
	want := "<system-reminder>\n\n# currentDate\nToday's date is 2026-04-15.\n\n</system-reminder>\n\n" + contract.RenderAttachmentText(attachment) + "\n\nhello"
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
	want := contract.FormatSystemContextBlock(systemContext) + "\n\n<system-reminder>\n\n# currentDate\nToday's date is 2026-04-15.\n\n</system-reminder>\n\nhello"
	if got != want {
		t.Fatalf("composeTurnText() = %q, want %q", got, want)
	}
}
