package workflowtemplates

import (
	"fmt"
	"strings"
	"testing"
)

func TestTemplateValidationRejectsUnsupportedRuntimeCapabilities(t *testing.T) {
	t.Parallel()

	tpl := testTemplateForValidation(t)
	tpl.DAGTemplate.Nodes[0].NodeType = "hybrid"
	tpl.Compatibility.NodeTypes = []string{"agent", "hybrid"}

	err := validateTemplate(tpl, newValidationRules())
	if err == nil || !strings.Contains(err.Error(), "hybrid") || !strings.Contains(err.Error(), "runtime support") {
		t.Fatalf("validateTemplate() error = %v, want hybrid runtime support failure", err)
	}
}

func TestPublishedTemplateValidationRequiresAgentExecCWD(t *testing.T) {
	t.Parallel()

	tpl := testTemplateForValidation(t)
	tpl = testTemplateWithExecCWD(tpl)
	delete(tpl.DAGTemplate.Nodes[0].Config, "exec")

	err := validatePublishedTemplate(tpl, newValidationRules())
	if err == nil || !strings.Contains(err.Error(), "config.exec.cwd") {
		t.Fatalf("validatePublishedTemplate() error = %v, want config.exec.cwd failure", err)
	}
}

func TestTemplateValidationChecksFinalOutputMapping(t *testing.T) {
	t.Parallel()

	tpl := testTemplateForValidation(t)
	finalIndex := testNodeIndex(t, tpl, tpl.DAGTemplate.FinalNodeKey)
	outputs := tpl.DAGTemplate.Nodes[finalIndex].Config["outputs"].(map[string]any)
	artifact := outputs["to_artifact"].(map[string]any)
	artifact["path_template"] = "{{output_path}}wrong.docx"

	err := validateTemplate(tpl, newValidationRules())
	if err == nil || !strings.Contains(err.Error(), "final_output") {
		t.Fatalf("validateTemplate() error = %v, want final_output mapping failure", err)
	}
}

func TestTemplateValidationChecksDocumentArtifactContract(t *testing.T) {
	t.Parallel()

	tpl := testTemplateForValidation(t)
	finalIndex := testNodeIndex(t, tpl, tpl.DAGTemplate.FinalNodeKey)
	outputs := tpl.DAGTemplate.Nodes[finalIndex].Config["outputs"].(map[string]any)
	artifact := outputs["to_artifact"].(map[string]any)
	delete(artifact, "source_text_field")
	artifact["source_path_field"] = "output_path"

	err := validateTemplate(tpl, newValidationRules())
	if err == nil || !strings.Contains(err.Error(), "document template") {
		t.Fatalf("validateTemplate() error = %v, want document artifact contract failure", err)
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

func TestApprovalMaterialTemplateRoutesDocumentOutputsThroughSharedfiles(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}

	draft, err := reg.RenderDAGDraft(RenderRequest{
		TemplateID: "government-enterprise/approval-material",
		Version:    1,
		Values: map[string]any{
			"title":            "数据共享审批",
			"approval_basis":   "数据共享审批依据",
			"source_materials": "申请材料正文",
			"output_format":    "docx",
			"reviewer":         "审批经办人",
			"output_path":      "reports/workflows/government_enterprise_approval_material/{{run_id}}/",
		},
		RuntimeContext: map[string]any{
			"cwd": "D:/project/demo",
		},
	})
	if err != nil {
		t.Fatalf("RenderDAGDraft() error = %v", err)
	}

	sharedfileByNode := assertDocumentNodesUseSharedfilesAndFinalArtifact(t, draft.Nodes, draft.FinalNodeKey)
	assertDocumentDependenciesReadSharedfiles(t, draft.Nodes, sharedfileByNode)
}

func TestGovernmentEnterpriseTextTemplatesKeepLargeBodiesOutOfNodeResults(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}

	for _, summary := range reg.ListTemplates(ListFilter{Category: "government-enterprise"}) {
		if summary.ID == "government-enterprise/promo-video" {
			continue
		}
		tpl, ok := reg.GetTemplate(summary.ID)
		if !ok {
			t.Fatalf("GetTemplate(%q) missing", summary.ID)
		}
		sharedfileByNode := assertDocumentNodesUseSharedfilesAndFinalArtifact(t, tpl.DAGTemplate.Nodes, tpl.DAGTemplate.FinalNodeKey)
		assertDocumentDependenciesReadSharedfiles(t, tpl.DAGTemplate.Nodes, sharedfileByNode)
	}
}

