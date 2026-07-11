package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/workflowtemplates"
)

func TestWorkflowTemplateHostToolRegistry_ListSchemaAndFilters(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry(newWorkflowTemplateRegistryForTest(t))
	tools := reg.ListHostTools()
	assertWorkflowTemplateToolNames(t, tools)

	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameWorkflowTemplateList,
		Arguments: json.RawMessage(`{"category":"government-enterprise","output_type":"docx"}`),
	})
	if err != nil {
		t.Fatalf("workflow_template_list error = %v", err)
	}
	list, ok := result.(workflowTemplateListResult)
	if !ok {
		t.Fatalf("workflow_template_list result type = %T", result)
	}
	if len(list.Templates) == 0 {
		t.Fatalf("workflow_template_list returned no templates")
	}
	for _, tpl := range list.Templates {
		if tpl.Category != "government-enterprise" || !workflowTemplateHasOutputType(tpl.OutputTypes, "docx") {
			t.Fatalf("filtered template mismatch: %+v", tpl)
		}
	}
}

func TestWorkflowTemplateHostToolRegistry_DefaultCodexToolsHideWriteEntrypoints(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry(newWorkflowTemplateRegistryForTest(t))
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindOrch: {listToolsPeer(nil, nil)},
			dto.ClientKindLSP:  {listToolsPeer(nil, nil)},
		}},
		hostTools: reg,
	}

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, forbidden := range []string{ToolNameWorkflowTemplateSave, ToolNameWorkflowTemplateRollback} {
		if names[forbidden] {
			t.Fatalf("default dynamic tools exposed write entrypoint %q in %#v", forbidden, names)
		}
	}
	for _, want := range []string{ToolNameWorkflowTemplateList, ToolNameWorkflowTemplateGet, ToolNameWorkflowTemplateRenderDAG} {
		if !names[want] {
			t.Fatalf("default dynamic tools missing read entrypoint %q in %#v", want, names)
		}
	}
}

func TestWorkflowTemplateHostToolRegistry_GetAndRenderDAG(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry(newWorkflowTemplateRegistryForTest(t))
	getResult, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameWorkflowTemplateGet,
		Arguments: json.RawMessage(`{"template_id":"government-enterprise/meeting-minutes","version":1}`),
	})
	if err != nil {
		t.Fatalf("workflow_template_get error = %v", err)
	}
	got, ok := getResult.(workflowTemplateGetResult)
	if !ok {
		t.Fatalf("workflow_template_get result type = %T", getResult)
	}
	if got.Template.ID != "government-enterprise/meeting-minutes" || len(got.Template.UISchema) == 0 {
		t.Fatalf("workflow_template_get template = %+v", got.Template)
	}

	renderResult, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name: ToolNameWorkflowTemplateRenderDAG,
		Arguments: json.RawMessage(`{
			"template_id":"government-enterprise/meeting-minutes",
			"version":1,
			"user_inputs":{
				"title":"6月项目例会",
				"source_materials":"reports/workflows/input/meeting.md",
				"output_format":"docx",
				"reviewer":"会议主持人",
				"output_path":"reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/"
			}
		}`),
	})
	if err != nil {
		t.Fatalf("workflow_template_render_dag error = %v", err)
	}
	rendered, ok := renderResult.(workflowTemplateRenderResult)
	if !ok {
		t.Fatalf("workflow_template_render_dag result type = %T", renderResult)
	}
	assertMeetingMinutesDraft(t, rendered.Draft)
}

func TestWorkflowTemplateHostToolRegistry_DefaultDirectWriteRequiresApproval(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry(newWorkflowTemplateRegistryForTest(t))
	h := &Handler{
		registry:  &stubKindRegistry{},
		hostTools: reg,
	}

	for _, toolName := range []string{ToolNameWorkflowTemplateSave, ToolNameWorkflowTemplateRollback} {
		got, err := h.routeToolCall(context.Background(), ToolCallRequest{
			Name:      toolName,
			Arguments: json.RawMessage(`{}`),
			AgentID:   "agent-1",
			ThreadID:  "thread-1",
			CallID:    "call-1",
		})
		if err != nil {
			t.Fatalf("%s routeToolCall() error = %v", toolName, err)
		}
		envelope := decodeToolResultEnvelope(t, got)
		if got.Success || envelope["kind"] != "approval_required" {
			t.Fatalf("%s result = success:%v envelope:%#v, want approval_required failure", toolName, got.Success, envelope)
		}
	}
}

