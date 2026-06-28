package e2e

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

func mustMarshalE2E(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal e2e payload: %v", err)
	}
	return raw
}

func assertNoLifecycleE2EJSONFields(t *testing.T, scope string, raw []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("unmarshal %s: %v", scope, err)
	}
	assertNoLifecycleE2EJSONValue(t, scope, value)
}

func assertNoLifecycleE2EJSONValue(t *testing.T, scope string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isLifecycleE2EJSONField(key) {
				t.Fatalf("%s unexpectedly exposes lifecycle field %q in %#v", scope, key, typed)
			}
			if skipLifecycleE2EJSONChild(key) {
				continue
			}
			assertNoLifecycleE2EJSONValue(t, scope+"."+key, child)
		}
	case []any:
		for _, child := range typed {
			assertNoLifecycleE2EJSONValue(t, scope+"[]", child)
		}
	}
}

func isLifecycleE2EJSONField(key string) bool {
	switch key {
	case "lifecycle", "lifecycleState", "state", "reason", "source", "updatedBy",
		"createdAt", "updatedAt", "workspaceRoot", "serverName", "toolName":
		return true
	default:
		return false
	}
}

func skipLifecycleE2EJSONChild(key string) bool {
	switch key {
	case "inputSchema", "outputSchema", "env", "headers":
		return true
	default:
		return false
	}
}

func codexToolInstance(clientKind string, peer *codexToolBridgePeer) *mcpcontrol.ToolInstance {
	return &mcpcontrol.ToolInstance{ClientKind: clientKind, Status: mcpdto.StatusActive, Peer: peer}
}

type codexMemoryReaderStub struct {
	enabled      bool
	toolsEnabled bool
}

func (s *codexMemoryReaderStub) ReadAgentMemory(_ context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
	return contract.MemoryReadResult{Entry: &contract.MemoryEntry{Name: req.Name, Type: req.Type, Content: "memory content"}, SourcePath: "feedback/read.md", IndexHit: true}, nil
}

func (s *codexMemoryReaderStub) MemoryReadEnabled() bool {
	return s == nil || s.enabled
}

func (s *codexMemoryReaderStub) MemoryReadToolsEnabled() bool {
	return s == nil || s.toolsEnabled
}

func codexListToolsPeer(tools []mcpdto.MCPTool) *codexToolBridgePeer {
	return &codexToolBridgePeer{tools: tools}
}

type codexToolBridgePeer struct {
	tools []mcpdto.MCPTool
}

func (p *codexToolBridgePeer) Notify(context.Context, string, any) error { return nil }

func (p *codexToolBridgePeer) Callback(_ context.Context, method string, _ any, result any) error {
	if method != "tools/list" {
		return nil
	}
	raw, err := json.Marshal(map[string]any{"tools": p.tools})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func (p *codexToolBridgePeer) Close() error { return nil }
