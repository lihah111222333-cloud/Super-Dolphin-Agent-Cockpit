package toolbridge

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

func TestListToolsForCodex_IncludesHostMemoryReadAndWrite(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	writer := &stubAgentMemoryWriter{}
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindOrch: {listToolsPeer(nil, nil)},
			dto.ClientKindLSP:  {listToolsPeer(nil, nil)},
		}},
		hostTools: NewCompositeHostToolRegistry(NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true}), NewMemoryWriteHostToolRegistry(writer, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: true})),
	}
	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = true
	}
	if !got[ToolNameMemoryRead] || !got[ToolNameMemoryWrite] {
		t.Fatalf("dynamic tools names = %#v, want memory_read and memory_write", got)
	}
}

func TestProvideHostToolRegistryForCodexOmitsSkillReadSectionButKeepsMemoryTools(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	writer := &stubAgentMemoryWriter{}
	host := ProvideHostToolRegistryForTesting(hostToolRegistryIn{
		Reader: reader,
		Writer: writer,
	})
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameReadSection, Description: "peer skill read"}}, nil)},
			dto.ClientKindLSP:  {listToolsPeer(nil, nil)},
		}},
		hostTools: host,
	}

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if containsDynamicToolName(tools, ToolNameReadSection) {
		t.Fatalf("dynamic tools = %+v, must not include skill_read_section", tools)
	}
	if !containsDynamicToolName(tools, ToolNameMemoryRead) || !containsDynamicToolName(tools, ToolNameMemoryWrite) {
		t.Fatalf("dynamic tools = %+v, want memory_read and memory_write preserved", tools)
	}
}

func TestListToolsForCodex_FiltersRemovedSkillToolsFromHostAndPeer(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameLegacySkillReadResource, Description: "peer removed"}, {Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, nil)},
	}}
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: ToolNameLegacySkillExpandBody, Description: "host removed"}, {Name: ToolNameMemoryRead, Description: "host memory"}}}
	h := &Handler{registry: registry, hostTools: host}
	attachActiveLifecycleForTools(h, map[string][]string{
		dto.ClientKindOrch: {"orchestration_launch_agent"},
	})

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	for _, removed := range []string{ToolNameLegacySkillExpandBody, ToolNameLegacySkillReadResource, ToolNameReadSection} {
		if containsDynamicToolName(tools, removed) {
			t.Fatalf("dynamic tools = %+v, must not include removed skill tool %s", tools, removed)
		}
	}
	if !containsDynamicToolName(tools, ToolNameMemoryRead) || !containsDynamicToolName(tools, "orchestration_launch_agent") {
		t.Fatalf("dynamic tools = %+v, want memory and peer tools preserved", tools)
	}
}

func TestCodexSkillReadSectionWithoutHostDoesNotFallbackToPeer(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for removed skill tool %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameReadSection, Arguments: mustMarshal(t, map[string]any{"name": "tdd", "anchor": "overview"}), AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want removed host tool error", got)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want no peer fallback", registry.gotKinds)
	}
}

func TestCodexRemovedLegacySkillToolsDoNotFallbackToPeer(t *testing.T) {
	for _, toolName := range []string{"skill_expand_body", "skill_read_resource"} {
		t.Run(toolName, func(t *testing.T) {
			registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
				dto.ClientKindOrch: {
					{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
						t.Fatalf("peer callback called for removed skill tool %s with params %#v", method, params)
						return nil
					}}},
				},
			}}
			h := &Handler{registry: registry}

			got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: toolName, Arguments: mustMarshal(t, map[string]any{"name": "tdd"}), AgentID: "agent-1"})
			if err != nil {
				t.Fatalf("routeToolCall() error = %v", err)
			}
			if got == nil || got.Success {
				t.Fatalf("result = %+v, want removed host tool error", got)
			}
			if len(registry.gotKinds) != 0 {
				t.Fatalf("FindActiveByKind() kinds = %#v, want no peer fallback", registry.gotKinds)
			}
		})
	}
}

func TestListToolsForCodex_FiltersPeerMemoryReadWhenReaderUnavailable(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, nil)},
	}}
	h := &Handler{registry: registry}
	attachActiveLifecycleForTools(h, map[string][]string{
		dto.ClientKindOrch: {"orchestration_launch_agent"},
	})
	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if containsDynamicToolName(tools, ToolNameMemoryRead) {
		t.Fatalf("dynamic tools = %+v, must filter peer memory_read when host reader is unavailable", tools)
	}
	if !containsDynamicToolName(tools, "orchestration_launch_agent") {
		t.Fatalf("dynamic tools = %+v, want non-memory peer tool preserved", tools)
	}
}

