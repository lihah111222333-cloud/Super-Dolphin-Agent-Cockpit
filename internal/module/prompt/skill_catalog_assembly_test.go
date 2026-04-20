package prompt

import (
	"context"
	"strings"
	"testing"

	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

// TestAssembleStart_LaunchSkillsReachBaseInstructions is the missing end-to-end
// assertion flagged in docs/plans/迁移/p20/p20.16-integration-matrix.md B2-3:
// no existing test sends LaunchSkillNames / ForceLaunchSkills into the
// assembler and inspects the resulting BaseInstructions. Without it, a
// regression in the launch→BuildCtx→SkillCatalogProvider plumbing can ship
// silently because every layer on its own still compiles and looks correct.
func TestAssembleStart_LaunchSkillsReachBaseInstructions(t *testing.T) {
	t.Parallel()

	svc := NewService(&Config{}, nil)
	impl, ok := svc.(*service)
	if !ok {
		t.Fatalf("NewService returned unexpected type %T", svc)
	}
	provider := NewSkillCatalogProviderWithOptions(
		fakeSkillLister{infos: []skillpkg.SkillInfo{
			{Name: "alpha", Description: "alpha desc", Summary: "alpha summary", Trust: skillpkg.TrustUser},
			{Name: "bravo", Description: "bravo desc", Summary: "bravo summary", Trust: skillpkg.TrustUser},
			{Name: "charlie", Description: "charlie desc", Summary: "charlie summary", Trust: skillpkg.TrustUser},
		}},
		nil,
		12000,
		SkillCatalogOptions{EmitMetaInstructions: false},
	)
	if err := impl.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Prompt:            "display",
		BaseInstructions:  "legacy base",
		Provider:          "codex",
		CWD:               "/repo",
		LaunchSkillNames:  []string{"bravo"},
		ForceLaunchSkills: true,
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}

	if !strings.Contains(assembly.BaseInstructions, "**bravo**") {
		t.Fatalf("BaseInstructions missing launched skill %q:\n%s", "bravo", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "**alpha**") {
		t.Fatalf("ForceLaunchSkills=true leaked unselected skill %q:\n%s", "alpha", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "**charlie**") {
		t.Fatalf("ForceLaunchSkills=true leaked unselected skill %q:\n%s", "charlie", assembly.BaseInstructions)
	}
}

// TestAssembleStart_LaunchSkillsPinWithoutForceKeepsAll verifies that the
// pin-only branch (ForceLaunchSkills=false) does not hide the unselected
// skills from the rendered manifest — this is the end-to-end contract users
// rely on when they merely "prefer" a subset without restricting the catalog.
// Guards against a future change that would flip pin into force semantics.
// Intra-group ordering inside groupSkillsForManifest is alphabetical by
// design, so we only assert presence, not position; the pure-function pin
// order is covered by TestApplyLaunchSkillSelection_Pin_NoForce_PinnedAtTopRestKept.
func TestAssembleStart_LaunchSkillsPinWithoutForceKeepsAll(t *testing.T) {
	t.Parallel()

	svc := NewService(&Config{}, nil)
	impl := svc.(*service)
	provider := NewSkillCatalogProviderWithOptions(
		fakeSkillLister{infos: []skillpkg.SkillInfo{
			{Name: "alpha", Description: "a", Summary: "a", Trust: skillpkg.TrustUser},
			{Name: "bravo", Description: "b", Summary: "b", Trust: skillpkg.TrustUser},
		}},
		nil,
		12000,
		SkillCatalogOptions{EmitMetaInstructions: false},
	)
	if err := impl.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		BaseInstructions:  "legacy base",
		Provider:          "codex",
		CWD:               "/repo",
		LaunchSkillNames:  []string{"bravo"},
		ForceLaunchSkills: false,
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !strings.Contains(assembly.BaseInstructions, "**bravo**") {
		t.Fatalf("BaseInstructions missing pinned skill %q:\n%s", "bravo", assembly.BaseInstructions)
	}
	if !strings.Contains(assembly.BaseInstructions, "**alpha**") {
		t.Fatalf("pin mode must not hide unselected skill %q:\n%s", "alpha", assembly.BaseInstructions)
	}
}
