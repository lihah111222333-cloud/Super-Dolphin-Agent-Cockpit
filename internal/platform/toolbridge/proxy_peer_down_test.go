package toolbridge

import (
	"errors"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

func TestProxyOrchToolsListPeerDownReturnsBlockingDegradedEnvelope(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: testHostToolName, Description: "host echo"}}}
	h := &Handler{
		hostTools: host,
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch peer down"))},
		}},
		proxyAuthToken: newProxyAuthToken(),
	}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)

	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v, want degraded result envelope", got.Error)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	requireProxyToolsListBoolField(t, result, "degraded")
	requireProxyToolsListBoolField(t, result, "blocks_provider_start")
	requireProxyToolsListBoolField(t, result, "blocks_turn")
	if result[observability.ErrorCodeField] != "peer_down" || result[observability.PeerIDField] != dto.ClientKindOrch {
		t.Fatalf("result = %#v, want peer-down standard fields", result)
	}
	preview, _ := result[observability.ErrorPreviewField].(string)
	if !strings.Contains(preview, "orch peer down") {
		t.Fatalf("error preview = %q, want peer-down detail", preview)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one host tool for legacy clients", result["tools"])
	}
}

func requireProxyToolsListBoolField(t *testing.T, result map[string]any, field string) {
	t.Helper()
	if result[field] != true {
		t.Fatalf("result = %#v, want %s=true", result, field)
	}
}
