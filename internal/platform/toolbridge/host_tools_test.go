package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	skillpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

// host tool 测试覆盖当前通用分发链路：
// callHostTool、routeToolCall、dedup 和 ListToolsForCodex 都通过 stubHostToolRegistry 驱动。
// 旧 skill body/resource 工具已下线，因此这里不再保留历史 skill 专用替身。
const testHostToolName = "test_host_echo"

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
		hasToolName: testHostToolName,
		err:         deniedHostToolError{},
	}
	h := &Handler{hostTools: host, skillMetrics: skillmetrics.NewRegistry()}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: testHostToolName, CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("callHostTool() error = %v, want structured approval denied result", err)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if got.Success {
		t.Fatalf("Success = true, want false")
	}
	if envelope["kind"] != "approval_denied" || envelope["tool"] != testHostToolName {
		t.Fatalf("structured denied envelope = %#v", envelope)
	}
	if envelope["success"] != false || envelope["code"] != "approval_denied" || envelope["hint"] == "" {
		t.Fatalf("structured denied envelope = %#v, want compatible ToolErrorEnvelope fields", envelope)
	}
	if envelope["error"] == "" {
		t.Fatalf("structured denied envelope missing error: %#v", envelope)
	}
}

func TestCallHostTool_ApprovalRequiredFallbackReturnsStructuredResult(t *testing.T) {
	metricSource := skillmetrics.NewRegistry()
	host := approvalRequiredHostToolRegistry()
	h := &Handler{hostTools: host, skillMetrics: metricSource}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: testHostToolName, CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("callHostTool() error = %v, want structured approval required result", err)
	}
	assertApprovalRequiredEnvelope(t, got)
	if snap := metricSource.Snapshot(); snap.HostToolCallApprovalReqTotal != 1 {
		t.Fatalf("HostToolCallApprovalReqTotal = %d, want 1 (snapshot %+v)", snap.HostToolCallApprovalReqTotal, snap)
	}
}

func approvalRequiredHostToolRegistry() *stubHostToolRegistry {
	approvalReq := contract.ApprovalRequest{
		CallID:       "call-1",
		ToolName:     testHostToolName,
		AgentID:      "agent-1",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		Reason:       "skill artifact requires approval",
		Kind:         "skill_artifact",
		SourceMethod: testHostToolName,
		Payload: map[string]any{
			"artifact_kind":    skillpkg.ArtifactKindBody,
			"artifact_locator": "SKILL.md",
			"callId":           "call-1",
			"toolName":         testHostToolName,
		},
	}
	return &stubHostToolRegistry{
		hasToolName: testHostToolName,
		err:         skillpkg.SkillApprovalRequiredError{Request: approvalReq},
	}
}

func assertApprovalRequiredEnvelope(t *testing.T, got *ToolCallResult) {
	t.Helper()
	envelope := decodeToolResultEnvelope(t, got)
	if got.Success {
		t.Fatalf("Success = true, want false")
	}
	approval, ok := envelope["approval"].(map[string]any)
	if !ok {
		t.Fatalf("approval envelope type = %T, envelope = %#v", envelope["approval"], envelope)
	}
	assertApprovalEnvelopeHeaders(t, envelope, approval)
	assertApprovalPayload(t, approval)
}

func assertApprovalEnvelopeHeaders(t *testing.T, envelope, approval map[string]any) {
	t.Helper()
	if envelope["kind"] != "approval_required" {
		t.Fatalf("structured approval_required envelope = %#v", envelope)
	}
	assertApprovalEnvelopeField(t, approval, "callId", "call-1")
	assertApprovalEnvelopeField(t, approval, "toolName", testHostToolName)
	assertApprovalEnvelopeField(t, approval, "agentId", "agent-1")
	assertApprovalEnvelopeField(t, approval, "threadId", "thread-1")
	assertApprovalEnvelopeField(t, approval, "turnId", "turn-1")
	if envelope["success"] != false || envelope["code"] != "approval_required" || envelope["hint"] == "" {
		t.Fatalf("structured approval_required envelope = %#v, want compatible ToolErrorEnvelope fields", envelope)
	}
	meta, ok := envelope["meta"].(map[string]any)
	if !ok || meta["kind"] != "approval_required" || meta["tool"] != testHostToolName {
		t.Fatalf("structured approval_required meta = %#v, want tool/kind", envelope["meta"])
	}
}

