package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// P4 Task 4: SkillHostTools struct and stubSkillService have been removed
// alongside the skill_expand_body / skill_read_resource host tools. The
// remaining tests cover generic dispatch (callHostTool / routeToolCall /
// dedup / ListToolsForCodex) using a stubHostToolRegistry plus the
// preserved SkillReadSectionRegistry.
//
// Tool-name string literals in this file ("skill_expand_body",
// "skill_read_resource") are intentional fixtures—labels only, with no
// backend wiring. They exercise the generic plumbing that the renamed
// skill_read_section pipeline still relies on.

type stubHostToolRegistry struct {
	hasToolName string
	tools       []dto.MCPTool
	result      any
	err         error
	calls       int
	last        HostToolCall
}

func (s *stubHostToolRegistry) ListHostTools() []dto.MCPTool {
	if s == nil {
		return nil
	}
	return append([]dto.MCPTool(nil), s.tools...)
}

func (s *stubHostToolRegistry) HasTool(name string) bool {
	return s != nil && name == s.hasToolName
}

func (s *stubHostToolRegistry) CallHostTool(_ context.Context, call HostToolCall) (any, error) {
	s.calls++
	s.last = call
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

type stubCWDResolver struct {
	cwd      string
	err      error
	agentID  string
	callSeen bool
}

func (s *stubCWDResolver) ResolveAgentCWD(_ context.Context, agentID string) (string, error) {
	s.callSeen = true
	s.agentID = agentID
	if s.err != nil {
		return "", s.err
	}
	return s.cwd, nil
}

type deniedHostToolError struct{}

func (deniedHostToolError) Error() string { return "skill expand approval denied: user denied" }

func (deniedHostToolError) SkillApprovalDenied() bool { return true }

func TestCallHostTool_ApprovalDeniedReturnsStructuredResult(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: "skill_expand_body",
		err:         deniedHostToolError{},
	}
	h := &Handler{hostTools: host}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: "skill_expand_body"})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if got.Success {
		t.Fatalf("Success = true, want false")
	}
	if envelope["kind"] != "approval_denied" || envelope["tool"] != "skill_expand_body" {
		t.Fatalf("structured denied envelope = %#v", envelope)
	}
	if envelope["error"] == "" {
		t.Fatalf("structured denied envelope missing error: %#v", envelope)
	}
}

func TestCallHostTool_ApprovalRequiredFallbackReturnsStructuredResult(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)
	approvalReq := contract.ApprovalRequest{
		CallID:       "call-1",
		ToolName:     "skill_expand_body",
		AgentID:      "agent-1",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		Reason:       "skill artifact requires approval",
		Kind:         "skill_artifact",
		SourceMethod: "skill_expand_body",
		Payload: map[string]any{
			"artifact_kind":    skillpkg.ArtifactKindBody,
			"artifact_locator": "SKILL.md",
			"callId":           "call-1",
			"toolName":         "skill_expand_body",
		},
	}
	host := &stubHostToolRegistry{
		hasToolName: "skill_expand_body",
		err:         skillpkg.SkillApprovalRequiredError{Request: approvalReq},
	}
	h := &Handler{hostTools: host}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: "skill_expand_body"})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if got.Success {
		t.Fatalf("Success = true, want false")
	}
	approval, ok := envelope["approval"].(map[string]any)
	if !ok {
		t.Fatalf("approval envelope type = %T, envelope = %#v", envelope["approval"], envelope)
	}
	if envelope["kind"] != "approval_required" || approval["callId"] != "call-1" || approval["toolName"] != "skill_expand_body" || approval["agentId"] != "agent-1" || approval["threadId"] != "thread-1" || approval["turnId"] != "turn-1" {
		t.Fatalf("structured approval_required envelope = %#v", envelope)
	}
	payload, ok := approval["payload"].(map[string]any)
	if !ok || payload["artifact_kind"] != skillpkg.ArtifactKindBody || payload["callId"] != "call-1" {
		t.Fatalf("approval payload = %#v", approval["payload"])
	}
	if snap := skillmetrics.Read(); snap.HostToolCallApprovalReqTotal != 1 {
		t.Fatalf("HostToolCallApprovalReqTotal = %d, want 1 (snapshot %+v)", snap.HostToolCallApprovalReqTotal, snap)
	}
}

