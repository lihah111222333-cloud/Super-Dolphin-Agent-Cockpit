package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/room"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools/unifiedv2"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// TestProductionRegistryKeepsUnifiedV2AndRoomOutOfPublicSurface 锁定生产 registry
// 不注册 strict unified_v2/Room 工具，同时区分 legacy 同名 list/send 定义。
func TestProductionRegistryKeepsUnifiedV2AndRoomOutOfPublicSurface(t *testing.T) {
	registry := newRegistry(newRegistryParamsForTest())
	definitions, err := registry.List()
	if err != nil {
		t.Fatalf("production registry list: %v", err)
	}
	byName := make(map[string]tools.ToolDefinition, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
		if strings.HasPrefix(definition.Name, "room_") || strings.HasPrefix(definition.Name, "room.") {
			t.Fatalf("Room tool %q is publicly registered", definition.Name)
		}
		for _, capability := range definition.Metadata.Capabilities {
			if strings.HasPrefix(capability, "room.") {
				t.Fatalf("Room capability %q is publicly registered by tool %q", capability, definition.Name)
			}
		}
	}
	if _, ok := byName["wait_agents"]; ok {
		t.Fatal("strict unified_v2 wait_agents is publicly registered")
	}
	assertLegacyListDefinition(t, byName)
	assertLegacySendDefinition(t, byName)
}

// assertLegacyListDefinition 确认 list_agents 仍是 legacy schema，而非 strict route view。
func assertLegacyListDefinition(t *testing.T, byName map[string]tools.ToolDefinition) {
	t.Helper()
	definition, ok := byName["list_agents"]
	if !ok {
		t.Fatal("legacy list_agents is missing")
	}
	properties := schemaPropertiesForGuard(definition.InputSchema)
	if _, strictField := properties["view"]; strictField {
		t.Fatal("legacy list_agents was replaced by strict unified_v2 route schema")
	}
	if _, strictField := properties["cursor"]; strictField {
		t.Fatal("legacy list_agents exposes strict unified_v2 cursor field")
	}
}

// assertLegacySendDefinition 确认 send_message 仍保持 legacy target-by-agent schema。
func assertLegacySendDefinition(t *testing.T, byName map[string]tools.ToolDefinition) {
	t.Helper()
	definition, ok := byName["send_message"]
	if !ok {
		t.Fatal("legacy send_message is missing")
	}
	properties := schemaPropertiesForGuard(definition.InputSchema)
	if _, strictField := properties["target"]; strictField {
		t.Fatal("legacy send_message was replaced by strict unified_v2 grant target")
	}
	if _, legacyField := properties["agent_id"]; !legacyField {
		t.Fatal("legacy send_message no longer exposes its existing agent_id contract")
	}
}

// schemaPropertiesForGuard 读取封闭对象 schema 的属性名，避免把 schema 元字段误判为工具字段。
func schemaPropertiesForGuard(schema tools.Schema) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	return properties
}

// TestLegacyAndStandaloneProvidersDoNotExposeStrictUnifiedV2 验证 legacy 与 standalone
// provider 都不会把未注册的 strict wait 或未来 Room surface 重新投影出来。
func TestLegacyAndStandaloneProvidersDoNotExposeStrictUnifiedV2(t *testing.T) {
	base := registryToolProvider{registry: newRegistry(newRegistryParamsForTest())}
	providers := []struct {
		name string
		list func(context.Context) ([]mcpdto.MCPTool, error)
	}{
		{name: "legacy", list: func(ctx context.Context) ([]mcpdto.MCPTool, error) {
			return legacyToolProvider{base: base}.ListTools(ctx)
		}},
		{name: "standalone", list: func(ctx context.Context) ([]mcpdto.MCPTool, error) {
			return standaloneHTTPToolProvider{base: base}.ListTools(ctx)
		}},
	}
	for _, provider := range providers {
		t.Run(provider.name, func(t *testing.T) {
			listed, err := provider.list(context.Background())
			if err != nil {
				t.Fatalf("list tools: %v", err)
			}
			for _, tool := range listed {
				if tool.Name == "wait_agents" || strings.HasPrefix(tool.Name, "room_") || strings.HasPrefix(tool.Name, "room.") {
					t.Fatalf("disabled public tool %q was listed", tool.Name)
				}
			}
			for _, disabledCall := range []struct {
				name string
				args string
			}{
				{name: "wait_agents", args: `{}`},
				{name: "room_broadcast", args: `{}`},
				{name: "list_agents", args: `{"view":"route"}`},
				{name: "send_message", args: `{"target":{"kind":"grant","grant_ref":"grant"},"message":{"kind":"content.single","text":"x"},"delivery":{"kind":"new_turn","busy_policy":{"action":"fail"}},"idempotency_key":"key"}`},
			} {
				if _, err := func() (any, error) {
					if provider.name == "legacy" {
						return legacyToolProvider{base: base}.CallTool(context.Background(), disabledCall.name, []byte(disabledCall.args))
					}
					return standaloneHTTPToolProvider{base: base}.CallTool(context.Background(), disabledCall.name, []byte(disabledCall.args))
				}(); err == nil {
					t.Fatalf("strict/Room call %q unexpectedly succeeded", disabledCall.name)
				}
			}
		})
	}
}

// TestUnifiedV2AndRoomMissingDependenciesRemainDisabled 锁定候选构造器缺少 canonical
// owner 时的稳定 disabled 哨兵，不允许空依赖降级成可执行 handler。
func TestUnifiedV2AndRoomMissingDependenciesRemainDisabled(t *testing.T) {
	if _, err := unifiedv2.NewListAgents(nil, nil, nil); !errors.Is(err, unifiedv2.ErrListDisabled) {
		t.Fatalf("NewListAgents(nil) error = %v, want ErrListDisabled", err)
	}
	if _, err := unifiedv2.NewWaitAgents(nil, nil); !errors.Is(err, unifiedv2.ErrWaitAgentsDisabled) {
		t.Fatalf("NewWaitAgents(nil) error = %v, want ErrWaitAgentsDisabled", err)
	}
	if _, err := unifiedv2.NewSend(); !errors.Is(err, unifiedv2.ErrSendDisabled) {
		t.Fatalf("NewSend() error = %v, want ErrSendDisabled", err)
	}
	if _, err := unifiedv2.NewSendWithCoordinator(nil); !errors.Is(err, unifiedv2.ErrSendDisabled) {
		t.Fatalf("NewSendWithCoordinator(nil) error = %v, want ErrSendDisabled", err)
	}
	if err := room.RequireService(nil); !errors.Is(err, room.ErrCapabilityDisabled) {
		t.Fatalf("room.RequireService(nil) error = %v, want ErrCapabilityDisabled", err)
	}
	if room.NewCandidate().Enabled() {
		t.Fatal("Room candidate reported enabled without a durable owner")
	}
}
