package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestEnumValidation_SchemaHandlerSingleSource 验证 schema 与 handler 共用
// 同一份 Registry runtime state（修改一边不会忘改另一边）。
//
// TestEnumValidation_SchemaHandlerSingleSource asserts that the schema and
// handler share the same enum slice (owned by the Registry runtime state),
// so changes cannot drift between layers.
func TestEnumValidation_SchemaHandlerSingleSource(t *testing.T) {
	registry := NewRegistry(Dependencies{})
	if registry.initErr != nil {
		t.Fatalf("NewRegistry() initErr = %v", registry.initErr)
	}
	state := registry.runtimeState
	if state == nil {
		t.Fatal("NewRegistry() must initialize runtime state")
	}
	definitions := registry.tools
	// 通过 schema 反取 enum，应与同一 Registry 状态逐元素相等。
	// Read the enum back from schemas and compare with the Registry-owned slice.
	cases := []struct {
		name       string
		fromSchema []string
		fromVar    []string
	}{
		{
			name:       "task_list_dags.status",
			fromSchema: enumValuesFromToolSchema(t, definitions, "task_list_dags", "status"),
			fromVar:    state.listDAGsStatusEnum,
		},
		{
			name:       "task_list_runs.status",
			fromSchema: enumValuesFromToolSchema(t, definitions, "task_list_runs", "status"),
			fromVar:    state.listRunsStatusEnum,
		},
		{
			name:       "task_start_dag.trigger_source",
			fromSchema: enumValuesFromToolSchema(t, definitions, "task_start_dag", "trigger_source"),
			fromVar:    state.startDAGTriggerEnum,
		},
		{
			name:       "task_update_node.status",
			fromSchema: enumValuesFromToolSchema(t, definitions, "task_update_node", "status"),
			fromVar:    state.updateNodeStatusEnum,
		},
		{
			name:       "task_workflow_recovery_action.action",
			fromSchema: enumValuesFromToolSchema(t, definitions, "task_workflow_recovery_action", "action"),
			fromVar:    state.recoveryActionEnum,
		},
		{
			name:       "launch_agent.provider",
			fromSchema: enumValuesFromToolSchema(t, definitions, "launch_agent", "provider"),
			fromVar:    state.launchAgentProviderEnum,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.fromSchema) != len(tc.fromVar) {
				t.Fatalf("len mismatch: schema=%d var=%d", len(tc.fromSchema), len(tc.fromVar))
			}
			for i := range tc.fromVar {
				if tc.fromSchema[i] != tc.fromVar[i] {
					t.Fatalf("index %d: schema=%q var=%q", i, tc.fromSchema[i], tc.fromVar[i])
				}
			}
		})
	}
}

func TestNewRegistryOwnsIndependentRuntimeState(t *testing.T) {
	first := NewRegistry(Dependencies{})
	second := NewRegistry(Dependencies{})
	if first.runtimeState == nil || second.runtimeState == nil {
		t.Fatal("NewRegistry() must initialize its runtime state")
	}
	if first.runtimeState == second.runtimeState {
		t.Fatal("NewRegistry() must not share runtime state between registries")
	}
	first.runtimeState.agentIDReg.reservations = map[string]struct{}{"agent-a": {}}
	if len(second.runtimeState.agentIDReg.reservations) != 0 {
		t.Fatal("registry runtime state leaked launch ID reservation to another registry")
	}
}

func TestUpdateNodeStatusEnumMatchesStateMachineTargets(t *testing.T) {
	t.Parallel()
	want := nodeexec.LegalTransitionTargetStatusStrings()
	got := enumValuesFromToolSchema(t, taskToolDefinitions(ToolPorts{}), "task_update_node", "status")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("task_update_node.status enum = %v, want legal transition targets %v", got, want)
	}
	for _, status := range got {
		if status == "pending" {
			t.Fatalf("task_update_node.status must not expose unreachable pending: %v", got)
		}
	}
}

func enumValuesFromToolSchema(t *testing.T, defs []ToolDefinition, toolName, propertyName string) []string {
	t.Helper()
	for _, def := range defs {
		if def.Name != toolName {
			continue
		}
		props, ok := def.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema properties = %#v, want map[string]any", toolName, def.InputSchema["properties"])
		}
		raw, ok := props[propertyName].(map[string]any)
		if !ok {
			t.Fatalf("%s.%s schema = %#v, want map[string]any", toolName, propertyName, props[propertyName])
		}
		return EnumValues(Schema(raw))
	}
	t.Fatalf("tool %q not found", toolName)
	return nil
}

// TestListRunsRequestFromInput 覆盖 status 字段的 4 个 case：合法/非法/空/空白。
//
// TestListRunsRequestFromInput covers four cases for the optional status
// field: valid, invalid, empty, whitespace.
func TestListRunsRequestFromInput(t *testing.T) {
	cases := []struct {
		name       string
		in         ListRunsInput
		wantErr    string // empty → expect success
		wantStatus string
	}{
		{name: "valid", in: ListRunsInput{DagKey: "dag-1", Status: " running "}, wantStatus: "running"},
		{name: "empty-status-ok", in: ListRunsInput{DagKey: "dag-1"}, wantStatus: ""},
		{name: "whitespace-status-ok", in: ListRunsInput{DagKey: "dag-1", Status: "   "}, wantStatus: ""},
		{name: "invalid-status", in: ListRunsInput{DagKey: "dag-1", Status: "bogus"}, wantErr: "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := listRunsRequestFromInput(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if req.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", req.Status, tc.wantStatus)
			}
		})
	}
}

