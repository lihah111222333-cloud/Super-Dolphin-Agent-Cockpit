package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/workflowtemplates"
)

func TestWorkflowTemplateHostToolRegistry_ListSchemaAndFilters(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry()
	tools := reg.ListHostTools()
	assertWorkflowTemplateToolNames(t, tools)

	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameWorkflowTemplateList,
		Arguments: json.RawMessage(`{"category":"government-enterprise","output_type":"markdown"}`),
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
		if tpl.Category != "government-enterprise" || !workflowTemplateHasOutputType(tpl.OutputTypes, "markdown") {
			t.Fatalf("filtered template mismatch: %+v", tpl)
		}
	}
}

func TestWorkflowTemplateHostToolRegistry_GetAndRenderDAG(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry()
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
				"output_format":"markdown",
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

func TestWorkflowTemplateHostToolRegistry_CWDisOptionalThroughHandler(t *testing.T) {
	reg := NewWorkflowTemplateHostToolRegistry()
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
	reg := NewWorkflowTemplateHostToolRegistry()
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

	if draft.FinalOutput.NodeKey != "final_minutes" || !strings.Contains(draft.FinalOutput.PathTemplate, "final.markdown") {
		t.Fatalf("draft final output = %+v", draft.FinalOutput)
	}
}
