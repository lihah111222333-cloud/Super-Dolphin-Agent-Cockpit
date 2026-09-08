package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

// TestAgentOutputSchemaDTOFields 动态枚举真实 DTO，双向校验嵌套字段和必填登记。
func TestAgentOutputSchemaDTOFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  any
		schema Schema
	}{
		{"snapshot", contract.AgentSnapshot{}, agentSnapshotOutputSchema()},
		{"report", contract.AgentReportResult{}, agentReportOutputSchema()},
		{"list", ListAgentsOutput{}, coreAgentOutputSchema("list_agents")},
		{"reports", agentReportsResult{}, coreAgentOutputSchema("get_agent_reports")},
	} {
		t.Run(tc.name, func(t *testing.T) { guardAgentOutputDTO(t, reflect.TypeOf(tc.value), tc.schema) })
	}
}

// guardAgentOutputDTO 递归比较 JSON 标签与 schema，新增及陈旧字段均阻断。
func guardAgentOutputDTO(t *testing.T, typ reflect.Type, schema Schema) {
	t.Helper()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
		if branches, ok := schema["anyOf"].([]Schema); ok {
			schema = branches[0]
		}
	}
	if typ == reflect.TypeOf(time.Time{}) {
		if schema["type"] != "string" {
			t.Fatalf("time schema = %#v", schema)
		}
		return
	}
	if typ.Kind() == reflect.Slice {
		guardAgentOutputDTO(t, typ.Elem(), Schema(schema["items"].(map[string]any)))
		return
	}
	if typ.Kind() != reflect.Struct {
		guardAgentOutputScalar(t, typ, schema)
		return
	}
	guardAgentOutputStruct(t, typ, schema)
}

// guardAgentOutputStruct 比较结构体字段与 required 登记。
func guardAgentOutputStruct(t *testing.T, typ reflect.Type, schema Schema) {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s missing properties", typ)
	}
	required := map[string]bool{}
	if names, ok := schema["required"].([]string); ok {
		for _, name := range names {
			required[name] = true
		}
	}
	seen := map[string]bool{}
	for n := 0; n < typ.NumField(); n++ {
		guardAgentOutputField(t, typ.Field(n), props, required, seen)
	}
	for name := range props {
		if !seen[name] {
			t.Errorf("stale schema field %s.%s", typ, name)
		}
	}
}

// guardAgentOutputField 验证单字段标签、必填性和嵌套结构。
func guardAgentOutputField(t *testing.T, f reflect.StructField, props map[string]any, required, seen map[string]bool) {
	t.Helper()
	tag := strings.Split(f.Tag.Get("json"), ",")
	name := tag[0]
	if name == "" || name == "-" {
		t.Fatalf("unhandled DTO field %s", f.Name)
	}
	seen[name] = true
	raw, exists := props[name]
	if !exists {
		t.Errorf("missing schema field %s", name)
		return
	}
	optional := strings.Contains(f.Tag.Get("json"), ",omitempty") || strings.Contains(f.Tag.Get("json"), ",omitzero")
	if required[name] == optional {
		t.Errorf("%s required=%v optional=%v", name, required[name], optional)
	}
	guardAgentOutputDTO(t, f.Type, Schema(raw.(map[string]any)))
}

