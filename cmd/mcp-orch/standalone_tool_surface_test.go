package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
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
