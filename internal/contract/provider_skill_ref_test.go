package contract

import (
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestCloneProviderSkillRefsMetadataOnlyStripsPromptWithoutAliasing(t *testing.T) {
	input := []dto.SkillRef{{
		Name:    "review",
		Key:     "project:review",
		Scope:   "project",
		Path:    "/repo/.agents/skills/review",
		Version: "v1",
		Prompt:  "legacy body",
		Summary: "summary",
		Source:  dto.SkillSourceManual,
	}}
	got := CloneProviderSkillRefsMetadataOnly(input)
	if len(got) != 1 || got[0].Prompt != "" {
		t.Fatalf("cloned refs = %#v, want metadata-only ref", got)
	}
	if got[0].Name != input[0].Name || got[0].Key != input[0].Key || got[0].Source != input[0].Source {
		t.Fatalf("cloned metadata = %#v, want identity metadata preserved", got[0])
	}
	got[0].Name = "mutated"
	if input[0].Name == got[0].Name || input[0].Prompt == "" {
		t.Fatalf("clone aliases or mutates source: input=%#v got=%#v", input, got)
	}
}
