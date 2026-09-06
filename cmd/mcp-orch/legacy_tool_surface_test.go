package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestLegacyToolProviderHidesAndRejectsEveryDirectPeerTool(t *testing.T) {
	provider := legacyToolProvider{base: registryToolProvider{registry: newRegistry(newRegistryParamsForTest())}}
	listed, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tools.LegacyDirectPeerToolNames() {
		if hasMCPToolName(listed, name) {
			t.Fatalf("legacy tools/list exposed direct-peer tool %q", name)
		}
		_, err := provider.CallTool(context.Background(), name, json.RawMessage(`{}`))
		if !errors.Is(err, errLegacyDirectPeerUnavailable) {
			t.Fatalf("legacy CallTool(%q) error=%v, want unavailable sentinel", name, err)
		}
	}
}

func TestHandleScopedToolsCallRejectsDirectPeerBeforeScopeBinding(t *testing.T) {
	for _, name := range tools.LegacyDirectPeerToolNames() {
		t.Run(name, func(t *testing.T) {
			called := false
			params := json.RawMessage(`{"name":"` + name + `","arguments":{"sender_agent_id":"forged"},"_agentId":"forged-agent","_cwd":"/forged"}`)
			result, err := handleScopedToolsCallWithCaller(context.Background(), "orch", params,
				func(context.Context, string, json.RawMessage) (any, error) {
					called = true
					return map[string]any{"success": true}, nil
				})
			if err != nil {
				t.Fatalf("handleScopedToolsCallWithCaller() error=%v", err)
			}
			if called {
				t.Fatal("direct-peer callback invoked without transport-owned attribution")
			}
			envelope := decodeScopedToolEnvelope(t, result)
			if envelope.Success || !strings.Contains(envelope.Error, "legacy direct-peer capability unavailable") {
				t.Fatalf("envelope=%+v, want fail-closed legacy capability error", envelope)
			}
		})
	}
}

func TestLegacyToolProviderKeepsNonDirectPeerTools(t *testing.T) {
	provider := legacyToolProvider{base: registryToolProvider{registry: newRegistry(newRegistryParamsForTest())}}
	listed, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasMCPToolName(listed, "list_agents") {
		t.Fatal("legacy tools/list unexpectedly hid non-direct-peer tool")
	}
}

var _ common.ToolProvider = legacyToolProvider{}