func assertApprovalEnvelopeField(t *testing.T, approval map[string]any, key, want string) {
	t.Helper()
	if approval[key] != want {
		t.Fatalf("approval[%s] = %#v, want %q in %#v", key, approval[key], want, approval)
	}
}

func assertApprovalPayload(t *testing.T, approval map[string]any) {
	t.Helper()
	payload, ok := approval["payload"].(map[string]any)
	if !ok || payload["artifact_kind"] != skillpkg.ArtifactKindBody || payload["callId"] != "call-1" {
		t.Fatalf("approval payload = %#v", approval["payload"])
	}
}

func TestRouteToolCall_HostToolBypassesPeer_UsesResolvedCWD(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: "custom_host_tool",
		result:      map[string]any{"name": "foo", "content": "body"},
	}
	resolver := &stubCWDResolver{cwd: t.TempDir()}
	registry := &stubRegistry{}
	h := &Handler{registry: registry, resolver: resolver, hostTools: host, skillMetrics: skillmetrics.NewRegistry()}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "custom_host_tool",
		Arguments: json.RawMessage(`{"name":"foo"}`),
		AgentID:   "agent-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		CallID:    "call-1",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertHostDirectRoute(t, got, host, resolver, registry)
}

func assertHostDirectRoute(t *testing.T, got *ToolCallResult, host *stubHostToolRegistry, resolver *stubCWDResolver, registry *stubRegistry) {
	t.Helper()
	if got == nil || !got.Success {
		t.Fatalf("routeToolCall() result = %#v, want success", got)
	}
	if host.calls != 1 {
		t.Fatalf("host calls = %d, want 1", host.calls)
	}
	assertResolvedHostToolMetadata(t, host.last, resolver.cwd)
	assertResolvedCWDResolverUsed(t, resolver)
	assertPeerRegistryUnused(t, registry)
}

func assertResolvedHostToolMetadata(t *testing.T, call HostToolCall, wantCWD string) {
	t.Helper()
	if call.CWD != wantCWD || call.AgentID != "agent-1" || call.ThreadID != "thread-1" || call.TurnID != "turn-1" || call.CallID != "call-1" {
		t.Fatalf("host call metadata = %+v", call)
	}
}

func assertResolvedCWDResolverUsed(t *testing.T, resolver *stubCWDResolver) {
	t.Helper()
	if !resolver.callSeen || resolver.agentID != "agent-1" {
		t.Fatalf("resolver callSeen=%v agentID=%q", resolver.callSeen, resolver.agentID)
	}
}

func assertPeerRegistryUnused(t *testing.T, registry *stubRegistry) {
	t.Helper()
	if len(registry.gotKinds) != 0 {
		t.Fatalf("peer registry was consulted despite host-direct match: %+v", registry.gotKinds)
	}
}

func TestRouteToolCall_HostToolPrefersInjectedCWD(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: "custom_host_tool",
		result:      map[string]any{"name": "foo", "content": "body"},
	}
	injectedCWD := t.TempDir()
	resolver := &stubCWDResolver{cwd: t.TempDir()}
	h := &Handler{registry: &stubRegistry{}, resolver: resolver, hostTools: host, skillMetrics: skillmetrics.NewRegistry()}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "custom_host_tool",
		Arguments: json.RawMessage(`{"name":"foo"}`),
		AgentID:   "agent-1",
		CWD:       injectedCWD,
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || !got.Success {
		t.Fatalf("routeToolCall() result = %#v, want success", got)
	}
	if host.last.CWD != injectedCWD {
		t.Fatalf("host call cwd = %q, want %q", host.last.CWD, injectedCWD)
	}
	if resolver.callSeen {
		t.Fatalf("resolver should not be called when request carries trusted cwd")
	}
}