func TestRouteToolCall_HostToolBypassesPeer_UsesResolvedCWD(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: "skill_expand_body",
		result:      map[string]any{"name": "foo", "content": "body"},
	}
	resolver := &stubCWDResolver{cwd: "/resolved/cwd"}
	registry := &stubRegistry{}
	h := &Handler{registry: registry, resolver: resolver, hostTools: host}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "skill_expand_body",
		Arguments: json.RawMessage(`{"name":"foo"}`),
		AgentID:   "agent-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		CallID:    "call-1",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || !got.Success {
		t.Fatalf("routeToolCall() result = %#v, want success", got)
	}
	if host.calls != 1 {
		t.Fatalf("host calls = %d, want 1", host.calls)
	}
	if host.last.CWD != "/resolved/cwd" || host.last.AgentID != "agent-1" || host.last.ThreadID != "thread-1" || host.last.TurnID != "turn-1" || host.last.CallID != "call-1" {
		t.Fatalf("host call metadata = %+v", host.last)
	}
	if !resolver.callSeen || resolver.agentID != "agent-1" {
		t.Fatalf("resolver callSeen=%v agentID=%q", resolver.callSeen, resolver.agentID)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("peer registry was consulted despite host-direct match: %+v", registry.gotKinds)
	}
}

func TestRouteToolCall_HostToolPrefersInjectedCWD(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: "skill_expand_body",
		result:      map[string]any{"name": "foo", "content": "body"},
	}
	resolver := &stubCWDResolver{cwd: "/stale/resolved/cwd"}
	h := &Handler{registry: &stubRegistry{}, resolver: resolver, hostTools: host}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "skill_expand_body",
		Arguments: json.RawMessage(`{"name":"foo"}`),
		AgentID:   "agent-1",
		CWD:       "/injected/cwd",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || !got.Success {
		t.Fatalf("routeToolCall() result = %#v, want success", got)
	}
	if host.last.CWD != "/injected/cwd" {
		t.Fatalf("host call cwd = %q, want /injected/cwd", host.last.CWD)
	}
	if resolver.callSeen {
		t.Fatalf("resolver should not be called when request carries trusted cwd")
	}
}

func TestCallHostTool_ObservabilityCountersAndLogs(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)
	host := &stubHostToolRegistry{
		hasToolName: "skill_expand_body",
		result:      map[string]any{"name": "foo", "content": "body"},
	}
	resolver := &stubCWDResolver{cwd: "/resolved/cwd"}
	var logs bytes.Buffer
	h := &Handler{
		resolver:  resolver,
		hostTools: host,
		logger:    slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{
		Name:    "skill_expand_body",
		AgentID: "agent-obs",
		CallID:  "call-obs",
	})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}
	if got == nil || !got.Success {
		t.Fatalf("callHostTool() result = %#v, want success", got)
	}
	if snap := skillmetrics.Read(); snap.HostToolCallOKTotal != 1 {
		t.Fatalf("HostToolCallOKTotal = %d, want 1 (snapshot %+v)", snap.HostToolCallOKTotal, snap)
	}
	text := logs.String()
	for _, want := range []string{"toolbridge host-direct tool call", "tool=skill_expand_body", "agent_id=agent-obs", "call_id=call-obs", "outcome=ok"} {
		if !bytes.Contains([]byte(text), []byte(want)) {
			t.Fatalf("host-direct log missing %q in %s", want, text)
		}
	}
}

func TestCallHostTool_CWDMissingCounterAndWarn(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)
	host := &stubHostToolRegistry{
		hasToolName: "skill_expand_body",
		err:         skillpkg.ErrMissingCWD,
	}
	var logs bytes.Buffer
	h := &Handler{
		hostTools: host,
		logger:    slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: "skill_expand_body", AgentID: "agent-missing"})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("callHostTool() result = %#v, want structured failure", got)
	}
	if snap := skillmetrics.Read(); snap.HostToolCallCWDMissingTotal != 1 {
		t.Fatalf("HostToolCallCWDMissingTotal = %d, want 1 (snapshot %+v)", snap.HostToolCallCWDMissingTotal, snap)
	}
	text := logs.String()
	for _, want := range []string{"toolbridge host-direct cwd missing before call", "agent_id=agent-missing", "outcome=cwd_missing"} {
		if !bytes.Contains([]byte(text), []byte(want)) {
			t.Fatalf("cwd-missing observability log missing %q in %s", want, text)
		}
	}
}

