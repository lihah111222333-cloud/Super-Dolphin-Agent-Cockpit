package dashboard

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dashboardWireDTOChecklistEntry struct {
	Endpoint   string
	SourceFile string
	StoreDTO   string
	LocalDTO   string
}

var dashboardWireDTOChecklist = []dashboardWireDTOChecklistEntry{
	{"dashboard/agentStatus", "agent_status.go", "agentstatusstore.AgentStatus", "dashboard.AgentStatus"},
	{"dashboard/logs/audit", "logs.go", "auditlogstore.AuditEvent", "dashboard.AuditEvent"},
	{"dashboard/logs/bus", "logs.go", "buslogstore.BusExceptionLog", "dashboard.BusExceptionLog"},
	{"dashboard/logs/system", "factory.go", "systemlogstore.ListFilter", "dashboard.SystemLogFilter"},
	{"dashboard/aiLogs", "ai_logs.go", "ailogstore.AILog", "dashboard.AILog"},
	{"dashboard/aiLogs/stats", "rpc.go", "ailogstore.StatusCount", "dashboard.AILogStatusCount"},
	{"dashboard/commandCards", "rpc.go", "commandcardstore.CommandCard", "dashboard.CommandCard"},
	{"dashboard/prompts", "rpc.go", "promptstore.PromptTemplate", "dashboard.PromptTemplate"},
	{"dashboard/sharedFiles", "rpc.go", "sharedfilestore.SharedFile", "dashboard.SharedFile"},
	{"dashboard/workflowMaterialWrite", "workflow_material.go", "sharedfilestore.UpsertParams", "dashboard.SharedFileUpsertParams"},
}

// TestDashboardWireDTOChecklistCoversPlannedStoreDTOs 锁定 dashboard 转换清单。
func TestDashboardWireDTOChecklistCoversPlannedStoreDTOs(t *testing.T) {
	t.Parallel()
	for _, entry := range dashboardWireDTOChecklist {
		if entry.Endpoint == "" || entry.SourceFile == "" || entry.StoreDTO == "" || entry.LocalDTO == "" {
			t.Fatalf("incomplete wire DTO checklist entry: %#v", entry)
		}
	}
}

// TestDashboardWireDTOJSONShape 锁定本地 wire DTO 的 JSON 字段和值，不依赖 App mapper 或 Store DTO。
func TestDashboardWireDTOJSONShape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 27, 8, 9, 10, 0, time.UTC)
	duration := int32(123)
	lastRun := now.Add(-time.Hour)
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{"agent", AgentStatus{AgentID: "a", OutputTail: json.RawMessage(`{"ok":true}`), CreatedAt: now, UpdatedAt: now}, `{"agent_id":"a","agent_name":"","session_id":"","status":"","stagnant_sec":0,"error":"","output_tail":{"ok":true},"created_at":"2026-06-27T08:09:10Z","updated_at":"2026-06-27T08:09:10Z"}`},
		{"ai", AILog{ID: 1, DurationMs: &duration, Extra: json.RawMessage(`{"a":1}`)}, `{"ID":1,"Ts":"0001-01-01T00:00:00Z","Level":"","Logger":"","Message":"","Raw":"","Source":"","Component":"","AgentID":"","ThreadID":"","TraceID":"","SpanID":"","ParentSpanID":"","EventType":"","ToolName":"","DurationMs":123,"Extra":{"a":1},"Category":"","Method":"","URL":"","Endpoint":"","Status":"","StatusText":"","Model":""}`},
		{"audit", AuditEvent{ID: 2, Extra: json.RawMessage(`{"b":2}`)}, `{"id":2,"ts":"0001-01-01T00:00:00Z","event_type":"","action":"","result":"","actor":"","target":"","detail":"","level":"","extra":{"b":2}}`},
		{"bus", BusExceptionLog{ID: 3, Extra: json.RawMessage(`{"c":3}`)}, `{"id":3,"ts":"0001-01-01T00:00:00Z","category":"","severity":"","source":"","tool_name":"","message":"","traceback":"","extra":{"c":3},"has_traceback":false,"has_extra":false}`},
		{"command", CommandCard{ID: 4, ArgsSchema: json.RawMessage(`{"type":"object"}`), LastRunAt: &lastRun}, `{"id":4,"card_key":"","title":"","description":"","command_template":"","args_schema":{"type":"object"},"risk_level":"","enabled":false,"created_by":"","updated_by":"","created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z","last_run_at":"2026-06-27T07:09:10Z","run_count":0}`},
		{"prompt", PromptTemplate{ID: 5, Tags: json.RawMessage(`[]`)}, `{"id":5,"prompt_key":"","title":"","agent_key":"","tool_name":"","prompt_text":"","when_to_use":"","variables":null,"tags":[],"enabled":false,"manually_edited":false,"priority":0,"created_by":"","updated_by":"","created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z","description":""}`},
		{"shared", SharedFile{Path: "reports/final.md", Content: "done"}, `{"path":"reports/final.md","content":"done","updated_by":"","created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(test.value)
			require.NoError(t, err)
			require.JSONEq(t, test.expected, string(payload))
		})
	}
}
