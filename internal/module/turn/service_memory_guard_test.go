package turn

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestStartTurnRejectsLegacyMemorySectionBeforeTrackerOrProvider(t *testing.T) {
	t.Parallel()

	providerCalls := 0
	session := &stubSession{
		threadID: "thread-legacy-guard",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			providerCalls++
			return nil, errors.New("unexpected provider call")
		},
	}
	svc := NewService(silentLogger(), NewToolResultRuntime()).(*service)
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		ThreadID: "thread-legacy-guard",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{ResolvedSections: []dto.ResolvedPromptSection{
			{Name: contract.DynamicSectionMemoryContext, Content: "legacy memory"},
		}},
	})
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) {
		t.Fatalf("StartTurn() error = %v, want legacy memory sentinel", err)
	}
	if providerCalls != 0 {
		t.Fatalf("provider StartTurn calls = %d, want 0", providerCalls)
	}
	if _, ok := svc.tracker.Get("turn-legacy-guard"); ok {
		t.Fatal("legacy turn was registered in tracker before rejection")
	}
}

func TestStartTurnFiltersLegacyMemoryAttachmentButKeepsOrdinary(t *testing.T) {
	t.Parallel()

	var got dto.TurnRequest
	handle := newStubTurnHandle("turn-ordinary-attachment", "provider-ordinary-attachment")
	session := &stubSession{
		threadID: "thread-ordinary-attachment",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			got = req
			return handle, nil
		},
	}
	svc := NewService(silentLogger(), NewToolResultRuntime()).(*service)
	legacy := dto.AttachmentEnvelope{Kind: dto.AttachmentKindNestedMemory, Path: "nested.md"}
	ordinary := dto.AttachmentEnvelope{Kind: "file", Path: "README.md", Header: "README", Content: "background", MtimeMs: 1}
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		ThreadID: "thread-ordinary-attachment",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
		TurnAssembly: dto.TurnAssembly{Attachments: []dto.AttachmentEnvelope{
			legacy,
			ordinary,
		}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if len(got.TurnAssembly.Attachments) != 1 || got.TurnAssembly.Attachments[0].Kind != ordinary.Kind {
		t.Fatalf("provider turn attachments = %#v, want ordinary attachment only", got.TurnAssembly.Attachments)
	}
}