func decodeToolResultEnvelope(t *testing.T, got *ToolCallResult) map[string]any {
	t.Helper()
	if got == nil {
		t.Fatal("ToolCallResult = nil")
	}
	if len(got.ContentItems) != 1 {
		t.Fatalf("len(ContentItems) = %d, want 1", len(got.ContentItems))
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(got.ContentItems[0].Text), &envelope); err != nil {
		t.Fatalf("decode envelope %q: %v", got.ContentItems[0].Text, err)
	}
	return envelope
}

type stubKindRegistry struct {
	mu       sync.Mutex
	peers    map[string][]*mcpcontrol.ToolInstance
	gotKinds []string
}

func (r *stubKindRegistry) FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gotKinds = append(r.gotKinds, clientKind)
	return r.peers[clientKind]
}

func listToolsPeer(tools []dto.MCPTool, err error) *mcpcontrol.ToolInstance {
	return &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, _ any, result any) error {
		if method != "tools/list" {
			return fmt.Errorf("method = %q, want tools/list", method)
		}
		if err != nil {
			return err
		}
		out, ok := result.(*peerToolsListResult)
		if !ok {
			return fmt.Errorf("result type = %T, want *peerToolsListResult", result)
		}
		*out = peerToolsListResult{Tools: tools}
		return nil
	}}}
}

func blockingListToolsPeer(kind string, tools []dto.MCPTool, started chan<- string, release <-chan struct{}) *mcpcontrol.ToolInstance {
	return &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(ctx context.Context, method string, _ any, result any) error {
		if method != "tools/list" {
			return fmt.Errorf("method = %q, want tools/list", method)
		}
		started <- kind
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		out, ok := result.(*peerToolsListResult)
		if !ok {
			return fmt.Errorf("result type = %T, want *peerToolsListResult", result)
		}
		*out = peerToolsListResult{Tools: tools}
		return nil
	}}}
}

func TestListToolsForCodex_HostToolsSurviveOrchFailure_LSPReady(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "skill_expand_body", Description: "host skill"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "lsp_hover", Description: "lsp"}}, nil)},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "skill_expand_body" || got[1].Name != "lsp_hover" {
		t.Fatalf("tools = %+v, want host + lsp", got)
	}
}

func TestListToolsForCodex_LogsDegradedPeer(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "skill_expand_body", Description: "host skill"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "lsp_hover", Description: "lsp"}}, nil)},
	}}
	var logs bytes.Buffer
	h := &Handler{
		registry:  registry,
		hostTools: host,
		logger:    slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("tools = %+v, want host + surviving peer", got)
	}
	text := logs.String()
	for _, want := range []string{"toolbridge dynamic tools peer degraded", "client_kind=orch", "orch down"} {
		if !bytes.Contains([]byte(text), []byte(want)) {
			t.Fatalf("degraded peer log missing %q in %s", want, text)
		}
	}
}

func TestListToolsForCodex_HostToolsSurviveLSPFailure_OrchReady(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "skill_expand_body", Description: "host skill"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "spawn_agent", Description: "orch"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, errors.New("lsp down"))},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "skill_expand_body" || got[1].Name != "spawn_agent" {
		t.Fatalf("tools = %+v, want host + orch", got)
	}
}

func TestListToolsForCodex_HostOnlyWhenBothPeersFail(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "skill_expand_body", Description: "host skill"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer(nil, errors.New("lsp down"))},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "skill_expand_body" {
		t.Fatalf("tools = %+v, want host only", got)
	}
}

func TestListToolsForCodex_ReturnsErrorWhenNoHostAndPeersFail(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer(nil, errors.New("lsp down"))},
	}}
	h := &Handler{registry: registry}

	got, err := h.ListToolsForCodex(context.Background())
	if err == nil {
		t.Fatalf("ListToolsForCodex() error = nil, tools = %+v", got)
	}
	if len(got) != 0 {
		t.Fatalf("tools = %+v, want none on hard failure", got)
	}
}

func TestListToolsForCodex_DedupKeepsHostBeforePeer(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "dupe", Description: "host wins"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "dupe", Description: "peer loses"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "lsp_hover", Description: "lsp"}}, nil)},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "dupe" || got[0].Description != "host wins" || got[1].Name != "lsp_hover" {
		t.Fatalf("tools = %+v, want host duplicate first and lsp second", got)
	}
}

