package prompt

import (
	"context"
	"reflect"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// TestAssembleStartSanitizesSectionProviderStartView 验证 section provider 不能
// 通过 SectionContext.Start 绕过 BuildCtx 的 Prompt carrier 清理。
func TestAssembleStartSanitizesSectionProviderStartView(t *testing.T) {
	svc := NewService(&Config{}, nil)
	var observedPrompt string
	registerDynamicProviderForTest(t, svc, DynamicTextProvider{
		Name: DynamicSectionProjectDefaultRules,
		ResolveFunc: func(_ context.Context, input SectionContext) (*string, error) {
			if input.Start != nil && len(input.Start.LaunchSkillRefs) > 0 {
				observedPrompt = input.Start.LaunchSkillRefs[0].Prompt
			}
			text := "provider section"
			return &text, nil
		},
	})
	_, err := svc.AssembleStart(context.Background(), StartInput{
		Provider:         "codex",
		BaseInstructions: "base",
		LaunchSkillRefs: []dto.SkillRef{{
			Name:   "review",
			Prompt: "legacy body must not reach section providers",
		}},
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if observedPrompt != "" {
		t.Fatalf("section provider observed legacy Prompt %q", observedPrompt)
	}
}

// TestBuildStartCtxStripsLegacySkillPrompt 验证 prompt 边界只保留 provider-native
// skill 元数据，不把旧全文 carrier 带入 BuildCtx。
func TestBuildStartCtxStripsLegacySkillPrompt(t *testing.T) {
	input := StartInput{
		CWD: "/repo",
		LaunchSkillRefs: []dto.SkillRef{{
			Key:     "project:review",
			Name:    "review",
			Scope:   "project",
			Path:    "/repo/.agents/skills/review",
			Version: "v1",
			Prompt:  "legacy body must not cross prompt boundary",
			Summary: "review summary",
			Source:  dto.SkillSourceManual,
		}},
	}

	got := buildStartCtx(input)
	if len(got.LaunchSkillRefs) != 1 {
		t.Fatalf("BuildCtx launch refs len = %d, want 1", len(got.LaunchSkillRefs))
	}
	ref := got.LaunchSkillRefs[0]
	want := input.LaunchSkillRefs[0]
	want.Prompt = ""
	if !reflect.DeepEqual(ref, want) {
		t.Fatalf("BuildCtx metadata = %#v, want preserved metadata", ref)
	}
	if input.LaunchSkillRefs[0].Prompt == "" {
		t.Fatal("BuildStartCtx mutated source skill prompt")
	}
	ref.Name = "mutated"
	if input.LaunchSkillRefs[0].Name == ref.Name {
		t.Fatal("BuildCtx launch refs alias source slice")
	}
}
