package thread

import (
	"context"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type capturingLaunchPromptAssembly struct {
	input contract.StartInput
	start contract.StartAssembly
}

func (c *capturingLaunchPromptAssembly) AssembleStart(_ context.Context, in contract.StartInput) (contract.StartAssembly, error) {
	c.input = in
	return c.start, nil
}

func (*capturingLaunchPromptAssembly) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (*capturingLaunchPromptAssembly) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

func TestP20LaunchSkillIntegrationStartMirrorsSelectionIntoAssemblySnapshotAndProviderRequest(t *testing.T) {
	t.Parallel()

	manifest := "skills:\n- planner\n- reviewer"
	assembly := &capturingLaunchPromptAssembly{start: contract.StartAssembly{
		DisplayName:           "Launch Skill Thread",
		BaseInstructions:      "assembled base",
		DeveloperInstructions: "assembled dev",
		ResolvedSections: []contract.ResolvedPromptSection{{
			Name:    contract.DynamicSectionSkillCatalog,
			Content: manifest,
		}},
	}}
	var got dto.StartSessionRequest
	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		got = req
		session := &stubSession{threadID: "provider-thread-launch"}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewServiceWithPromptAssembly(silentLogger(), threads, nil, sessions, starter, nil, orch, nil, assembly, nil, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-launch",
		Provider:          "codex",
		Name:              "Launch Skill Thread",
		LaunchSkillNames:  []string{" planner ", "reviewer", ""},
		ForceLaunchSkills: true,
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	wantInputSkills := []string{" planner ", "reviewer", ""}
	wantSkills := []string{"planner", "reviewer"}
	if !reflect.DeepEqual(assembly.input.LaunchSkillNames, wantInputSkills) {
		t.Fatalf("AssembleStart input launch skills = %#v, want %#v", assembly.input.LaunchSkillNames, wantInputSkills)
	}
	if !assembly.input.ForceLaunchSkills {
		t.Fatalf("AssembleStart input ForceLaunchSkills = false, want true")
	}
	if !reflect.DeepEqual(got.LaunchSkillNames, wantInputSkills) || !got.ForceLaunchSkills {
		t.Fatalf("StartSession launch fields = %#v / force=%v", got.LaunchSkillNames, got.ForceLaunchSkills)
	}
	if !reflect.DeepEqual(got.StartAssembly.LaunchSkillNames, wantSkills) || !got.StartAssembly.ForceLaunchSkills {
		t.Fatalf("StartAssembly launch fields = %#v / force=%v", got.StartAssembly.LaunchSkillNames, got.StartAssembly.ForceLaunchSkills)
	}
	if !reflect.DeepEqual(got.StartAssembly.Snapshot.LaunchSkillNames, wantSkills) || !got.StartAssembly.Snapshot.ForceLaunchSkills {
		t.Fatalf("StartAssembly snapshot launch fields = %#v / force=%v", got.StartAssembly.Snapshot.LaunchSkillNames, got.StartAssembly.Snapshot.ForceLaunchSkills)
	}
	if got.StartAssembly.Snapshot.Hash == "" {
		t.Fatal("StartAssembly snapshot hash is empty")
	}
	if len(got.StartAssembly.ResolvedSections) != 1 || got.StartAssembly.ResolvedSections[0].Name != contract.DynamicSectionSkillCatalog || got.StartAssembly.ResolvedSections[0].Content != manifest {
		t.Fatalf("ResolvedSections = %#v, want skill_catalog manifest", got.StartAssembly.ResolvedSections)
	}
	if got.Instructions != "assembled base" {
		t.Fatalf("Instructions = %q, want assembled base", got.Instructions)
	}
	if got.Config["developerInstructions"] != "assembled dev" {
		t.Fatalf("developerInstructions = %#v, want assembled dev", got.Config["developerInstructions"])
	}
	if threads.promptSnapshot == nil {
		t.Fatal("stored prompt snapshot = nil")
	}
	if !reflect.DeepEqual(threads.promptSnapshot.LaunchSkillNames, wantSkills) || !threads.promptSnapshot.ForceLaunchSkills {
		t.Fatalf("stored prompt snapshot launch fields = %#v / force=%v", threads.promptSnapshot.LaunchSkillNames, threads.promptSnapshot.ForceLaunchSkills)
	}
}
