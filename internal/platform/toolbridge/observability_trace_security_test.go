package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

func TestHandleToolCallRecordsSecurityAuditMetadata(t *testing.T) {
	tracer := newToolbridgeTraceService(t)
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, _ string, _ any, result any) error {
		resp := result.(*peerToolCallResponse)
		resp.Content = []peerToolCallContent{{Type: "text", Text: "ok"}}
		return nil
	}}}
	handler, _ := newHandlerForTest(peer)
	handler.tracer = tracer

	ctx := observability.ContextWithSpan(context.Background(), "trace-tool-security", "turn-span", "root-span")
	_, err := handler.HandleToolCall(ctx, contract.ToolCallRawMessage{
		ID:     json.RawMessage(`"call-a"`),
		Method: "item/tool/call",
		Params: mustRawJSON(t, map[string]any{
			"name":            "file",
			"arguments":       map[string]any{"api_key": "sk-secret", "path": "safe.txt"},
			"agentId":         "agent-a",
			"threadId":        "thread-a",
			"turnId":          "turn-a",
			"callId":          "call-a",
			"clientKind":      dto.ClientKindLSP,
			"_workspaceRoots": []string{"/workspace"},
		}),
	})
	if err != nil {
		t.Fatalf("HandleToolCall returned error: %v", err)
	}

	events := tracer.Query(context.Background(), observability.Query{TraceID: "trace-tool-security"}).Events
	begin := requireTraceMethod(t, events, "tool.call.begin")
	if begin.Metadata["source_actor"] != "agent-a" {
		t.Fatalf("source_actor = %v, want agent-a", begin.Metadata["source_actor"])
	}
	if begin.Metadata["target_peer"] != dto.ClientKindLSP {
		t.Fatalf("target_peer = %v, want %s", begin.Metadata["target_peer"], dto.ClientKindLSP)
	}
	if begin.Metadata["tool_name"] != "file" {
		t.Fatalf("tool_name = %v, want file", begin.Metadata["tool_name"])
	}
	if begin.Metadata["trace_id"] != "trace-tool-security" {
		t.Fatalf("trace_id metadata = %v, want trace-tool-security", begin.Metadata["trace_id"])
	}
	if begin.Metadata["redaction_policy"] != "metadata_only" {
		t.Fatalf("redaction_policy = %v, want metadata_only", begin.Metadata["redaction_policy"])
	}
	if !strings.Contains(mustTraceJSON(t, []observability.TraceEvent{begin}), "api_key") {
		t.Fatalf("redacted argument keys missing api_key: %+v", begin.Metadata)
	}
	assertTraceDoesNotLeak(t, events, "sk-secret")
}