func TestCallHostTool_ObservabilityCountersAndLogs(t *testing.T) {
	metricSource := skillmetrics.NewRegistry()
	host := &stubHostToolRegistry{
		hasToolName: testHostToolName,
		result:      map[string]any{"name": "foo", "content": "body"},
	}
	resolver := &stubCWDResolver{cwd: t.TempDir()}
	var logs bytes.Buffer
	h := &Handler{
		resolver:     resolver,
		hostTools:    host,
		skillMetrics: metricSource,
		logger:       slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{
		Name:    testHostToolName,
		AgentID: "agent-obs",
		CallID:  "call-obs",
	})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}
	if got == nil || !got.Success {
		t.Fatalf("callHostTool() result = %#v, want success", got)
	}
	if snap := metricSource.Snapshot(); snap.HostToolCallOKTotal != 1 {
		t.Fatalf("HostToolCallOKTotal = %d, want 1 (snapshot %+v)", snap.HostToolCallOKTotal, snap)
	}
	text := logs.String()
	for _, want := range []string{"toolbridge host-direct tool call", "tool=" + testHostToolName, "agent_id=agent-obs", "call_id=call-obs", "outcome=ok"} {
		if !bytes.Contains([]byte(text), []byte(want)) {
			t.Fatalf("host-direct log missing %q in %s", want, text)
		}
	}
}

func TestCallHostTool_PartialStructuredResultIsNotSuccess(t *testing.T) {
	metricSource := skillmetrics.NewRegistry()
	host := &stubHostToolRegistry{
		hasToolName: testHostToolName,
		result: map[string]any{
			"success":  false,
			"partial":  true,
			"degraded": true,
			"code":     "memory_write_partial",
			"message":  "memory write completed partially",
		},
	}
	resolver := &stubCWDResolver{cwd: t.TempDir()}
	var logs bytes.Buffer
	h := &Handler{
		resolver:     resolver,
		hostTools:    host,
		skillMetrics: metricSource,
		logger:       slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{
		Name:    testHostToolName,
		AgentID: "agent-partial",
		CallID:  "call-partial",
	})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("callHostTool() result = %#v, want structured partial failure", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["success"] != false || envelope["partial"] != true || envelope["degraded"] != true {
		t.Fatalf("structured envelope = %#v, want partial degraded failure flags", envelope)
	}
	if snap := metricSource.Snapshot(); snap.HostToolCallOKTotal != 0 || snap.HostToolCallErrorTotal != 1 {
		t.Fatalf("host tool counters = %+v, want 0 ok and 1 error", snap)
	}
	assertLogContainsAll(t, logs.String(), "toolbridge host-direct tool call", "agent_id=agent-partial", "call_id=call-partial", "outcome=error")
}

func TestCallHostTool_CWDMissingCounterAndWarn(t *testing.T) {
	metricSource := skillmetrics.NewRegistry()
	host := &stubHostToolRegistry{
		hasToolName: testHostToolName,
		err:         skillpkg.ErrMissingCWD,
	}
	var logs bytes.Buffer
	h := &Handler{
		hostTools:    host,
		skillMetrics: metricSource,
		logger:       slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: testHostToolName, AgentID: "agent-missing"})
	if err != nil {
		t.Fatalf("callHostTool() error = %v, want structured cwd-required result", err)
	}
	if got == nil || got.Success {
		t.Fatalf("callHostTool() result = %#v, want structured failure", got)
	}
	if snap := metricSource.Snapshot(); snap.HostToolCallCWDMissingTotal != 1 {
		t.Fatalf("HostToolCallCWDMissingTotal = %d, want 1 (snapshot %+v)", snap.HostToolCallCWDMissingTotal, snap)
	}
	text := logs.String()
	for _, want := range []string{"toolbridge host-direct tool call", "agent_id=agent-missing", "outcome=cwd_missing"} {
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

func assertLogContainsAll(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !bytes.Contains([]byte(text), []byte(want)) {
			t.Fatalf("log missing %q in %s", want, text)
		}
	}
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
		*out = peerToolsListResult{Tools: tools, toolsPresent: true}
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
		*out = peerToolsListResult{Tools: tools, toolsPresent: true}
		return nil
	}}}
}

func TestListToolsForCodexFailsWhenOrchPeerMissing(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: testHostToolName, Description: "host echo"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "inspect", Description: "lsp"}}, nil)},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err == nil {
		t.Fatalf("ListToolsForCodex() error = nil, tools = %+v", got)
	}
	if !strings.Contains(err.Error(), dto.ClientKindOrch) {
		t.Fatalf("ListToolsForCodex() error = %v, want orch peer failure", err)
	}
	if len(got) != 0 {
		t.Fatalf("tools = %+v, want no partial list on peer failure", got)
	}
}