func TestListToolsForCodex_LogsShadowedPeerTool(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "dupe", Description: "host wins"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "dupe", Description: "peer loses"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "lsp_hover", Description: "lsp"}}, nil)},
	}}
	var logs bytes.Buffer
	h := &Handler{
		registry:  registry,
		hostTools: host,
		logger:    slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("tools = %+v, want deduped host + lsp", got)
	}
	text := logs.String()
	for _, want := range []string{"toolbridge dynamic tool shadowed by earlier source", "tool=dupe", "source=orch", "shadowed_by=host"} {
		if !bytes.Contains([]byte(text), []byte(want)) {
			t.Fatalf("shadow log missing %q in %s", want, text)
		}
	}
}

func TestListToolsForCodex_PeerWaitIsConcurrent(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {blockingListToolsPeer(dto.ClientKindOrch, []dto.MCPTool{{Name: "spawn_agent"}}, started, release)},
		dto.ClientKindLSP:  {blockingListToolsPeer(dto.ClientKindLSP, []dto.MCPTool{{Name: "lsp_hover"}}, started, release)},
	}}
	h := &Handler{registry: registry}
	type result struct {
		toolCount int
		err       error
	}
	done := make(chan result, 1)
	go func() {
		tools, err := h.ListToolsForCodex(context.Background())
		done <- result{toolCount: len(tools), err: err}
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case kind := <-started:
			seen[kind] = true
		case <-time.After(200 * time.Millisecond):
			close(release)
			t.Fatalf("peer tools/list did not start concurrently; seen=%v", seen)
		}
	}
	close(release)
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("ListToolsForCodex() error = %v", res.err)
		}
		if res.toolCount != 2 {
			t.Fatalf("tool count = %d, want two peer tools", res.toolCount)
		}
	case <-time.After(time.Second):
		t.Fatal("ListToolsForCodex() did not finish after releasing peer callbacks")
	}
}

func TestMemoryWriteHostToolRegistry_DisabledToolsHideButRejectStaleCall(t *testing.T) {
	reg := NewMemoryWriteHostToolRegistry(&stubAgentMemoryWriter{}, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: false})

	if tools := reg.ListHostTools(); tools != nil {
		t.Fatalf("ListHostTools() = %+v, want nil when tools disabled", tools)
	}
	if !reg.HasTool(ToolNameMemoryWrite) {
		t.Fatalf("HasTool(%q) = false, want true for stale call handling", ToolNameMemoryWrite)
	}
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryWrite})
	if contract.AgentMemoryErrorCode(err) != "tools_disabled" {
		t.Fatalf("CallHostTool() error = %v, want tools_disabled", err)
	}
}

func TestMemoryWriteHostToolRegistry_ListSchemaAndCall(t *testing.T) {

	writer := &stubAgentMemoryWriter{}
	reg := NewMemoryWriteHostToolRegistry(writer, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: true})
	tools := reg.ListHostTools()
	if len(tools) != 1 {
		t.Fatalf("ListHostTools() len = %d, want 1", len(tools))
	}
	if tools[0].Name != ToolNameMemoryWrite {
		t.Fatalf("tool name = %q, want %q", tools[0].Name, ToolNameMemoryWrite)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("schema json invalid: %v", err)
	}
	required, _ := schema["required"].([]any)
	if !containsAnyString(required, "description") {
		t.Fatalf("schema required = %#v, want description required", required)
	}
	if !reg.HasTool(ToolNameMemoryWrite) {
		t.Fatalf("HasTool(%q) = false, want true", ToolNameMemoryWrite)
	}

	args := mustMarshal(t, map[string]any{
		"name":        "daily-report-style",
		"description": "Report style preference",
		"content":     "Prefer concise daily status.\nWhy: user asked.\nHow to apply: keep reports short.",
		"type":        "feedback",
	})
	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameMemoryWrite,
		Arguments: args,
		AgentID:   "agent-1",
		ThreadID:  "thread-1",
		CWD:       "/repo",
		CallID:    "call-1",
	})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("writer calls = %d, want 1", writer.calls)
	}
	if writer.last.Name != "daily-report-style" || writer.last.Description != "Report style preference" || writer.last.Type != contract.MemoryTypeFeedback || writer.last.Scope != contract.MemoryScopeUser || writer.last.AgentID != "agent-1" || writer.last.ThreadID != "thread-1" || writer.last.CWD != "/repo" || writer.last.CallID != "call-1" || writer.last.Source != "agent_tool" {
		t.Fatalf("writer request = %+v", writer.last)
	}
	res, ok := result.(contract.AgentMemoryWriteResult)
	if !ok {
		t.Fatalf("result type = %T, want contract.AgentMemoryWriteResult", result)
	}
	if res.ActualTarget != "private" || res.Type != contract.MemoryTypeFeedback {
		t.Fatalf("result = %+v", res)
	}
}