func TestGovernmentEnterpriseTextTemplatesAdvertiseGeneratedDocumentArtifacts(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}

	for _, summary := range reg.ListTemplates(ListFilter{Category: "government-enterprise"}) {
		if summary.ID == "government-enterprise/promo-video" {
			continue
		}
		assertDocumentOutputTypes(t, summary.ID, "output_types", summary.OutputTypes)
		tpl, ok := reg.GetTemplate(summary.ID)
		if !ok {
			t.Fatalf("GetTemplate(%q) missing", summary.ID)
		}
		assertOutputFormatFieldAdvertisesDocumentTypes(t, tpl)
		if tpl.FinalOutput.Kind != "artifact" {
			t.Fatalf("%s final_output.kind = %q, want artifact", tpl.ID, tpl.FinalOutput.Kind)
		}
	}
}

func assertDocumentOutputTypes(t *testing.T, templateID string, source string, values []string) {
	t.Helper()

	got := make(map[string]bool, len(values))
	for _, value := range values {
		got[strings.ToLower(strings.TrimSpace(value))] = true
	}
	if !got["docx"] || !got["pdf"] || len(got) != 2 {
		t.Fatalf("%s %s = %v, want exactly docx/pdf", templateID, source, values)
	}
}

func assertOutputFormatFieldAdvertisesDocumentTypes(t *testing.T, tpl Template) {
	t.Helper()

	for _, field := range tpl.UISchema {
		if field.Key != "output_format" {
			continue
		}
		values := make([]string, 0, len(field.Options))
		for _, option := range field.Options {
			values = append(values, option.Value)
		}
		assertDocumentOutputTypes(t, tpl.ID, "ui_schema.output_format.options", values)
		return
	}
	t.Fatalf("%s missing output_format field", tpl.ID)
}

func assertDocumentNodesUseSharedfilesAndFinalArtifact(t *testing.T, nodes []NodeTemplate, finalNodeKey string) map[string]string {
	t.Helper()

	sharedfileByNode := make(map[string]string, len(nodes))
	for _, node := range nodes {
		outputs := testObjectMap(t, node.Config, "outputs")
		if node.NodeKey == finalNodeKey {
			assertDocumentFinalArtifact(t, node, outputs)
			continue
		}
		shared := testObjectMap(t, outputs, "to_sharedfile")
		path := strings.TrimSpace(fmt.Sprint(shared["path"]))
		if path == "" || path == "<nil>" {
			t.Fatalf("node %s outputs.to_sharedfile.path is empty", node.NodeKey)
		}
		sharedfileByNode[node.NodeKey] = path
		if outputs["to_node_result"] == true {
			t.Fatalf("node %s must not write document bodies to outputs.to_node_result with sharedfile output", node.NodeKey)
		}
	}
	return sharedfileByNode
}

func assertDocumentFinalArtifact(t *testing.T, node NodeTemplate, outputs map[string]any) {
	t.Helper()

	artifact := testObjectMap(t, outputs, "to_artifact")
	if artifact["source_tool"] != "document_renderer" || artifact["source_text_field"] != "document_text" {
		t.Fatalf("node %s document artifact selector = %+v", node.NodeKey, artifact)
	}
	path := strings.TrimSpace(fmt.Sprint(artifact["path_template"]))
	if !strings.Contains(path, "final.{{output_format}}") && !strings.Contains(path, "final.docx") && !strings.Contains(path, "final.pdf") {
		t.Fatalf("node %s artifact path_template = %q", node.NodeKey, path)
	}
	if outputs["to_node_result"] == true {
		t.Fatalf("node %s must not write document bodies to outputs.to_node_result with artifact output", node.NodeKey)
	}
}

func assertDocumentDependenciesReadSharedfiles(t *testing.T, nodes []NodeTemplate, sharedfileByNode map[string]string) {
	t.Helper()

	for _, node := range nodes {
		if len(node.DependsOn) == 0 {
			continue
		}
		inputs := testObjectMap(t, node.Config, "inputs")
		fromSharedfiles := testStringSetFromAnyList(t, inputs["from_sharedfiles"], node.NodeKey)
		assertDependencySharedfiles(t, node, sharedfileByNode, fromSharedfiles)
	}
}

func assertDependencySharedfiles(t *testing.T, node NodeTemplate, sharedfileByNode map[string]string, fromSharedfiles map[string]bool) {
	t.Helper()

	for _, dep := range node.DependsOn {
		path := sharedfileByNode[dep]
		if path == "" {
			t.Fatalf("node %s depends on %s without sharedfile output", node.NodeKey, dep)
		}
		if !fromSharedfiles[path] {
			t.Fatalf("node %s inputs.from_sharedfiles missing upstream path %q", node.NodeKey, path)
		}
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

func testObjectMap(t *testing.T, obj map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := obj[key].(map[string]any)
	if !ok {
		t.Fatalf("%s missing or not object: %+v", key, obj[key])
	}
	return value
}

func testStringSetFromAnyList(t *testing.T, value any, nodeKey string) map[string]bool {
	t.Helper()

	items, ok := value.([]any)
	if !ok {
		t.Fatalf("node %s inputs.from_sharedfiles missing or not a list: %+v", nodeKey, value)
	}
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[strings.TrimSpace(fmt.Sprint(item))] = true
	}
	return out
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
