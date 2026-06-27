package dashboard

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

type dashboardWireDTOChecklistEntry struct {
	Endpoint   string
	SourceFile string
	StoreDTO   string
	LocalDTO   string
}

var dashboardWireDTOChecklist = []dashboardWireDTOChecklistEntry{
	{"dashboard/agentStatus", "agent_status.go", "agentstatusstore.AgentStatus", "dashboard.AgentStatus"},
	{"dashboard/agentStatus", "contract.go", "agentstatusstore.AgentStatus", "dashboard.AgentStatus"},
	{"dashboard/agentStatus", "rpc.go", "agentstatusstore.AgentStatus", "dashboard.AgentStatus"},
	{"dashboard/logs/audit", "logs.go", "auditlogstore.ListFilter", "dashboard.AuditLogFilter"},
	{"dashboard/logs/audit", "logs.go", "auditlogstore.AuditEvent", "dashboard.AuditEvent"},
	{"dashboard/logs/bus", "logs.go", "buslogstore.ListFilter", "dashboard.BusLogFilter"},
	{"dashboard/logs/bus", "logs.go", "buslogstore.BusExceptionLog", "dashboard.BusExceptionLog"},
	{"dashboard/logs/system", "factory.go", "systemlogstore.ListFilter", "dashboard.SystemLogFilter"},
	{"dashboard/aiLogs", "ai_logs.go", "ailogstore.AILog", "dashboard.AILog"},
	{"dashboard/aiLogs/recent", "rpc.go", "ailogstore.AILog", "dashboard.AILog"},
	{"dashboard/aiLogs/stats", "rpc.go", "ailogstore.StatusCount", "dashboard.AILogStatusCount"},
	{"dashboard/commandCards", "ui_page.go", "commandcardstore.CommandCard", "dashboard.CommandCard"},
	{"dashboard/commandCards", "rpc.go", "commandcardstore.CommandCard", "dashboard.CommandCard"},
	{"dashboard/prompts", "ui_page.go", "promptstore.PromptTemplate", "dashboard.PromptTemplate"},
	{"dashboard/prompts", "rpc.go", "promptstore.PromptTemplate", "dashboard.PromptTemplate"},
	{"dashboard/sharedFiles", "ui_page.go", "sharedfilestore.SharedFile", "dashboard.SharedFile"},
	{"dashboard/sharedFiles", "rpc.go", "sharedfilestore.SharedFile", "dashboard.SharedFile"},
	{"dashboard/workflowMaterialWrite", "workflow_material.go", "sharedfilestore.UpsertParams", "dashboard.SharedFileUpsertParams"},
	{"dashboard/workflowMaterialWrite", "workflow_material.go", "sharedfilestore.SharedFile", "dashboard.SharedFile"},
}

// TestDashboardWireDTOChecklistCoversPlannedStoreDTOs 锁定 D01 dashboard lane 的转换清单。
func TestDashboardWireDTOChecklistCoversPlannedStoreDTOs(t *testing.T) {
	t.Parallel()

	required := map[string]bool{
		"dashboard/agentStatus|agent_status.go|agentstatusstore.AgentStatus":                false,
		"dashboard/logs/audit|logs.go|auditlogstore.AuditEvent":                             false,
		"dashboard/logs/bus|logs.go|buslogstore.BusExceptionLog":                            false,
		"dashboard/logs/system|factory.go|systemlogstore.ListFilter":                        false,
		"dashboard/aiLogs|ai_logs.go|ailogstore.AILog":                                      false,
		"dashboard/aiLogs/stats|rpc.go|ailogstore.StatusCount":                              false,
		"dashboard/commandCards|rpc.go|commandcardstore.CommandCard":                        false,
		"dashboard/prompts|rpc.go|promptstore.PromptTemplate":                               false,
		"dashboard/sharedFiles|rpc.go|sharedfilestore.SharedFile":                           false,
		"dashboard/workflowMaterialWrite|workflow_material.go|sharedfilestore.UpsertParams": false,
	}
	for _, entry := range dashboardWireDTOChecklist {
		key := entry.Endpoint + "|" + entry.SourceFile + "|" + entry.StoreDTO
		if _, ok := required[key]; ok {
			required[key] = true
		}
		if entry.LocalDTO == "" {
			t.Fatalf("wire DTO checklist entry missing local DTO: %#v", entry)
		}
	}
	for key, seen := range required {
		if !seen {
			t.Fatalf("wire DTO checklist missing %s", key)
		}
	}
}