func TestMemoryWriteHostToolRegistry_RejectsPathAndLocalScope(t *testing.T) {
	reg := NewMemoryWriteHostToolRegistry(&stubAgentMemoryWriter{}, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: true})
	tests := []struct {
		name string
		args map[string]any
		code string
	}{
		{
			name: "path traversal field",
			args: map[string]any{"name": "x", "description": "d", "content": "c", "type": "feedback", "path": "../../x"},
			code: "invalid_input",
		},
		{
			name: "local scope unsupported",
			args: map[string]any{"name": "x", "description": "d", "content": "c", "type": "feedback", "scope": "local"},
			code: "unsupported_scope",
		},
		{
			name: "feedback project mismatch",
			args: map[string]any{"name": "x", "description": "d", "content": "c", "type": "feedback", "scope": "project"},
			code: "invalid_input",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryWrite, Arguments: mustMarshal(t, tt.args)})
			if err == nil {
				t.Fatal("CallHostTool() error = nil, want validation error")
			}
			if code := contract.AgentMemoryErrorCode(err); code != tt.code {
				t.Fatalf("error code = %q, want %q (err=%v)", code, tt.code, err)
			}
		})
	}
}

func TestCompositeHostToolRegistry_HostOrderAndCall(t *testing.T) {
	first := &stubHostToolRegistry{hasToolName: "dup", tools: []dto.MCPTool{{Name: "dup", Description: "first"}}, result: map[string]any{"source": "first"}}
	second := &stubHostToolRegistry{hasToolName: "dup", tools: []dto.MCPTool{{Name: "dup", Description: "second"}}}
	reg := NewCompositeHostToolRegistry(first, second)
	tools := reg.ListHostTools()
	if len(tools) != 1 || tools[0].Description != "first" {
		t.Fatalf("composite tools = %+v, want first duplicate only", tools)
	}
	result, err := reg.CallHostTool(context.Background(), HostToolCall{Name: "dup"})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	if first.calls != 1 || second.calls != 0 {
		t.Fatalf("calls first=%d second=%d, want first only", first.calls, second.calls)
	}
	got, _ := result.(map[string]any)
	if got["source"] != "first" {
		t.Fatalf("result = %#v", result)
	}
}

type stubAgentMemoryReader struct {
	calls        int
	last         contract.MemoryReadRequest
	err          error
	enabled      bool
	toolsEnabled bool
}

func (s *stubAgentMemoryReader) ReadAgentMemory(_ context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
	s.calls++
	s.last = req
	if s.err != nil {
		return contract.MemoryReadResult{}, s.err
	}
	return contract.MemoryReadResult{Entry: &contract.MemoryEntry{Name: req.Name, Type: req.Type, Content: "memory content"}, SourcePath: "feedback/read.md", IndexHit: true}, nil
}

func (s *stubAgentMemoryReader) MemoryReadEnabled() bool {
	return s == nil || s.enabled
}

func (s *stubAgentMemoryReader) MemoryReadToolsEnabled() bool {
	return s == nil || s.toolsEnabled
}

