package workflowtemplates

import (
	"strings"
	"testing"
)

func TestDefaultRegistryLoadsGovernmentEnterpriseTemplates(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}

	got := reg.ListTemplates()
	if len(got) != 6 {
		t.Fatalf("ListTemplates() length = %d, want 6", len(got))
	}
	wantIDs := []string{
		"government-enterprise/promo-video",
		"government-enterprise/daily-weekly-report",
		"government-enterprise/project-briefing",
		"government-enterprise/meeting-minutes",
		"government-enterprise/data-analysis-brief",
		"government-enterprise/approval-material",
	}
	for index, wantID := range wantIDs {
		if got[index].ID != wantID {
			t.Fatalf("ListTemplates()[%d].ID = %q, want %q", index, got[index].ID, wantID)
		}
		if !got[index].RequiresReview {
			t.Fatalf("template %s must require review", wantID)
		}
		if got[index].FinalNodeKey == "" {
			t.Fatalf("template %s must expose final node", wantID)
		}
	}
}

func TestDefaultTemplatesKeepReviewBeforeFinalAndUIConfig(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}

	for _, summary := range reg.ListTemplates() {
		tpl, ok := reg.GetTemplate(summary.ID)
		if !ok {
			t.Fatalf("GetTemplate(%q) missing", summary.ID)
		}
		assertTemplateReviewFinalOrder(t, tpl)
	}
}

func assertTemplateReviewFinalOrder(t *testing.T, tpl Template) {
	t.Helper()

	reviewIndex, finalIndex := templateReviewFinalIndexes(t, tpl)
	if reviewIndex >= finalIndex {
		t.Fatalf("%s review/final order invalid: review=%d final=%d", tpl.ID, reviewIndex, finalIndex)
	}
	finalNode := tpl.DAGTemplate.Nodes[finalIndex]
	if !contains(finalNode.DependsOn, tpl.DAGTemplate.Nodes[reviewIndex].NodeKey) {
		t.Fatalf("%s final node must depend on review node", tpl.ID)
	}
}

func templateReviewFinalIndexes(t *testing.T, tpl Template) (int, int) {
	t.Helper()

	reviewIndex := -1
	finalIndex := -1
	for index, node := range tpl.DAGTemplate.Nodes {
		if _, ok := node.Config["ui"]; !ok {
			t.Fatalf("%s node %s missing config.ui", tpl.ID, node.NodeKey)
		}
		if node.NodeKey == "review" || strings.Contains(node.NodeKey, "review") {
			reviewIndex = index
		}
		if node.NodeKey == tpl.DAGTemplate.FinalNodeKey {
			finalIndex = index
		}
	}
	if reviewIndex < 0 || finalIndex < 0 {
		t.Fatalf("%s review/final node missing: review=%d final=%d", tpl.ID, reviewIndex, finalIndex)
	}
	return reviewIndex, finalIndex
}

func TestRenderDAGDraftFailsFastAndRendersPlaceholders(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}

	assertRenderDraftMissingFields(t, reg)
	draft := renderMeetingMinutesDraft(t, reg)
	assertRenderedMeetingMinutesDraft(t, draft)
}

func assertRenderDraftMissingFields(t *testing.T, reg *Registry) {
	t.Helper()

	_, err := reg.RenderDAGDraft(RenderRequest{TemplateID: "government-enterprise/meeting-minutes", Values: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "missing required fields") {
		t.Fatalf("RenderDAGDraft() error = %v, want missing required fields", err)
	}
}

func renderMeetingMinutesDraft(t *testing.T, reg *Registry) DAGDraft {
	t.Helper()

	draft, err := reg.RenderDAGDraft(RenderRequest{
		TemplateID: "government-enterprise/meeting-minutes",
		Version:    1,
		Values: map[string]any{
			"title":            "6月项目推进会",
			"source_materials": "meetings/raw.md",
			"output_format":    "docx",
			"reviewer":         "会议主持人",
			"output_path":      "reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/",
		},
	})
	if err != nil {
		t.Fatalf("RenderDAGDraft() error = %v", err)
	}
	return draft
}

func TestRegistryValidationRulesAreInstancePrivate(t *testing.T) {
	t.Parallel()

	first, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("first registry: %v", err)
	}
	second, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("second registry: %v", err)
	}

	first.rules.allowedOutputTypes["test-only"] = struct{}{}
	_, shared := second.rules.allowedOutputTypes["test-only"]
	if shared {
		t.Fatal("registry validation rules must not share mutable maps")
	}
}

func assertRenderedMeetingMinutesDraft(t *testing.T, draft DAGDraft) {
	t.Helper()

	if draft.FinalNodeKey != "final_minutes" || draft.ReviewNodeKey != "review" {
		t.Fatalf("RenderDAGDraft() final/review = %q/%q", draft.FinalNodeKey, draft.ReviewNodeKey)
	}
	if !strings.Contains(draft.Title, "6月项目推进会") {
		t.Fatalf("RenderDAGDraft() title = %q", draft.Title)
	}
	if draft.TemplateVersion != 1 || draft.Metadata["template_id"] != "government-enterprise/meeting-minutes" {
		t.Fatalf("RenderDAGDraft() metadata/version = %+v/%d", draft.Metadata, draft.TemplateVersion)
	}
	if draft.FinalOutput.Kind != "artifact" {
		t.Fatalf("RenderDAGDraft() final kind = %q, want artifact", draft.FinalOutput.Kind)
	}
	if draft.FinalOutput.PathTemplate != "reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/final.docx" {
		t.Fatalf("RenderDAGDraft() final path = %q", draft.FinalOutput.PathTemplate)
	}
}
