package claudecli

import (
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestComposeTurnTextOmitsLegacyMemoryAttachment(t *testing.T) {
	attachment := contract.NewRelevantMemoryAttachment(
		"project/commit-style.md",
		"Memory (saved today): project/commit-style.md:",
		"Use concise imperative commit messages.",
		time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
		720,
		false,
	)
	ordinary := dto.AttachmentEnvelope{
		Kind:    "file",
		Path:    "project/README.md",
		Header:  "README",
		Content: "Use the project README as background.",
		MtimeMs: 1,
	}
	got := composeTurnText(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{
			UserContext: map[string]string{
				"currentDate": "Today's date is 2026-04-15.",
			},
			Attachments: []dto.AttachmentEnvelope{attachment, ordinary},
		},
	})
	// system-reminder is now injected once via baseInstructions at start;
	// legacy memory is dropped while ordinary attachments remain on the wire.
	want := contract.RenderAttachmentText(ordinary) + "\n\nhello"
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

func TestBuildSteerPayloadRejectsLegacyMemorySection(t *testing.T) {
	t.Parallel()

	_, err := buildSteerPayload(dto.SteerRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{ResolvedSections: []dto.ResolvedPromptSection{
			{Name: contract.DynamicSectionMemory, Content: "legacy memory"},
		}},
	}, nil)
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) {
		t.Fatalf("buildSteerPayload() error = %v, want legacy memory sentinel", err)
	}
}