func TestMemoryReadHostToolRegistry_ListSchemaAndCall(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	reg := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	tools := reg.ListHostTools()
	if len(tools) != 1 || tools[0].Name != ToolNameMemoryRead {
		t.Fatalf("ListHostTools() = %+v, want memory_read", tools)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("schema json error = %v", err)
	}
	properties := schema["properties"].(map[string]any)
	for _, key := range []string{"name", "path", "scope", "type"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("schema properties missing %q: %#v", key, properties)
		}
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	scopeSchema := properties["scope"].(map[string]any)
	if containsAnyString(scopeSchema["enum"].([]any), "private") || containsAnyString(scopeSchema["enum"].([]any), "project") || containsAnyString(scopeSchema["enum"].([]any), "local") {
		t.Fatalf("scope enum = %#v, want only public supported scopes", scopeSchema["enum"])
	}

	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameMemoryRead,
		Arguments: mustMarshal(t, map[string]any{"name": "daily-report-style", "scope": "team", "type": "project"}),
		AgentID:   "agent-1",
		ThreadID:  "thread-1",
		CWD:       "/repo",
		CallID:    "call-1",
	})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	if reader.calls != 1 || reader.last.Name != "daily-report-style" || reader.last.Scope != contract.MemoryScopeTeam || reader.last.Type != contract.MemoryTypeProject || reader.last.CWD != "/repo" || reader.last.AgentID != "agent-1" {
		t.Fatalf("reader request = %+v calls=%d", reader.last, reader.calls)
	}
	if _, ok := result.(contract.MemoryReadResult); !ok {
		t.Fatalf("result type = %T, want contract.MemoryReadResult", result)
	}
}

func TestMemoryReadHostToolRegistry_ListHiddenWhenDisabled(t *testing.T) {
	reader := &stubAgentMemoryReader{}
	cases := []MemoryReadHostToolOptions{{Enabled: false, ToolsEnabled: true}, {Enabled: true, ToolsEnabled: false}}
	for _, opts := range cases {
		reg := NewMemoryReadHostToolRegistry(reader, opts)
		if got := reg.ListHostTools(); len(got) != 0 {
			t.Fatalf("ListHostTools() = %+v, want hidden for opts %+v", got, opts)
		}
		if !reg.HasTool(ToolNameMemoryRead) {
			t.Fatalf("HasTool(%q) = false, want true for stale call handling", ToolNameMemoryRead)
		}
	}
}

func TestMemoryReadHostToolRegistry_StaleCallWhenFeatureDisabled(t *testing.T) {
	reg := NewMemoryReadHostToolRegistry(&stubAgentMemoryReader{}, MemoryReadHostToolOptions{Enabled: false, ToolsEnabled: true})
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "x"})})
	if code := contract.AgentMemoryErrorCode(err); code != "feature_disabled" {
		t.Fatalf("error code = %q, want feature_disabled (err=%v)", code, err)
	}
}

func TestMemoryReadHostToolRegistry_StaleCallWhenToolsDisabled(t *testing.T) {
	reg := NewMemoryReadHostToolRegistry(&stubAgentMemoryReader{}, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "x"})})
	if code := contract.AgentMemoryErrorCode(err); code != "tools_disabled" {
		t.Fatalf("error code = %q, want tools_disabled (err=%v)", code, err)
	}
}

func TestMemoryReadHostToolRegistry_ReaderUnavailable(t *testing.T) {
	reg := &MemoryReadHostToolRegistry{opts: MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true}}
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "x"})})
	if code := contract.AgentMemoryErrorCode(err); code != "reader_unavailable" {
		t.Fatalf("error code = %q, want reader_unavailable (err=%v)", code, err)
	}
}

func TestMemoryReadHostToolRegistry_InvalidInput(t *testing.T) {
	reg := NewMemoryReadHostToolRegistry(&stubAgentMemoryReader{enabled: true, toolsEnabled: true}, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	for _, args := range []json.RawMessage{json.RawMessage(`{"name":"x","scope":"private"}`), json.RawMessage(`{"name":"x","type":"bogus"}`), json.RawMessage(`not-json`)} {
		_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: args})
		if code := contract.AgentMemoryErrorCode(err); code != "invalid_input" {
			t.Fatalf("error code = %q, want invalid_input for args %s (err=%v)", code, string(args), err)
		}
	}
}

func TestMemoryReadHostToolRegistry_ReaderError(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true, err: contract.NewAgentMemoryError("not_found", errors.New("missing"))}
	reg := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "missing"})})
	if code := contract.AgentMemoryErrorCode(err); code != "not_found" {
		t.Fatalf("error code = %q, want not_found (err=%v)", code, err)
	}
}

type stubAgentMemoryWriter struct {
	calls int
	last  contract.AgentMemoryWriteRequest
}

func (s *stubAgentMemoryWriter) WriteAgentMemory(_ context.Context, req contract.AgentMemoryWriteRequest) (contract.AgentMemoryWriteResult, error) {
	s.calls++
	s.last = req
	return contract.AgentMemoryWriteResult{Path: "feedback/daily-report-style.md", RequestedScope: req.Scope, ActualTarget: "private", Type: req.Type}, nil
}

