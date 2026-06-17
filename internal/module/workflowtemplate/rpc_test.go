package workflowtemplate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestWorkflowTemplateHandlersListGetAndRender(t *testing.T) {
	t.Parallel()

	svc, err := NewService()
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)

	assertWorkflowTemplateList(t, server)
	assertWorkflowTemplateGet(t, server)
	assertWorkflowTemplateRender(t, server)
}

func assertWorkflowTemplateList(t *testing.T, server *platformrpc.Server) {
	t.Helper()

	raw, err := server.Dispatch(context.Background(), "workflowTemplates/list", json.RawMessage(`{"category":"government-enterprise","output_type":"docx"}`))
	if err != nil {
		t.Fatalf("workflowTemplates/list error = %v", err)
	}
	var response listResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode workflowTemplates/list response: %v", err)
	}
	if len(response.Templates) == 0 {
		t.Fatalf("workflowTemplates/list returned no templates")
	}
}

func assertWorkflowTemplateGet(t *testing.T, server *platformrpc.Server) {
	t.Helper()

	raw, err := server.Dispatch(context.Background(), "workflowTemplates/get", json.RawMessage(`{"templateId":"government-enterprise/approval-material","version":1}`))
	if err != nil {
		t.Fatalf("workflowTemplates/get error = %v", err)
	}
	var response getResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode workflowTemplates/get response: %v", err)
	}
	if response.Template.ID != "government-enterprise/approval-material" || response.Template.Version != 1 {
		t.Fatalf("workflowTemplates/get template = %+v", response.Template)
	}
}

func assertWorkflowTemplateRender(t *testing.T, server *platformrpc.Server) {
	t.Helper()

	raw, err := server.Dispatch(context.Background(), "workflowTemplates/renderDag", json.RawMessage(`{
		"templateId":"government-enterprise/approval-material",
		"version":1,
		"values":{
			"title":"数据共享申请",
			"approval_basis":"数据共享管理办法",
			"source_materials":"approval/source.md",
			"output_format":"docx",
			"reviewer":"审批经办人",
			"output_path":"reports/workflows/government_enterprise_approval_material/{{run_id}}/"
		}
	}`))
	if err != nil {
		t.Fatalf("workflowTemplates/renderDag error = %v", err)
	}
	var response renderResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode workflowTemplates/renderDag response: %v", err)
	}
	if response.Draft.FinalNodeKey != "final_pack" {
		t.Fatalf("workflowTemplates/renderDag final = %q", response.Draft.FinalNodeKey)
	}
}