func TestListDAGsRequestFromInput(t *testing.T) {
	cases := []struct {
		name       string
		in         ListDAGsInput
		wantErr    string
		wantStatus string
	}{
		{name: "valid", in: ListDAGsInput{Status: " draft "}, wantStatus: "draft"},
		{name: "empty-status-ok", in: ListDAGsInput{}, wantStatus: ""},
		{name: "whitespace-status-ok", in: ListDAGsInput{Status: "   "}, wantStatus: ""},
		{name: "invalid-status", in: ListDAGsInput{Status: "bogus"}, wantErr: "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := listDAGsFilterFromInput(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if req.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", req.Status, tc.wantStatus)
			}
		})
	}
}

// TestStartDAGRequestFromInput 覆盖 trigger_source 4 个 case。
//
// TestStartDAGRequestFromInput covers four cases for trigger_source.
func TestStartDAGRequestFromInput(t *testing.T) {
	cases := []struct {
		name        string
		in          StartDAGInput
		wantErr     string
		wantTrigger string
	}{
		{name: "valid", in: StartDAGInput{DagKey: "dag-1", TriggerSource: "manual"}, wantTrigger: "manual"},
		{name: "empty-ok", in: StartDAGInput{DagKey: "dag-1"}, wantTrigger: ""},
		{name: "whitespace-ok", in: StartDAGInput{DagKey: "dag-1", TriggerSource: "  "}, wantTrigger: ""},
		{name: "invalid", in: StartDAGInput{DagKey: "dag-1", TriggerSource: "cron"}, wantErr: "trigger_source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := startDAGRequestFromInput(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if req.TriggerSource != tc.wantTrigger {
				t.Fatalf("trigger = %q, want %q", req.TriggerSource, tc.wantTrigger)
			}
		})
	}
}

func TestTerminateDAGRequestFromInput(t *testing.T) {
	req, err := terminateDAGRequestFromInput(TerminateDAGInput{
		DagKey: " dag-1 ",
		RunKey: " run-1 ",
		Reason: " user_requested ",
	})
	if err != nil {
		t.Fatalf("terminateDAGRequestFromInput() error = %v", err)
	}
	if req != (contract.TerminateDAGRequest{DagKey: "dag-1", RunKey: "run-1", Reason: "user_requested"}) {
		t.Fatalf("request = %#v", req)
	}

	_, err = terminateDAGRequestFromInput(TerminateDAGInput{DagKey: "dag-1"})
	if err == nil || !strings.Contains(err.Error(), "run_key") {
		t.Fatalf("missing run_key error = %v, want run_key", err)
	}
}

// TestUpdateNodeRequestFromInput_EnumValidation 覆盖 status 4 个 case
// （合法/非法/空/空白）。注意 status 必填，与 list_runs/start_dag 不同。
//
// TestUpdateNodeRequestFromInput_EnumValidation covers four status cases.
// Unlike list_runs/start_dag, status is required here.
func TestUpdateNodeRequestFromInput_EnumValidation(t *testing.T) {
	cases := []struct {
		name       string
		in         UpdateNodeInput
		wantErr    string
		wantStatus string
	}{
		{name: "valid running", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", RunID: 7, Status: "running"}, wantStatus: "running"},
		{name: "valid ready", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", RunID: 7, Status: "ready"}, wantStatus: "ready"},
		{name: "missing-run-id", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", Status: "running"}, wantErr: "run_id"},
		{name: "empty", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", RunID: 7, Status: ""}, wantErr: "status is required"},
		{name: "whitespace", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", RunID: 7, Status: "  "}, wantErr: "status is required"},
		{name: "pending unreachable", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", RunID: 7, Status: "pending"}, wantErr: "status"},
		{name: "invalid", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", RunID: 7, Status: "skipped"}, wantErr: "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := updateNodeRequestFromInput(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if req.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", req.Status, tc.wantStatus)
			}
			if req.RunID != tc.in.RunID {
				t.Fatalf("run_id = %d, want %d", req.RunID, tc.in.RunID)
			}
		})
	}
}

func TestApplyOpsRequestFromInputRejectsHybridAddNode(t *testing.T) {
	cases := []struct {
		name string
		in   ApplyOpsInput
	}{
		{
			name: "flat add_node",
			in: ApplyOpsInput{
				DagKey:      "dag-1",
				BaseVersion: 3,
				Action:      "add_node",
				NodeKey:     "review",
				Title:       "Review",
				NodeType:    "hybrid",
			},
		},
		{
			name: "raw add_node",
			in: ApplyOpsInput{
				DagKey:      "dag-1",
				BaseVersion: 3,
				Ops:         json.RawMessage(`[{"op":"add_node","node":{"node_key":"review","title":"Review","node_type":"hybrid"}}]`),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyOpsRequestFromInput(tc.in)
			if err == nil {
				t.Fatal("applyOpsRequestFromInput() error = nil, want hybrid node_type rejection")
			}
			for _, want := range []string{"node_type", "hybrid", "reserved"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want substring %q", err.Error(), want)
				}
			}
		})
	}
}

// TestValidateLaunchProvider_EnumValidation 覆盖 provider 4 个 case。
// 空串 → codex，与其他 enum 字段不同。
//
// TestValidateLaunchProvider_EnumValidation covers four provider cases.
// Empty input returns codex.
func TestValidateLaunchProvider_EnumValidation(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantErr     string
		wantPayload string
	}{
		{name: "valid-codex", raw: "codex", wantPayload: "codex"},
		{name: "valid-claude-uppercase", raw: "CLAUDE", wantPayload: "claude"},
		{name: "empty-ok", raw: "", wantPayload: "codex"},
		{name: "whitespace-ok", raw: "   ", wantPayload: "codex"},
		{name: "invalid", raw: "openai", wantErr: "provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateLaunchProvider(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.wantPayload {
				t.Fatalf("got = %q, want %q", got, tc.wantPayload)
			}
		})
	}
}