func (s *stubAgentMemoryWriter) MemoryWriteEnabled() bool { return true }

func (s *stubAgentMemoryWriter) MemoryWriteToolsEnabled() bool { return true }

func containsAnyString(values []any, want string) bool {
	for _, value := range values {
		if s, ok := value.(string); ok && s == want {
			return true
		}
	}
	return false
}

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

func TestListToolsForCodex_FiltersPeerMemoryReadWhenReaderUnavailable(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, nil)},
	}}
	h := &Handler{registry: registry}
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

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "daily"}), AgentID: "agent-1"})

	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want host tool error", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["tool"] != ToolNameMemoryRead || envelope["code"] != "tools_disabled" {
		t.Fatalf("envelope = %#v, want tools_disabled", envelope)
	}
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

	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want host tool error", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["tool"] != ToolNameMemoryRead || envelope["code"] != "feature_disabled" {
		t.Fatalf("envelope = %#v, want feature_disabled", envelope)
	}
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

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "private"}), AgentID: "agent-1"})

	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want host tool error", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["tool"] != ToolNameMemoryRead || envelope["code"] != "not_visible" {
		t.Fatalf("envelope = %#v, want not_visible", envelope)
	}
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
	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "daily", "scope": "user"}), AgentID: "agent-1"})
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
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["tool"] != ToolNameMemoryWrite || envelope["code"] != "tools_disabled" {
		t.Fatalf("envelope = %#v, want tools_disabled", envelope)
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

// ── SkillReadSectionRegistry tests ─────────────────────────────────────────

func TestNewSkillReadSectionRegistry_NilTool(t *testing.T) {
	if got := NewSkillReadSectionRegistry(nil); got != nil {
		t.Fatalf("NewSkillReadSectionRegistry(nil) must return nil, got %#v", got)
	}
}

func TestSkillReadSectionRegistry_ListHostTools_SingleEntry(t *testing.T) {
	cacheDir := t.TempDir()
	reg := NewSkillReadSectionRegistry(NewSkillReadSectionTool(cacheDir, skilllibrary.ReadSection, nil))
	tools := reg.ListHostTools()
	if len(tools) != 1 {
		t.Fatalf("expect 1 tool, got %d: %+v", len(tools), tools)
	}
	if tools[0].Name != ToolNameReadSection {
		t.Fatalf("tool name = %q, want %q", tools[0].Name, ToolNameReadSection)
	}
	if tools[0].Description == "" {
		t.Fatal("tool description must not be empty")
	}
	if len(tools[0].InputSchema) == 0 {
		t.Fatal("tool InputSchema must not be empty")
	}
}

func TestSkillReadSectionRegistry_HasTool(t *testing.T) {
	reg := NewSkillReadSectionRegistry(NewSkillReadSectionTool(t.TempDir(), skilllibrary.ReadSection, nil))
	if !reg.HasTool(ToolNameReadSection) {
		t.Fatalf("HasTool(%q) = false, want true", ToolNameReadSection)
	}
	if reg.HasTool("skill_expand_body") {
		t.Fatal("HasTool(skill_expand_body) = true, want false — old tools must not be listed")
	}
	if reg.HasTool("skill_read_resource") {
		t.Fatal("HasTool(skill_read_resource) = true, want false — old tools must not be listed")
	}
	if reg.HasTool("unknown_tool") {
		t.Fatal("HasTool(unknown_tool) = true, want false")
	}
}

func TestSkillReadSectionRegistry_NilReceiver(t *testing.T) {
	var r *SkillReadSectionRegistry
	if got := r.ListHostTools(); got != nil {
		t.Fatalf("nil receiver ListHostTools must return nil, got %v", got)
	}
	if r.HasTool(ToolNameReadSection) {
		t.Fatal("nil receiver HasTool must return false")
	}
	_, err := r.CallHostTool(context.Background(), HostToolCall{Name: ToolNameReadSection})
	if err == nil {
		t.Fatal("nil receiver CallHostTool must return error")
	}
}

func TestSkillReadSectionRegistry_CallHostTool_ReadsSection(t *testing.T) {
	cacheDir := t.TempDir()
	makeRefFile(t, cacheDir, "tdd", "overview", "TDD overview content")

	reg := NewSkillReadSectionRegistry(NewSkillReadSectionTool(cacheDir, skilllibrary.ReadSection, nil))
	args := mustMarshal(t, map[string]any{"name": "tdd", "anchor": "overview"})
	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameReadSection,
		Arguments: args,
		CWD:       "/some/project", // CWD unused by cache-based read
	})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	res, ok := result.(SkillReadSectionResult)
	if !ok {
		t.Fatalf("result type = %T, want SkillReadSectionResult", result)
	}
	if res.Name != "tdd" {
		t.Fatalf("result name = %q, want \"tdd\"", res.Name)
	}
	if res.Anchor != "overview" {
		t.Fatalf("result anchor = %q, want \"overview\"", res.Anchor)
	}
	if res.Body != "TDD overview content" {
		t.Fatalf("result body = %q, want \"TDD overview content\"", res.Body)
	}
	if res.Truncated {
		t.Fatal("result truncated = true, want false")
	}
	if res.TotalBytes != len("TDD overview content") {
		t.Fatalf("result total_bytes = %d, want %d", res.TotalBytes, len("TDD overview content"))
	}
}

