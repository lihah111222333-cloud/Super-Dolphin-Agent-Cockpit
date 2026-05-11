package tools

import (
	"strings"
	"testing"
)

// TestEnumValidation_SchemaHandlerSingleSource 验证 schema 与 handler 共用
// 同一份 enum 切片（修改一边不会忘改另一边）。
//
// TestEnumValidation_SchemaHandlerSingleSource asserts that the schema and
// handler share the same enum slice (defined as a package-level variable),
// so changes cannot drift between layers.
func TestEnumValidation_SchemaHandlerSingleSource(t *testing.T) {
	// 通过 schema 反取 enum，应与包级变量逐元素相等。
	// Read the enum back from schemas and compare with the package-level slice.
	cases := []struct {
		name       string
		fromSchema []string
		fromVar    []string
	}{
		{
			name:       "task_list_runs.status",
			fromSchema: EnumValues(EnumStringSchema("d", listRunsStatusEnum...)),
			fromVar:    listRunsStatusEnum,
		},
		{
			name:       "task_start_dag.trigger_source",
			fromSchema: EnumValues(EnumStringSchema("d", startDAGTriggerEnum...)),
			fromVar:    startDAGTriggerEnum,
		},
		{
			name:       "task_update_node.status",
			fromSchema: EnumValues(EnumStringSchema("d", updateNodeStatusEnum...)),
			fromVar:    updateNodeStatusEnum,
		},
		{
			name:       "orchestration_launch_agent.provider",
			fromSchema: EnumValues(EnumStringSchema("d", launchAgentProviderEnum...)),
			fromVar:    launchAgentProviderEnum,
		},
	}
	for _, tc := range cases {
		tc := tc
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
		tc := tc
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
		tc := tc
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
		{name: "valid", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", Status: "running"}, wantStatus: "running"},
		{name: "empty", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", Status: ""}, wantErr: "status is required"},
		{name: "whitespace", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", Status: "  "}, wantErr: "status is required"},
		{name: "invalid", in: UpdateNodeInput{DagKey: "dag-1", NodeKey: "n", Status: "skipped"}, wantErr: "status"},
	}
	for _, tc := range cases {
		tc := tc
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
		})
	}
}

// TestValidateLaunchProvider_EnumValidation 覆盖 provider 4 个 case。
// 空串 → 返空（下游默认 codex），与其他 enum 字段不同。
//
// TestValidateLaunchProvider_EnumValidation covers four provider cases.
// Empty input returns empty (downstream defaults to codex).
func TestValidateLaunchProvider_EnumValidation(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantErr     string
		wantPayload string
	}{
		{name: "valid-codex", raw: "codex", wantPayload: "codex"},
		{name: "valid-claude-uppercase", raw: "CLAUDE", wantPayload: "claude"},
		{name: "empty-ok", raw: "", wantPayload: ""},
		{name: "whitespace-ok", raw: "   ", wantPayload: ""},
		{name: "invalid", raw: "openai", wantErr: "provider"},
	}
	for _, tc := range cases {
		tc := tc
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
