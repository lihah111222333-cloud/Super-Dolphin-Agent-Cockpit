package turn

import (
	"context"
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
)

func TestPrepareTurnRecordsSkillsSelected(t *testing.T) {
	t.Parallel()

	mem := observation.NewMemory()
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, nil, mem, nil, nil)
	session := &stubSession{threadID: "thread-1"}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt: "Please use @debug and [skill:deploy-tool] on this issue.",
		Skills: []dto.SkillRef{{Name: " explicit "}},
		CandidateSkills: []dto.SkillRef{
			{Name: "debug"},
			{Name: "deploy-tool"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	got := mem.SkillsSelected(req.LocalID)
	want := []string{"explicit", "debug", "deploy-tool"}
	if !slices.Equal(got, want) {
		t.Fatalf("SkillsSelected(%q) = %#v, want %#v", req.LocalID, got, want)
	}
}

func TestProviderTurnCreationMapsToLocalTurnID(t *testing.T) {
	t.Parallel()

	mem := observation.NewMemory()
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, nil, mem, nil, nil)
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			return newStubTurnHandle(req.LocalID, "provider-turn-1"), nil
		},
	}

	t.Cleanup(func() {
		if sd, ok := svc.(interface{ Shutdown() }); ok {
			sd.Shutdown()
		}
	})

	req := dto.TurnRequest{LocalID: "local-turn-1", ThreadID: "thread-1"}
	if _, err := svc.StartTurn(context.Background(), session, req); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if got, ok := mem.ResolveProviderTurn("local-turn-1"); !ok || got != "provider-turn-1" {
		t.Fatalf("ResolveProviderTurn = (%q,%v), want provider-turn-1,true", got, ok)
	}
	if got, ok := mem.ResolveLocalTurn("provider-turn-1"); !ok || got != "local-turn-1" {
		t.Fatalf("ResolveLocalTurn = (%q,%v), want local-turn-1,true", got, ok)
	}
}