// TestAgentOutputSchemaCanonicalValues 校验真实 DTO 编码与协议数组拒绝边界。
func TestAgentOutputSchemaCanonicalValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"recover_agent", contract.AgentSnapshot{}}, {"get_agent_report", contract.AgentReportResult{}},
		{"list_agents", newListAgentsOutput([]contract.AgentSnapshot{{AgentID: "a"}}, 1)},
		{"get_agent_reports", agentReportsResult{Status: "completed", Results: []agentReportItem{{AgentID: "a"}}}},
		{"launch_agent", successResult(launchAgentAcceptedResult(contract.AgentSnapshot{AgentID: "a", ID: "runtime", ThreadID: "thread"}, "a"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := compileToolOutputSchema(tc.name, coreAgentOutputSchema(tc.name))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateToolOutputValue(tc.name, schema, tc.value); err != nil {
				t.Fatal(err)
			}
		})
	}
	schema, err := compileToolOutputSchema("list_agents", coreAgentOutputSchema("list_agents"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateToolOutputValue("list_agents", schema, []contract.AgentSnapshot{}); err == nil {
		t.Fatal("legacy array accepted")
	}
}

// TestListAgentsPaginationContract 验证完整过滤计数、分页和仅补水返回页。
func TestListAgentsPaginationContract(t *testing.T) {
	for _, count := range []int{0, 2, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			snapshots := []contract.AgentSnapshot{{AgentID: "outside", State: "idle", Cwd: "/other"}, {AgentID: "failed", State: "failed", Cwd: "/repo"}}
			for n := 0; n < count; n++ {
				snapshots = append(snapshots, contract.AgentSnapshot{AgentID: fmt.Sprint(n), State: "idle", Cwd: "/repo"})
			}
			reads := 0
			handler := handleListAgentsWithStub(&golden.OrchestrationStub{ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) { return snapshots, nil }, GetReportFunc: func(_ context.Context, id string) (contract.AgentReportResult, error) {
				reads++
				return contract.AgentReportResult{AgentID: id, Report: "report"}, nil
			}})
			value, err := handler(context.Background(), json.RawMessage(`{"cwd":"/repo","state":"idle","limit":2,"include_reports":true}`))
			if err != nil {
				t.Fatal(err)
			}
			out := value.(ListAgentsOutput)
			show := min(count, 2)
			assertAgentPage(t, out, count, show, reads)
			schema, err := compileToolOutputSchema("list_agents", coreAgentOutputSchema("list_agents"))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateToolOutputValue("list_agents", schema, value); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestListAgentsInvalidInputFailsBeforeRead 验证旧数组请求及错误过滤在服务调用前失败。
func TestListAgentsInvalidInputFailsBeforeRead(t *testing.T) {
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) { t.Fatal("unexpected read"); return nil, nil }})
	for _, input := range []string{`{"envelope":false}`, `{"limit":-1}`, `{"state":"typo"}`, `{"state":","}`} {
		if _, err := handler(context.Background(), json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}

// TestAgentOutputSchemaRejectsUnknownAndDrift 验证未知工具、未知字段和不完整成功分支均失败。
func TestAgentOutputSchemaRejectsUnknownAndDrift(t *testing.T) {
	if _, err := compileToolOutputSchema("unknown", coreAgentOutputSchema("unknown")); err == nil {
		t.Fatal("unknown agent schema accepted")
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"launch_agent", map[string]any{"success": false, "agent_id": "a", "status": "launching"}},
		{"send_message", map[string]any{"success": true, "agent_id": "a", "submitted": true}},
		{"stop_agent", map[string]any{"success": true, "agent_id": "a", "archived": false, "stopped": true}},
		{"interrupt_agent", map[string]any{"success": true, "agent_id": "a", "interrupted": true, "state": "idle", "active_turn_id": "stale"}},
		{"get_agent_report", map[string]any{"agent_id": "a", "report": "done", "report_seq": 1, "state": "idle", "unregistered": true}},
	} {
		schema, err := compileToolOutputSchema(tc.name, coreAgentOutputSchema(tc.name))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateToolOutputValue(tc.name, schema, tc.value); err == nil {
			t.Fatalf("%s accepted malformed output %#v", tc.name, tc.value)
		}
	}
}

// assertAgentPage 检查分页对象与报告补水次数。
func assertAgentPage(t *testing.T, out ListAgentsOutput, count, show, reads int) {
	t.Helper()
	if out.Total != count || out.Showing != show || out.Truncated != (count > 2) || len(out.Data) != show || reads != show {
		t.Fatalf("output=%#v reads=%d", out, reads)
	}
}

