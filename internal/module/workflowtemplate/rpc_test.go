package workflowtemplate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/workflowtemplates"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge"
)

func TestWorkflowTemplateHandlersListGetAndRender(t *testing.T) {
	t.Parallel()

	server := newWorkflowTemplateRPCServer(t)

	assertWorkflowTemplateList(t, server)
	assertWorkflowTemplateGet(t, server)
	assertWorkflowTemplateRender(t, server)
}

func TestWorkflowTemplateHandlersSaveAndRollbackTemplate(t *testing.T) {
	t.Parallel()

	server := newWorkflowTemplateRPCServer(t)
	assertWorkflowTemplateSave(t, server)
	assertWorkflowTemplateRollback(t, server)
}

func TestWorkflowTemplateRPCAndHostToolsShareTemplateRegistry(t *testing.T) {
	t.Parallel()

	registry := newWorkflowTemplateRegistryForTest(t)
	svc, err := NewService(registry)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	readHost := toolbridge.NewWorkflowTemplateHostToolRegistry(registry)
	writeHost := toolbridge.NewWorkflowTemplateWriteHostToolRegistry(registry, allowWorkflowTemplateWriteAuthority{})

	assertWorkflowTemplateSave(t, server)
	listResult, err := readHost.CallHostTool(context.Background(), toolbridge.HostToolCall{
		Name:      toolbridge.ToolNameWorkflowTemplateList,
		Arguments: json.RawMessage(`{"category":"government-enterprise","output_type":"docx"}`),
	})
	if err != nil {
		t.Fatalf("host workflow_template_list after RPC save error = %v", err)
	}
	assertWorkflowTemplateHostListVersion(t, listResult, "government-enterprise/meeting-minutes", 2)
	if _, err := writeHost.CallHostTool(context.Background(), toolbridge.HostToolCall{
		Name:      toolbridge.ToolNameWorkflowTemplateRollback,
		Arguments: json.RawMessage(`{"template_id":"government-enterprise/meeting-minutes","version":1}`),
	}); err != nil {
		t.Fatalf("host workflow_template_rollback error = %v", err)
	}
	if _, err := server.Dispatch(context.Background(), "workflowTemplates/get", json.RawMessage(`{"templateId":"government-enterprise/meeting-minutes","version":1}`)); err != nil {
		t.Fatalf("RPC workflowTemplates/get after host rollback error = %v", err)
	}
}

type allowWorkflowTemplateWriteAuthority struct{}

func (allowWorkflowTemplateWriteAuthority) AuthorizeWorkflowTemplateWrite(context.Context, toolbridge.HostToolCall) error {
	return nil
}

func assertWorkflowTemplateHostListVersion(t *testing.T, result any, id string, version int) {
	t.Helper()

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal host list result: %v", err)
	}
	var decoded struct {
		Templates []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode host list result: %v", err)
	}
	for _, tpl := range decoded.Templates {
		if tpl.ID == id {
			if tpl.Version != version {
				t.Fatalf("host list template %s version = %d, want %d", id, tpl.Version, version)
			}
			return
		}
	}
	t.Fatalf("host list missing template %s in %#v", id, decoded.Templates)
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
		{
			name:   "save missing version",
			method: "workflowTemplates/save",
			params: `{"templateId":"government-enterprise/meeting-minutes"}`,
			code:   platformrpc.CodeInvalidParams,
		},
		{
			name:   "rollback unknown version",
			method: "workflowTemplates/rollback",
			params: `{"templateId":"government-enterprise/meeting-minutes","version":99}`,
			code:   platformrpc.CodeNotFound,
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

	svc, err := NewService(newWorkflowTemplateRegistryForTest(t))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}

func newWorkflowTemplateRegistryForTest(t *testing.T) *workflowtemplates.Registry {
	t.Helper()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
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

func assertWorkflowTemplateSave(t *testing.T, server *platformrpc.Server) {
	t.Helper()

	raw, err := server.Dispatch(context.Background(), "workflowTemplates/renderDag", json.RawMessage(`{
		"templateId":"government-enterprise/meeting-minutes",
		"version":1,
		"values":{
			"title":"6月项目推进会",
			"source_materials":"meetings/raw.md",
			"output_format":"docx",
			"reviewer":"会议主持人",
			"output_path":"reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/"
		},
		"runtime_context":{"cwd":"D:/project/demo"}
	}`))
	if err != nil {
		t.Fatalf("workflowTemplates/renderDag before save error = %v", err)
	}
	var rendered renderResponse
	if err := json.Unmarshal(raw, &rendered); err != nil {
		t.Fatalf("decode render response: %v", err)
	}
	payload := map[string]any{
		"templateId":    "government-enterprise/meeting-minutes",
		"version":       2,
		"title":         map[string]any{"zh": "会议纪要 v2", "en": "Meeting Minutes v2"},
		"description":   map[string]any{"zh": "保存后的会议纪要模板", "en": "Saved meeting template"},
		"category":      "government-enterprise",
		"business_flow": "会议督办",
		"output_types":  []string{"docx", "pdf"},
		"tags":          []string{"政企", "会议"},
		"ui_schema": []map[string]any{
			{"key": "title", "type": "text", "required": true, "label": map[string]any{"zh": "会议名称"}, "placeholder": map[string]any{"zh": "例如：6月项目推进会"}, "help": map[string]any{"zh": "用于纪要标题。"}},
			{"key": "output_path", "type": "path", "required": true, "label": map[string]any{"zh": "保存目录"}, "placeholder": map[string]any{"zh": "reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/"}, "help": map[string]any{"zh": "最终纪要保存位置。"}},
		},
		"validation":        map[string]any{"sharedfile_prefixes": []string{"reports/workflows/", "dag/"}, "require_review_before_final": true, "require_final_node_key": true},
		"trust":             map[string]any{"level": "user", "source": "user_saved"},
		"compatibility":     map[string]any{"runtime": "dag-v2", "node_types": []string{"agent"}, "required_capabilities": []string{"workflow.node.agent", "workflow.output.sharedfile", "workflow.output.artifact", "workflow.final_output"}},
		"supports_schedule": false,
		"requires_review":   true,
		"draft":             rendered.Draft,
	}
	saveRaw, err := server.Dispatch(context.Background(), "workflowTemplates/save", mustJSON(t, payload))
	if err != nil {
		t.Fatalf("workflowTemplates/save error = %v", err)
	}
	var saved saveResponse
	if err := json.Unmarshal(saveRaw, &saved); err != nil {
		t.Fatalf("decode workflowTemplates/save response: %v", err)
	}
	if saved.Template.ID != "government-enterprise/meeting-minutes" || saved.Template.Version != 2 {
		t.Fatalf("saved template = %+v", saved.Template)
	}
}

func assertWorkflowTemplateRollback(t *testing.T, server *platformrpc.Server) {
	t.Helper()

	raw, err := server.Dispatch(context.Background(), "workflowTemplates/rollback", json.RawMessage(`{
		"templateId":"government-enterprise/meeting-minutes",
		"version":1
	}`))
	if err != nil {
		t.Fatalf("workflowTemplates/rollback error = %v", err)
	}
	var response rollbackResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode workflowTemplates/rollback response: %v", err)
	}
	if response.Template.Version != 1 {
		t.Fatalf("rollback active version = %d, want 1", response.Template.Version)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}
	return raw
}