func TestSkillReadSectionRegistry_CallHostTool_TruncatedMetadata(t *testing.T) {
	cacheDir := t.TempDir()
	makeRefFile(t, cacheDir, "tdd", "overview", "abcdefghij")

	reg := NewSkillReadSectionRegistry(NewSkillReadSectionTool(cacheDir, skilllibrary.ReadSection, nil))
	args := mustMarshal(t, map[string]any{"name": "tdd", "anchor": "overview", "max_bytes": 4})
	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameReadSection,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	res, ok := result.(SkillReadSectionResult)
	if !ok {
		t.Fatalf("result type = %T, want SkillReadSectionResult", result)
	}
	if res.Body != "abcd" {
		t.Fatalf("result body = %q, want \"abcd\"", res.Body)
	}
	if !res.Truncated {
		t.Fatal("result truncated = false, want true")
	}
	if res.TotalBytes != len("abcdefghij") {
		t.Fatalf("result total_bytes = %d, want %d", res.TotalBytes, len("abcdefghij"))
	}
}

func TestSkillReadSectionRegistry_CallHostTool_UnknownToolReturnsError(t *testing.T) {
	reg := NewSkillReadSectionRegistry(NewSkillReadSectionTool(t.TempDir(), skilllibrary.ReadSection, nil))

	_, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      "skill_expand_body",
		Arguments: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("expect error for unknown tool name")
	}
}

// TestListToolsForCodex_HostToolIsReadSection verifies that when a
// SkillReadSectionRegistry is wired as the host registry, ListToolsForCodex
// surfaces skill_read_section (not skill_expand_body or skill_read_resource).
func TestListToolsForCodex_HostToolIsReadSection(t *testing.T) {
	cacheDir := t.TempDir()
	reg := NewSkillReadSectionRegistry(NewSkillReadSectionTool(cacheDir, skilllibrary.ReadSection, nil))
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "spawn_agent"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "lsp_hover"}}, nil)},
	}}
	h := &Handler{registry: registry, hostTools: reg}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) == 0 || got[0].Name != ToolNameReadSection {
		t.Fatalf("tools = %+v, want skill_read_section as first tool", got)
	}
	for _, tool := range got {
		if tool.Name == "skill_expand_body" || tool.Name == "skill_read_resource" {
			t.Fatalf("old tool %q must not appear in Codex tool list, got %+v", tool.Name, got)
		}
	}
}

// TestRouteToolCall_SkillReadSection_BypassesPeer verifies that a
// skill_read_section call is routed to the host registry without
// touching the peer network.
func TestRouteToolCall_SkillReadSection_BypassesPeer(t *testing.T) {
	cacheDir := t.TempDir()
	makeRefFile(t, cacheDir, "demo", "intro", "intro content")

	reg := NewSkillReadSectionRegistry(NewSkillReadSectionTool(cacheDir, skilllibrary.ReadSection, nil))
	registry := &stubRegistry{}
	h := &Handler{registry: registry, hostTools: reg}

	args := mustMarshal(t, map[string]any{"name": "demo", "anchor": "intro"})
	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      ToolNameReadSection,
		Arguments: args,
		AgentID:   "agent-1",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || !got.Success {
		t.Fatalf("routeToolCall() result = %#v, want success", got)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("peer registry was consulted despite host-direct match: %+v", registry.gotKinds)
	}
}
