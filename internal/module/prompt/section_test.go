package prompt

import (
	"context"
	"testing"
)

func TestStaticSectionsExposeRequestedSlots(t *testing.T) {
	sections := StaticSections()
	want := []string{
		SectionIdentity,
		SectionConstraints,
		SectionTools,
		SectionMemoryRules,
		SectionToolPreferences,
		SectionProjectContext,
		SectionUserPreferences,
	}
	if len(sections) != len(want) {
		t.Fatalf("len(StaticSections()) = %d, want %d", len(sections), len(want))
	}
	for i, name := range want {
		if sections[i].Name != name {
			t.Fatalf("StaticSections()[%d].Name = %q, want %q", i, sections[i].Name, name)
		}
		if sections[i].Region != PromptRegionStatic {
			t.Fatalf("StaticSections()[%d].Region = %v, want %v", i, sections[i].Region, PromptRegionStatic)
		}
	}
}

func TestStaticSectionsRenderNonEmptyContent(t *testing.T) {
	for _, section := range StaticSections() {
		content, err := section.Compute(context.Background(), SectionContext{})
		if err != nil {
			t.Fatalf("section %q Compute() error = %v", section.Name, err)
		}
		if content == nil || *content == "" {
			t.Fatalf("section %q returned empty content", section.Name)
		}
	}
}
