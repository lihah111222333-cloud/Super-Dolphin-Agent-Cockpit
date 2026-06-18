package workflowtemplate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
)

func TestWorkflowTemplateHandlersListGetAndRender(t *testing.T) {
	t.Parallel()

	server := newWorkflowTemplateRPCServer(t)

	assertWorkflowTemplateList(t, server)
	assertWorkflowTemplateGet(t, server)
	assertWorkflowTemplateRender(t, server)
}

func TestWorkflowTemplateHandlersMapErrorsToRPCCodes(t *testing.T) {
	t.Parallel()

	server := newWorkflowTemplateRPCServer(t)
	tests := []struct {
		name   string
		method string
		params string
		code   int
	}{
		{
			name:   "get missing template id",
			method: "workflowTemplates/get",
			params: `{}`,
			code:   platformrpc.CodeInvalidParams,
		},
		{
			name:   "get unknown template",
			method: "workflowTemplates/get",
			params: `{"templateId":"missing/template"}`,
			code:   platformrpc.CodeNotFound,
		},
		{
			name:   "get unknown version",
			method: "workflowTemplates/get",
			params: `{"templateId":"government-enterprise/approval-material","version":99}`,
			code:   platformrpc.CodeNotFound,
		},
		{
			name:   "render missing template id",
			method: "workflowTemplates/renderDag",
			params: `{}`,
			code:   platformrpc.CodeInvalidParams,
		},
		{
			name:   "render unknown template",
			method: "workflowTemplates/renderDag",
			params: `{"templateId":"missing/template"}`,
			code:   platformrpc.CodeNotFound,
		},
		{
			name:   "render missing required fields",
			method: "workflowTemplates/renderDag",
			params: `{"templateId":"government-enterprise/approval-material","version":1}`,
			code:   platformrpc.CodeInvalidParams,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertWorkflowTemplateRPCCode(t, server, tt.method, json.RawMessage(tt.params), tt.code)
		})
	}
}

func newWorkflowTemplateRPCServer(t *testing.T) *platformrpc.Server {
	t.Helper()

	svc, err := NewService()
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}

func assertWorkflowTemplateRPCCode(t *testing.T, server *platformrpc.Server, method string, params json.RawMessage, want int) {
	t.Helper()

	_, err := server.Dispatch(context.Background(), method, params)
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("%s error = %T, want *jrpc2.Error", method, err)
	}
	if rpcErr.Code != jrpc2.Code(want) {
		t.Fatalf("%s code = %v, want %v", method, rpcErr.Code, want)
	}
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