func TestListToolsForCodex_FiltersPeerMemoryReadWhenMemoryReadToolsDisabled(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, nil)},
	}}
	h := &Handler{registry: registry, hostTools: host}
	attachActiveLifecycleForTools(h, map[string][]string{
		dto.ClientKindOrch: {"orchestration_launch_agent"},
	})
	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if containsDynamicToolName(tools, ToolNameMemoryRead) {
		t.Fatalf("dynamic tools = %+v, must filter peer memory_read when memory_read tools are disabled", tools)
	}
	if !containsDynamicToolName(tools, "orchestration_launch_agent") {
		t.Fatalf("dynamic tools = %+v, want non-memory peer tool preserved", tools)
	}
}

func containsDynamicToolName(tools []contract.DynamicToolSchema, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestListToolsForCodex_HostMemoryReadPreventsPeerMemoryReadUse(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, nil)},
	}}
	h := &Handler{registry: registry, hostTools: host}
	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	count := 0
	var description string
	for _, tool := range tools {
		if tool.Name == ToolNameMemoryRead {
			count++
			description = tool.Description
		}
	}
	if count != 1 || description == "peer memory" {
		t.Fatalf("memory_read count=%d description=%q, want host only", count, description)
	}
}

func TestCodexMemoryReadCallToolsDisabledReturnsStableEnvelopeWithoutPeerFallback(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "daily"}), AgentID: "agent-1", CWD: t.TempDir()})

	assertHostRouteFailFast(t, got, err)
	if reader.calls != 0 || len(registry.gotKinds) != 0 {
		t.Fatalf("reader.calls=%d registry kinds=%#v, want no reader or peer", reader.calls, registry.gotKinds)
	}
}

func TestCodexMemoryReadCallFeatureDisabledReturnsStableEnvelopeWithoutPeerFallback(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: false, ToolsEnabled: true})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "daily"}), AgentID: "agent-1"})

	assertHostRouteFailFast(t, got, err)
	if reader.calls != 0 || len(registry.gotKinds) != 0 {
		t.Fatalf("reader.calls=%d registry kinds=%#v, want no reader or peer", reader.calls, registry.gotKinds)
	}
}

func TestCodexMemoryReadCallNilReaderReturnsStableEnvelopeWithoutPeerFallback(t *testing.T) {
	host := &MemoryReadHostToolRegistry{opts: MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "daily"}), AgentID: "agent-1"})

	assertHostRouteFailFast(t, got, err)
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestCodexMemoryReadCallReaderErrorPreservesCodeWithoutPeerFallback(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true, err: contract.NewAgentMemoryError("not_visible", errors.New("memory not visible"))}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "private"}), AgentID: "agent-1", CWD: t.TempDir()})

	assertHostRouteFailFast(t, got, err)
	if reader.calls != 1 || len(registry.gotKinds) != 0 {
		t.Fatalf("reader.calls=%d registry kinds=%#v, want one reader call and no peer", reader.calls, registry.gotKinds)
	}
}

func TestCodexMemoryReadCallUsesHostDirect(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry, hostTools: host}
	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "daily", "scope": "user"}), AgentID: "agent-1", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || !got.Success || reader.calls != 1 {
		t.Fatalf("result=%+v reader.calls=%d, want host success", got, reader.calls)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestCodexMemoryWriteCallWithoutHostDoesNotFallbackToPeer(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry}
	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryWrite, Arguments: mustMarshal(t, map[string]any{"name": "daily"}), AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want host tool error", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["tool"] != ToolNameMemoryWrite || envelope["code"] != "writer_unavailable" {
		t.Fatalf("envelope = %#v, want writer_unavailable", envelope)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestCodexMemoryWriteCallToolsDisabledReturnsStableEnvelope(t *testing.T) {
	writer := &stubAgentMemoryWriter{}
	host := NewMemoryWriteHostToolRegistry(writer, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: false})
	h := &Handler{registry: &stubKindRegistry{}, hostTools: host}
	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryWrite, Arguments: mustMarshal(t, map[string]any{"name": "daily"}), AgentID: "agent-1"})
	assertHostRouteFailFast(t, got, err)
}

func assertHostRouteFailFast(t *testing.T, got *ToolCallResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("routeToolCall() error = %v, want structured host tool result", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want structured failure result", got)
	}
}

func TestCodexMemoryReadCallWithoutRegistryReturnsStableEnvelope(t *testing.T) {
	h := &Handler{}
	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "daily"}), AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v, want stable tool result", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want host tool error", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["tool"] != ToolNameMemoryRead || envelope["code"] != "reader_unavailable" {
		t.Fatalf("envelope = %#v, want reader_unavailable", envelope)
	}
}

func TestCodexMemoryReadCallWithoutHostDoesNotFallbackToPeer(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry}
	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "daily"}), AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want host tool error", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["tool"] != ToolNameMemoryRead || envelope["code"] != "reader_unavailable" {
		t.Fatalf("envelope = %#v, want reader_unavailable", envelope)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}
