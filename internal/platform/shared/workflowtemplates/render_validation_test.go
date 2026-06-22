package workflowtemplates

import (
	"strings"
	"testing"
)

func TestTemplateValidationRejectsUnsupportedRuntimeCapabilities(t *testing.T) {
	t.Parallel()

	tpl := testTemplateForValidation(t)
	tpl.DAGTemplate.Nodes[0].NodeType = "hybrid"
	tpl.Compatibility.NodeTypes = []string{"agent", "hybrid"}

	err := validateTemplate(tpl)
	if err == nil || !strings.Contains(err.Error(), "hybrid") || !strings.Contains(err.Error(), "runtime support") {
		t.Fatalf("validateTemplate() error = %v, want hybrid runtime support failure", err)
	}
}

func TestPublishedTemplateValidationRequiresAgentExecCWD(t *testing.T) {
	t.Parallel()

	tpl := testTemplateForValidation(t)
	tpl = testTemplateWithExecCWD(tpl)
	delete(tpl.DAGTemplate.Nodes[0].Config, "exec")

	err := validatePublishedTemplate(tpl)
	if err == nil || !strings.Contains(err.Error(), "config.exec.cwd") {
		t.Fatalf("validatePublishedTemplate() error = %v, want config.exec.cwd failure", err)
	}
}

func TestTemplateValidationChecksFinalOutputMapping(t *testing.T) {
	t.Parallel()

	tpl := testTemplateForValidation(t)
	finalIndex := testNodeIndex(t, tpl, tpl.DAGTemplate.FinalNodeKey)
	outputs := tpl.DAGTemplate.Nodes[finalIndex].Config["outputs"].(map[string]any)
	shared := outputs["to_sharedfile"].(map[string]any)
	shared["path"] = "{{output_path}}wrong.md"

	err := validateTemplate(tpl)
	if err == nil || !strings.Contains(err.Error(), "final_output") {
		t.Fatalf("validateTemplate() error = %v, want final_output mapping failure", err)
	}
}

func TestRenderDAGDraftAppliesRuntimeContextToExecCWD(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}

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
		RuntimeContext: map[string]any{
			"cwd": "D:/project/demo",
		},
	})
	if err != nil {
		t.Fatalf("RenderDAGDraft() error = %v", err)
	}
	exec, ok := draft.Nodes[0].Config["exec"].(map[string]any)
	if !ok {
		t.Fatalf("first node exec config missing: %+v", draft.Nodes[0].Config)
	}
	if got := exec["cwd"]; got != "D:/project/demo" {
		t.Fatalf("exec.cwd = %v, want runtime context cwd", got)
	}
}

func TestRegistrySaveTemplateVersionsAndRollback(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	tpl := testTemplateForValidation(t)
	tpl = testTemplateWithExecCWD(tpl)
	tpl.Version = 2
	tpl.Description.Zh = "用户保存的会议纪要模板"
	tpl.Trust.Source = "user_saved"

	if err := reg.SaveTemplate(tpl); err != nil {
		t.Fatalf("SaveTemplate() error = %v", err)
	}
	summary := testSummaryByID(t, reg.ListTemplates(), tpl.ID)
	if summary.Version != 2 || len(summary.AvailableVersions) != 2 {
		t.Fatalf("summary version metadata = %+v, want active v2 and two versions", summary)
	}

	if err := reg.RollbackTemplate(tpl.ID, 1); err != nil {
		t.Fatalf("RollbackTemplate() error = %v", err)
	}
	got, ok := reg.GetTemplate(tpl.ID)
	if !ok {
		t.Fatalf("GetTemplate(%q) missing after rollback", tpl.ID)
	}
	if got.Version != 1 {
		t.Fatalf("active version = %d, want 1", got.Version)
	}
}

func testTemplateWithExecCWD(tpl Template) Template {
	for index := range tpl.DAGTemplate.Nodes {
		node := &tpl.DAGTemplate.Nodes[index]
		if strings.TrimSpace(strings.ToLower(node.NodeType)) != "agent" {
			continue
		}
		if node.Config == nil {
			node.Config = make(map[string]any)
		}
		node.Config["exec"] = map[string]any{"cwd": "D:/project/demo"}
	}
	return tpl
}

func testTemplateForValidation(t *testing.T) Template {
	t.Helper()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	tpl, ok := reg.GetTemplate("government-enterprise/meeting-minutes")
	if !ok {
		t.Fatalf("GetTemplate(meeting-minutes) missing")
	}
	return tpl
}

func testNodeIndex(t *testing.T, tpl Template, key string) int {
	t.Helper()

	for index, node := range tpl.DAGTemplate.Nodes {
		if node.NodeKey == key {
			return index
		}
	}
	t.Fatalf("node %q missing", key)
	return -1
}

func testSummaryByID(t *testing.T, summaries []TemplateSummary, id string) TemplateSummary {
	t.Helper()

	for _, summary := range summaries {
		if summary.ID == id {
			return summary
		}
	}
	t.Fatalf("summary %q missing", id)
	return TemplateSummary{}
}
