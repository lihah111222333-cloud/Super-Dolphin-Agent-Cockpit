package prompt

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestMergeTemplateSections_OverridesBuiltinByKey locks the "same key replaces
// built-in, novel key appends with tpl: prefix" semantics. This is the UX
// contract operators rely on: editing a section named "identity" in the
// prompt_template_sections table must OVERRIDE the bundled built-in identity
// block, not stack on top of it.
func TestMergeTemplateSections_OverridesBuiltinByKey(t *testing.T) {
	t.Parallel()
	resolved := []contract.ResolvedPromptSection{
		{Name: "identity", Region: contract.PromptRegionStatic, Content: "You are Claude Code."},
		{Name: "style", Region: contract.PromptRegionStatic, Content: "Built-in style."},
	}
	blocks := []contract.BaseInstructionBlock{
		{Key: "identity", Region: contract.PromptRegionStatic, Ordinal: 0, Body: "You are a super-Dolphin."},
		{Key: "novel_extra", Region: contract.PromptRegionStatic, Ordinal: 10, Body: "Extra block body."},
	}
	got := mergeTemplateSections(resolved, blocks, contract.BuildCtx{}, "")

	if len(got) != 3 {
		t.Fatalf("want 3 sections (identity replaced, style kept, novel appended), got %d", len(got))
	}

	ident := findSectionByName(got, "identity")
	if ident == nil {
		t.Fatalf("identity section missing after merge")
	}
	if ident.Content != "You are a super-Dolphin." {
		t.Fatalf("identity not overridden: %q", ident.Content)
	}

	style := findSectionByName(got, "style")
	if style == nil || style.Content != "Built-in style." {
		t.Fatalf("style should stay built-in (no override block), got %+v", style)
	}

	novel := findSectionByName(got, "tpl:novel_extra")
	if novel == nil || novel.Content != "Extra block body." {
		t.Fatalf("novel block should be appended as tpl:novel_extra, got %+v", novel)
	}

	// Ensure no stray tpl:identity exists (the bug this test guards against):
	// before the override fix, identity would be appended as tpl:identity,
	// leaving both the built-in AND the template version in the prompt.
	if findSectionByName(got, "tpl:identity") != nil {
		t.Fatalf("identity should not be both overridden AND appended")
	}
}

func findSectionByName(sections []contract.ResolvedPromptSection, name string) *contract.ResolvedPromptSection {
	for i := range sections {
		if sections[i].Name == name {
			return &sections[i]
		}
	}
	return nil
}