func TestWorkflowTemplateHostToolRegistry_SaveAndRollback(t *testing.T) {
	registry := newWorkflowTemplateRegistryForTest(t)
	readReg := NewWorkflowTemplateHostToolRegistry(registry)
	writeReg := NewWorkflowTemplateWriteHostToolRegistry(registry, allowWorkflowTemplateWriteAuthority{})
	renderResult, err := readReg.CallHostTool(context.Background(), HostToolCall{
		Name: ToolNameWorkflowTemplateRenderDAG,
		Arguments: json.RawMessage(`{
			"template_id":"government-enterprise/meeting-minutes",
			"version":1,
			"user_inputs":{
				"title":"6月项目例会",
				"source_materials":"reports/workflows/input/meeting.md",
				"output_format":"docx",
				"reviewer":"会议主持人",
				"output_path":"reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/"
			},
			"runtime_context":{"cwd":"D:/project/demo"}
		}`),
	})
	if err != nil {
		t.Fatalf("workflow_template_render_dag error = %v", err)
	}
	rendered := renderResult.(workflowTemplateRenderResult)

	savePayload := map[string]any{
		"template_id":       "government-enterprise/meeting-minutes",
		"version":           2,
		"title":             map[string]any{"zh": "会议纪要 v2", "en": "Meeting Minutes v2"},
		"description":       map[string]any{"zh": "保存后的会议纪要模板", "en": "Saved meeting template"},
		"category":          "government-enterprise",
		"business_flow":     "会议督办",
		"output_types":      []string{"docx", "pdf"},
		"tags":              []string{"政企", "会议"},
		"requires_review":   true,
		"supports_schedule": false,
		"ui_schema": []map[string]any{
			{"key": "title", "type": "text", "required": true, "label": map[string]any{"zh": "会议名称"}, "placeholder": map[string]any{"zh": "例如：6月项目推进会"}, "help": map[string]any{"zh": "用于纪要标题。"}},
			{"key": "output_path", "type": "path", "required": true, "label": map[string]any{"zh": "保存目录"}, "placeholder": map[string]any{"zh": "reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/"}, "help": map[string]any{"zh": "最终纪要保存位置。"}},
		},
		"validation":    map[string]any{"sharedfile_prefixes": []string{"reports/workflows/", "dag/"}, "require_review_before_final": true, "require_final_node_key": true},
		"trust":         map[string]any{"level": "user", "source": "user_saved"},
		"compatibility": map[string]any{"runtime": "dag-v2", "node_types": []string{"agent"}, "required_capabilities": []string{"workflow.node.agent", "workflow.output.sharedfile", "workflow.output.artifact", "workflow.final_output"}},
		"draft":         rendered.Draft,
	}
	saveRaw, err := json.Marshal(savePayload)
	if err != nil {
		t.Fatalf("marshal save payload: %v", err)
	}
	saveResult, err := writeReg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameWorkflowTemplateSave,
		Arguments: saveRaw,
	})
	if err != nil {
		t.Fatalf("workflow_template_save error = %v", err)
	}
	saved := saveResult.(workflowTemplateSaveResult)
	if saved.Template.Version != 2 || len(saved.Template.AvailableVersions) != 2 {
		t.Fatalf("save result template = %+v", saved.Template)
	}

	rollbackResult, err := writeReg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameWorkflowTemplateRollback,
		Arguments: json.RawMessage(`{"template_id":"government-enterprise/meeting-minutes","version":1}`),
	})
	if err != nil {
		t.Fatalf("workflow_template_rollback error = %v", err)
	}
	rolledBack := rollbackResult.(workflowTemplateRollbackResult)
	if rolledBack.Template.Version != 1 {
		t.Fatalf("rollback version = %d, want 1", rolledBack.Template.Version)
	}
}

type allowWorkflowTemplateWriteAuthority struct{}

