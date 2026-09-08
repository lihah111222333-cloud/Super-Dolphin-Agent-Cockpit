package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestStandaloneHTTPToolSurfaceHidesTaskDirectPeerWithoutAttribution(t *testing.T) {
	provider := standaloneHTTPToolProvider{base: registryToolProvider{registry: newRegistry(newRegistryParamsForTest())}}
	listed, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tools.TaskScopedDirectPeerToolNames() {
		if hasMCPToolName(listed, name) {
			t.Fatalf("operator standalone tools/list exposed task-scoped direct-peer tool %q", name)
		}
		if _, err := provider.CallTool(context.Background(), name, json.RawMessage(`{}`)); !errors.Is(err, errStandaloneTaskCapabilityUnavailable) {
			t.Fatalf("operator standalone CallTool(%q) error = %v, want fail-closed capability error", name, err)
		}
	}
	if !hasMCPToolName(listed, "manage_direct_peer") {
		t.Fatal("operator standalone tools/list hid operator-only direct-peer management")
	}
}

func TestStandaloneHTTPToolSurfacePreservesTaskDirectPeerWithBoundAttribution(t *testing.T) {
	provider := standaloneHTTPToolProvider{base: registryToolProvider{registry: newRegistry(newRegistryParamsForTest())}}
	ctx := standaloneTaskAttributionContext(t)
	listed, err := provider.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tools.TaskScopedDirectPeerToolNames() {
		if !hasMCPToolName(listed, name) {
			t.Fatalf("task-attributed standalone tools/list hid %q", name)
		}
	}
}

func hasMCPToolName(listed []common.MCPTool, want string) bool {
	for _, tool := range listed {
		if tool.Name == want {
			return true
		}
	}
	return false
}

func standaloneTaskAttributionContext(t *testing.T) context.Context {
	t.Helper()
	principal, err := common.NewCallerPrincipal("agent/source-1", "scope/task", "task_http_session_v1", "auth/task-session", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := common.NewCallerAttribution("agent/source-1", common.ExecutionFence{
		AgentID: "agent/source-1", ReservationID: "reservation/source-1", ReservationGeneration: 3,
		IncarnationID: "incarnation/source-1", IncarnationGeneration: 5, BindingVersion: 7,
		BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "source-fence/provider-1",
	}, common.IssuerBinding{
		Kind: common.CallerAttributionIssuerProviderBridge, BindingRef: "issuer/provider-1", Generation: 11,
		Digest: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := common.BindCallerPrincipal(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = common.BindCallerAttribution(ctx, attribution)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// TestStandaloneLaunchModelProjection 验证 task/operator 投影公开必填模型，且共享 peer schema 未被污染。
func TestStandaloneLaunchModelProjection(t *testing.T) {
	base := registryToolProvider{registry: newRegistry(newRegistryParamsForTest())}
	before, err := base.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{context.Background(), standaloneTaskAttributionContext(t)} {
		listed, err := (standaloneHTTPToolProvider{base: base}).ListTools(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertLaunchModelRequired(t, listed, true)
	}
	after, err := base.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertLaunchModelRequired(t, after, false)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("standalone projection changed shared peer catalog")
	}
}

// assertLaunchModelRequired 检查真实公开 schema 的模式约束与对应说明。
func assertLaunchModelRequired(t *testing.T, listed []common.MCPTool, want bool) {
	t.Helper()
	for _, tool := range listed {
		if tool.Name != "launch_agent" {
			continue
		}
		var schema struct{ Required []string }
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(schema.Required, "model") != want {
			t.Fatalf("launch required=%v want model=%v", schema.Required, want)
		}
		if want && !strings.Contains(tool.Description, "Standalone requires explicit model") {
			t.Fatal("standalone model guidance omitted")
		}
		return
	}
	t.Fatal("launch_agent missing")
}

// TestStandaloneLaunchModelFailsBeforeBase 验证缺失模型不能进入基础 launch handler 的读写端口。
func TestStandaloneLaunchModelFailsBeforeBase(t *testing.T) {
	calls := 0
	probe := errors.New("underlying handler reached")
	params := newRegistryParamsForTest()
	params.AgentLaunch = &golden.OrchestrationStub{ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) { calls++; return nil, probe }}
	provider := standaloneHTTPToolProvider{base: registryToolProvider{registry: newRegistry(params)}}
	for _, model := range []string{"", `,"model":null`, `,"model":""`, `,"model":"  "`, `,"model":12`} {
		input := json.RawMessage(`{"name":"review","cwd":"/tmp","agent_id":"a"` + model + `}`)
		if _, err := provider.CallTool(context.Background(), "launch_agent", input); err == nil || errors.Is(err, probe) {
			t.Fatalf("invalid model reached base: %s err=%v", input, err)
		}
	}
	if _, err := provider.CallTool(context.Background(), " launch_agent ", json.RawMessage(`{"name":"review","cwd":"/tmp","agent_id":"a"}`)); err == nil {
		t.Fatal("space-padded launch bypassed model validation")
	}
	if calls != 0 {
		t.Fatalf("invalid model reached base %d times", calls)
	}
	_, err := provider.CallTool(context.Background(), "launch_agent", json.RawMessage(`{"name":"review","cwd":"/tmp","agent_id":"a","model":"configured-model"}`))
	if !errors.Is(err, probe) || calls != 1 {
		t.Fatalf("valid model did not reach base once: calls=%d err=%v", calls, err)
	}
}