// TestDashboardWireDTOJSONMatchesStoreDTOs 确认 dashboard DTO 脱钩不改变前端 JSON 字段和值。
func TestDashboardWireDTOJSONMatchesStoreDTOs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 27, 8, 9, 10, 0, time.UTC)
	duration := int32(123)
	lastRun := now.Add(-time.Hour)
	cases := []struct {
		name  string
		store any
		local any
	}{
		{
			name: "agent status",
			store: agentstatusstore.AgentStatus{
				AgentID: "agent-1", AgentName: "worker", SessionID: "session-1", Status: "running",
				StagnantSec: 7, Error: "", OutputTail: json.RawMessage(`{"tail":true}`), CreatedAt: now, UpdatedAt: now,
			},
			local: mapAgentStatuses([]agentstatusstore.AgentStatus{{
				AgentID: "agent-1", AgentName: "worker", SessionID: "session-1", Status: "running",
				StagnantSec: 7, Error: "", OutputTail: json.RawMessage(`{"tail":true}`), CreatedAt: now, UpdatedAt: now,
			}})[0],
		},
		{
			name:  "ai log",
			store: ailogstore.AILog{ID: 1, Ts: now, Level: "info", Message: "ok", DurationMs: &duration, Extra: json.RawMessage(`{"a":1}`), Category: "api_request", Status: "200"},
			local: mapAILogs([]ailogstore.AILog{{ID: 1, Ts: now, Level: "info", Message: "ok", DurationMs: &duration, Extra: json.RawMessage(`{"a":1}`), Category: "api_request", Status: "200"}})[0],
		},
		{
			name:  "ai status count",
			store: ailogstore.StatusCount{Status: "200", Count: 3},
			local: mapAILogStatusCounts([]ailogstore.StatusCount{{Status: "200", Count: 3}})[0],
		},
		{
			name:  "audit event",
			store: auditlogstore.AuditEvent{ID: 2, Ts: now, EventType: "tool", Action: "run", Result: "ok", Actor: "agent", Extra: json.RawMessage(`{"b":2}`)},
			local: mapAuditEvents([]auditlogstore.AuditEvent{{ID: 2, Ts: now, EventType: "tool", Action: "run", Result: "ok", Actor: "agent", Extra: json.RawMessage(`{"b":2}`)}})[0],
		},
		{
			name:  "bus log",
			store: buslogstore.BusExceptionLog{ID: 3, Ts: now, Category: "rpc", Severity: "error", Source: "bus", ToolName: "tool", Message: "boom", Extra: json.RawMessage(`{"c":3}`)},
			local: mapBusExceptionLogs([]buslogstore.BusExceptionLog{{ID: 3, Ts: now, Category: "rpc", Severity: "error", Source: "bus", ToolName: "tool", Message: "boom", Extra: json.RawMessage(`{"c":3}`)}})[0],
		},
		{
			name: "command card",
			store: commandcardstore.CommandCard{
				ID: 4, CardKey: "cmd/review", Title: "Review", CommandTemplate: "review", ArgsSchema: json.RawMessage(`{"type":"object"}`),
				RiskLevel: "medium", Enabled: true, CreatedBy: "seed", UpdatedBy: "seed", CreatedAt: now, UpdatedAt: now, LastRunAt: &lastRun, RunCount: 5,
			},
			local: mapCommandCards([]commandcardstore.CommandCard{{
				ID: 4, CardKey: "cmd/review", Title: "Review", CommandTemplate: "review", ArgsSchema: json.RawMessage(`{"type":"object"}`),
				RiskLevel: "medium", Enabled: true, CreatedBy: "seed", UpdatedBy: "seed", CreatedAt: now, UpdatedAt: now, LastRunAt: &lastRun, RunCount: 5,
			}})[0],
		},
		{
			name:  "prompt template",
			store: promptstore.PromptTemplate{ID: 5, PromptKey: "p", Title: "Prompt", Tags: json.RawMessage(`["scope.cwd:/repo"]`), Enabled: true, Priority: 9, CreatedAt: now, UpdatedAt: now},
			local: mapPromptTemplates([]promptstore.PromptTemplate{{ID: 5, PromptKey: "p", Title: "Prompt", Tags: json.RawMessage(`["scope.cwd:/repo"]`), Enabled: true, Priority: 9, CreatedAt: now, UpdatedAt: now}})[0],
		},
		{
			name:  "shared file",
			store: sharedfilestore.SharedFile{Path: "reports/final.md", Content: "done", UpdatedBy: "agent", CreatedAt: now, UpdatedAt: now},
			local: mapSharedFiles([]sharedfilestore.SharedFile{{Path: "reports/final.md", Content: "done", UpdatedBy: "agent", CreatedAt: now, UpdatedAt: now}})[0],
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSameJSONPayload(t, tc.store, tc.local)
		})
	}
}

func assertSameJSONPayload(t *testing.T, storeDTO any, localDTO any) {
	t.Helper()

	storePayload := mustJSONMap(t, storeDTO)
	localPayload := mustJSONMap(t, localDTO)
	if !reflect.DeepEqual(localPayload, storePayload) {
		t.Fatalf("dashboard wire payload changed\nstore=%s\nlocal=%s", mustJSONString(t, storeDTO), mustJSONString(t, localDTO))
	}
}

func mustJSONMap(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()

	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mustJSONString(t, value)), &out); err != nil {
		t.Fatalf("json.Unmarshal(%T) error = %v", value, err)
	}
	return out
}

func mustJSONString(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T) error = %v", value, err)
	}
	return string(data)
}