func (allowWorkflowTemplateWriteAuthority) AuthorizeWorkflowTemplateWrite(context.Context, HostToolCall) error {
	return nil
}

func newWorkflowTemplateRegistryForTest(t *testing.T) *workflowtemplates.Registry {
	t.Helper()

	registry, err := workflowtemplates.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	return registry
}

func TestWorkflowTemplateHostToolRegistry_CWDisOptionalThroughHandler(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry(newWorkflowTemplateRegistryForTest(t))
	h := &Handler{hostTools: reg}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{
		Name:      ToolNameWorkflowTemplateList,
		Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}
	if got == nil || !got.Success {
		t.Fatalf("callHostTool() result = %#v, want success", got)
	}
	var envelope workflowTemplateListResult
	if err := json.Unmarshal([]byte(got.ContentItems[0].Text), &envelope); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(envelope.Templates) != 6 {
		t.Fatalf("templates = %d, want 6", len(envelope.Templates))
	}
}

func TestWorkflowTemplateHostToolRegistry_InvalidInputFailsFast(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry(newWorkflowTemplateRegistryForTest(t))
	_, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameWorkflowTemplateRenderDAG,
		Arguments: json.RawMessage(`{"template_id":"government-enterprise/meeting-minutes","unknown":true}`),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("workflow_template_render_dag err = %v, want unknown field failure", err)
	}

	_, err = reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameWorkflowTemplateGet,
		Arguments: json.RawMessage(`{"template_id":"government-enterprise/meeting-minutes","version":0}`),
	})
	if err == nil || !strings.Contains(err.Error(), "version 0 not found") {
		t.Fatalf("workflow_template_get err = %v, want version failure", err)
	}
}

func assertWorkflowTemplateToolNames(t *testing.T, tools []dto.MCPTool) {
	t.Helper()
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		names[tool.Name] = struct{}{}
		if len(tool.InputSchema) == 0 {
			t.Fatalf("tool %q missing input schema", tool.Name)
		}
	}
	for _, want := range []string{ToolNameWorkflowTemplateList, ToolNameWorkflowTemplateGet, ToolNameWorkflowTemplateRenderDAG} {
		if _, ok := names[want]; !ok {
			t.Fatalf("workflow template tools = %+v, missing %q", tools, want)
		}
	}
	for _, forbidden := range []string{ToolNameWorkflowTemplateSave, ToolNameWorkflowTemplateRollback} {
		if _, ok := names[forbidden]; ok {
			t.Fatalf("workflow template tools = %+v, must not expose write tool %q", tools, forbidden)
		}
	}
}

func assertMeetingMinutesDraft(t *testing.T, draft workflowtemplates.DAGDraft) {
	t.Helper()
	assertMeetingMinutesDraftKeys(t, draft)
	assertMeetingMinutesDraftNodes(t, draft)
	assertMeetingMinutesFinalOutput(t, draft)
}

func assertMeetingMinutesDraftKeys(t *testing.T, draft workflowtemplates.DAGDraft) {
	t.Helper()

	if draft.TemplateID != "government-enterprise/meeting-minutes" || draft.TemplateVersion != 1 || draft.FinalNodeKey != "final_minutes" || draft.ReviewNodeKey != "review" {
		t.Fatalf("draft keys = %+v", draft)
	}
}

func assertMeetingMinutesDraftNodes(t *testing.T, draft workflowtemplates.DAGDraft) {
	t.Helper()

	if len(draft.Nodes) != 5 {
		t.Fatalf("draft nodes = %d, want 5", len(draft.Nodes))
	}
	for _, node := range draft.Nodes {
		if node.AssignedTo == "" {
			t.Fatalf("node missing assigned_to: %+v", node)
		}
		if _, ok := node.Config["ui"]; !ok {
			t.Fatalf("node missing config.ui: %+v", node)
		}
	}
}

func assertMeetingMinutesFinalOutput(t *testing.T, draft workflowtemplates.DAGDraft) {
	t.Helper()

	if draft.FinalOutput.NodeKey != "final_minutes" || draft.FinalOutput.Kind != "artifact" || !strings.Contains(draft.FinalOutput.PathTemplate, "final.docx") {
		t.Fatalf("draft final output = %+v", draft.FinalOutput)
	}
}
