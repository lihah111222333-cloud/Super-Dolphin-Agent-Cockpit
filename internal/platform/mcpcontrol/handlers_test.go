package mcpcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/creachadair/jrpc2"
)

type stubAgentContextSource struct {
	snapshot *contract.AgentSnapshot
	err      error
	gotID    string
}

func (s *stubAgentContextSource) GetAgentSnapshot(agentID string) (*contract.AgentSnapshot, error) {
	s.gotID = agentID
	if s.err != nil {
		return nil, s.err
	}
	if s.snapshot == nil {
		return nil, errors.New("missing snapshot")
	}
	cloned := *s.snapshot
	return &cloned, nil
}

func TestRegistryContextProvider_UsesRequestedAgentSnapshotForRuntimeScope(t *testing.T) {
	source := &stubAgentContextSource{
		snapshot: &contract.AgentSnapshot{
			ID:       "agent-42",
			ThreadID: "thread-42",
			PID:      4242,
			State:    "running",
		},
	}
	resp, err := (registryContextProvider{agents: source}).GetContext(context.Background(), &ToolInstance{
		AgentID:    "shared",
		BinaryName: "mcp-orch",
		ClientKind: "orch",
		PeerKind:   dto.PeerKindTool,
		PID:        99,
		Status:     dto.StatusActive,
	}, dto.ContextRequest{
		AgentID: "agent-42",
		Scope:   dto.ScopeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := payload["agent_id"]; got != "agent-42" {
		t.Fatalf("payload.agent_id = %#v, want agent-42", got)
	}
	if got := payload["pid"]; got != float64(4242) {
		t.Fatalf("payload.pid = %#v, want 4242", got)
	}
	if got := payload["status"]; got != "running" {
		t.Fatalf("payload.status = %#v, want running", got)
	}
	if source.gotID != "agent-42" {
		t.Fatalf("GetAgentSnapshot() agent_id = %q, want agent-42", source.gotID)
	}
}

func TestRegistryContextProvider_UsesLeaseScopedAgentIDWhenHintMissing(t *testing.T) {
	resp, err := (registryContextProvider{}).GetContext(context.Background(), &ToolInstance{
		AgentID:    "lease-agent",
		BinaryName: "mcp-orch",
		ClientKind: "orch",
		PeerKind:   dto.PeerKindTool,
		PID:        99,
		Status:     dto.StatusActive,
	}, dto.ContextRequest{
		Scope: dto.ScopeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := payload["agent_id"]; got != "lease-agent" {
		t.Fatalf("payload.agent_id = %#v, want lease-agent", got)
	}
}

func TestRegistryContextProvider_UsesRequestedAgentSnapshotForThreadBinding(t *testing.T) {
	resp, err := (registryContextProvider{agents: &stubAgentContextSource{
		snapshot: &contract.AgentSnapshot{
			ID:       "agent-42",
			ThreadID: "thread-42",
			State:    "running",
		},
	}}).GetContext(context.Background(), &ToolInstance{
		AgentID:  "shared",
		ThreadID: "thread-shared",
		Lease: dto.LeaseKey{
			InstanceID: "instance-1",
			Generation: 7,
		},
	}, dto.ContextRequest{
		AgentID: "agent-42",
		Scope:   dto.ScopeThreadBinding,
	})
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := payload["agent_id"]; got != "agent-42" {
		t.Fatalf("payload.agent_id = %#v, want agent-42", got)
	}
	if got := payload["thread_id"]; got != "thread-42" {
		t.Fatalf("payload.thread_id = %#v, want thread-42", got)
	}
	if got := payload["instance_id"]; got != "instance-1" {
		t.Fatalf("payload.instance_id = %#v, want instance-1", got)
	}
}

func TestRegistryContextProvider_ReturnsAgentNotFoundWhenSourceMissing(t *testing.T) {
	_, err := (registryContextProvider{}).GetContext(context.Background(), &ToolInstance{
		AgentID: "shared",
	}, dto.ContextRequest{
		AgentID: "agent-42",
		Scope:   dto.ScopeAgentRuntime,
	})
	if err == nil || !strings.Contains(err.Error(), "agent not found") {
		t.Fatalf("GetContext() error = %v, want agent not found", err)
	}
}

func TestDefaultLogSinkRedactsPeerFields(t *testing.T) {
	var buf bytes.Buffer
	sink := defaultLogSink{logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}

	err := sink.HandleLog(context.Background(), &ToolInstance{
		Lease:      dto.LeaseKey{InstanceID: "peer-1", Generation: 2},
		BinaryName: "mcp-lsp",
		ClientKind: "lsp",
	}, dto.LogNotify{
		Level:   "INFO",
		Message: "peer emitted diagnostic",
		Fields: map[string]any{
			"token":  "sk-abcdefghijklmnopqrstuvwxyz",
			"detail": "plain diagnostic",
		},
	})
	if err != nil {
		t.Fatalf("HandleLog() error = %v", err)
	}
	raw := buf.String()
	if strings.Contains(raw, "sk-") {
		t.Fatalf("mcp control log leaked secret: %s", raw)
	}
	if !strings.Contains(raw, `"token":"[REDACTED]"`) || !strings.Contains(raw, "plain diagnostic") {
		t.Fatalf("mcp control log = %s, want redacted token and safe detail", raw)
	}
}

func TestValidateHookSubscribeRequest_ReturnsInvalidParams(t *testing.T) {
	err := validateHookSubscribeRequest(dto.HookSubscribeRequest{})
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("validateHookSubscribeRequest() error = %T, want *jrpc2.Error", err)
	}
	if got := int(rpcErr.Code); got != dto.ErrCodeInvalidParams {
		t.Fatalf("validateHookSubscribeRequest() code = %d, want %d", got, dto.ErrCodeInvalidParams)
	}

}

func TestValidateHookResolveRequest_ReturnsInvalidParams(t *testing.T) {
	err := validateHookResolveRequest(dto.HookResolveRequest{})
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("validateHookResolveRequest() error = %T, want *jrpc2.Error", err)
	}
	if got := int(rpcErr.Code); got != dto.ErrCodeInvalidParams {
		t.Fatalf("validateHookResolveRequest() code = %d, want %d", got, dto.ErrCodeInvalidParams)
	}

}

func TestMapHookHandlerError_StoreErrorReturnsInternal(t *testing.T) {
	err := mapHookHandlerError("resolve", platformdb.WrapStoreError(errors.New("boom"), "save", "hook_pending_review"))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("mapHookHandlerError() error = %T, want *jrpc2.Error", err)
	}
	if got := int(rpcErr.Code); got != dto.ErrCodeInternal {
		t.Fatalf("mapHookHandlerError() code = %d, want %d", got, dto.ErrCodeInternal)
	}
}