// TestAgentOutputSchemaRegistryHandlers 验证真实成功处理器经过注册表输出校验。
func TestAgentOutputSchemaRegistryHandlers(t *testing.T) {
	recovered := false
	reportSeq := int64(1)
	stub := &golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{ID: "runtime-b", AgentID: req.AgentID, ThreadID: "thread-b", State: "idle"}, nil
		},
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{AgentID: "a", State: "stopped"}}, nil
		},
		StopAgentFunc: func(context.Context, string) error { return nil },
		InterruptAgentFunc: func(_ context.Context, id, source string) (contract.AgentStateResult, error) {
			return contract.AgentStateResult{AgentID: id, State: "idle"}, nil
		},
		SnapshotFunc: func(_ context.Context, id string) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{AgentID: id, ThreadID: "thread", State: "idle"}, nil
		},
		SubmitTurnFunc: func(context.Context, contract.TurnSubmission) error { reportSeq++; return nil },
		GetReportFunc: func(_ context.Context, id string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: id, Report: "done", ReportSeq: reportSeq, State: "idle"}, nil
		},
	}
	for _, tc := range []struct {
		name, input string
		handler     ToolHandler
	}{
		{"launch_agent", `{"agent_id":"b","name":"worker","cwd":"/tmp/work","prompt":"work"}`, HandleLaunchAgent(stub)},
		{"send_message", `{"agent_id":"a","message":"next"}`, handleSendMessageWithStub(stub)},
		{"send_message", `{"agent_id":"a","message":"next","wait_report":true,"timeout_ms":100}`, handleSendMessageWithStub(stub)},
		{"stop_agent", `{"agent_id":"a"}`, HandleStopAgent(stub)},
		{"stop_agent", `{"agent_id":"a","wait":true}`, HandleStopAgent(stub)},
		{"interrupt_agent", `{"agent_id":"a"}`, HandleInterruptAgent(stub)},
		{"recover_agent", `{"agent_id":"agent-1"}`, HandleRecoverAgent(recoverSnapshotStub(t, "stopped", &recovered))},
		{"get_agent_report", `{"agent_id":"a"}`, HandleGetAgentReport(stub)},
		{"get_agent_reports", `{"agent_ids":["a"]}`, HandleGetAgentReports(stub)},
		{"list_agents", `{"include_inactive":true}`, handleListAgentsWithStub(stub)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := newRegistryFromToolDefinitions(nil, []ToolDefinition{{Name: tc.name, Metadata: ToolMetadata{OutputSchema: coreAgentOutputSchema(tc.name)}, Handler: tc.handler}})
			requireOutputSchemaRegistryReady(t, registry)
			def, ok := registry.Lookup(tc.name)
			if !ok {
				t.Fatal("definition missing")
			}
			if _, err := def.Handler(context.Background(), json.RawMessage(tc.input)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestAgentOutputContract 提供轻量宿主精确 selector，统一执行本输出契约的验证矩阵。
func TestAgentOutputContract(t *testing.T) {
	t.Run("fields", TestAgentOutputSchemaDTOFields)
	t.Run("canonical", TestAgentOutputSchemaCanonicalValues)
	t.Run("pagination", TestListAgentsPaginationContract)
	t.Run("invalid_input", TestListAgentsInvalidInputFailsBeforeRead)
	t.Run("reject_drift", TestAgentOutputSchemaRejectsUnknownAndDrift)
	t.Run("registry", TestAgentOutputSchemaRegistryHandlers)
	t.Run("default_list", TestListAgentsHandlerDefaultsToActiveCompactSnapshots)
	t.Run("states", TestListAgentsHandlerFiltersCommaSeparatedState)
	t.Run("scope", TestListAgentsHandlerTrustedScopeCWDOverridesArgumentCWD)
	t.Run("object", TestHandleListAgentsAlwaysReturnsObject)
}

// guardAgentOutputScalar 直接从 Go 类型检查 schema 标量类型，防止可选字段类型漂移漏检。
func guardAgentOutputScalar(t *testing.T, typ reflect.Type, schema Schema) {
	t.Helper()
	var want string
	switch typ.Kind() {
	case reflect.String:
		want = "string"
	case reflect.Bool:
		want = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		want = "integer"
	default:
		t.Fatalf("unhandled DTO type %s", typ)
	}
	if schema["type"] != want {
		t.Errorf("%s schema type=%v want=%s", typ, schema["type"], want)
	}
}