func TestListToolsForCodexFailClosedDoesNotReturnPartialPeerList(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: testHostToolName, Description: "host echo"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "inspect", Description: "lsp"}}, nil)},
	}}
	var logs bytes.Buffer
	h := &Handler{
		registry:  registry,
		hostTools: host,
		logger:    slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	got, err := h.ListToolsForCodex(context.Background())
	if err == nil {
		t.Fatalf("ListToolsForCodex() error = nil, tools = %+v", got)
	}
	for _, fragment := range []string{dto.ClientKindOrch, "orch down"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("ListToolsForCodex() error = %v, want fragment %q", err, fragment)
		}
	}
	if len(got) != 0 {
		t.Fatalf("tools = %+v, want no partial list on peer failure", got)
	}
}

func TestListToolsForCodexFailsWhenLSPPeerErrors(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: testHostToolName, Description: "host echo"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "spawn_agent", Description: "orch"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, errors.New("lsp down"))},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err == nil {
		t.Fatalf("ListToolsForCodex() error = nil, tools = %+v", got)
	}
	if !strings.Contains(err.Error(), dto.ClientKindLSP) {
		t.Fatalf("ListToolsForCodex() error = %v, want lsp peer failure", err)
	}
	if len(got) != 0 {
		t.Fatalf("tools = %+v, want no partial list on peer failure", got)
	}
}

func TestListToolsForCodexFailsWhenLSPPeerMissing(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: testHostToolName, Description: "host echo"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "spawn_agent", Description: "orch"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, ErrNoPeerAvailable)},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err == nil {
		t.Fatalf("ListToolsForCodex() error = nil, tools = %+v", got)
	}
	if !strings.Contains(err.Error(), dto.ClientKindLSP) {
		t.Fatalf("ListToolsForCodex() error = %v, want lsp peer failure", err)
	}
	if len(got) != 0 {
		t.Fatalf("tools = %+v, want no partial list on peer failure", got)
	}
}

func TestListToolsForCodex_HostOnlyWhenBothPeersFail(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: testHostToolName, Description: "host echo"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer(nil, errors.New("lsp down"))},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err == nil {
		t.Fatalf("ListToolsForCodex() error = nil, tools = %+v", got)
	}
	if len(got) != 0 {
		t.Fatalf("tools = %+v, want no partial host-only list on peer failure", got)
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

func TestListToolsForCodex_PeerPanicSurfacesAsError(t *testing.T) {
	t.Parallel()

	h := &Handler{registry: panicActiveRegistry{}}
	_, err := h.ListToolsForCodex(context.Background())
	if err == nil || !strings.Contains(err.Error(), "list peer tools panic") {
		t.Fatalf("ListToolsForCodex() error = %v, want peer panic error", err)
	}
}

type panicActiveRegistry struct{}

func (panicActiveRegistry) FindActiveByKind(string) []*mcpcontrol.ToolInstance {
	// archguard:ignore panic_count -- 测试替身需要触发 registry panic recovery。
	panic("registry failed")
}

func TestListToolsForCodex_DedupKeepsHostBeforePeer(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "dupe", Description: "host wins"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "dupe", Description: "peer loses"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "inspect", Description: "lsp"}}, nil)},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "dupe" || got[0].Description != "host wins" || got[1].Name != "inspect" {
		t.Fatalf("tools = %+v, want host duplicate first and lsp second", got)
	}
}

func TestListToolsForCodexPreservesToolDetails(t *testing.T) {
	inputSchema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"search query"}}}`)
	outputSchema := json.RawMessage(`{"type":"object","properties":{"files":{"type":"object","description":"matches by file"}}}`)
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "launch_agent", Description: "launch", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "grep", Description: "grep source", InputSchema: inputSchema, OutputSchema: outputSchema}}, nil)},
	}}
	h := &Handler{registry: registry}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	assertDynamicToolSchema(t, got, "grep", "grep source", inputSchema, outputSchema)
}

func TestListToolsForCodex_LogsShadowedPeerTool(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "dupe", Description: "host wins"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "dupe", Description: "peer loses"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer([]dto.MCPTool{{Name: "inspect", Description: "lsp"}}, nil)},
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
		dto.ClientKindLSP:  {blockingListToolsPeer(dto.ClientKindLSP, []dto.MCPTool{{Name: "inspect"}}, started, release)},
	}}
	h := &Handler{registry: registry}
	type result struct {
		toolCount int
		err       error
	}
	done := make(chan result, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		tools, err := h.ListToolsForCodex(context.Background())
		done <- result{toolCount: len(tools), err: err}
	})

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
		wg.Wait()
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
