package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/pkg/skilltool"
)

// stubSkillService 满足 skillpkg.Service 接口（仅本测试关心的两个方法），其余
// 方法 panic——确保意外路径会立刻暴露。
type stubSkillService struct {
	skillpkg.Service
	expandIn  skillpkg.ExpandBodyParams
	expandOut skillpkg.ExpandBodyResult
	expandErr error

	resourceIn  skillpkg.ReadResourceParams
	resourceOut skillpkg.ReadResourceResult
	resourceErr error

	cwdSeen string // 记录 ExpandBody/ReadResource 调用时 ctx 的 cwd
}

func (s *stubSkillService) ExpandBody(ctx context.Context, p skillpkg.ExpandBodyParams) (skillpkg.ExpandBodyResult, error) {
	s.cwdSeen, _ = skillpkg.RequireCWD(ctx)
	s.expandIn = p
	if s.expandErr != nil {
		return skillpkg.ExpandBodyResult{}, s.expandErr
	}
	return s.expandOut, nil
}

func (s *stubSkillService) ReadResource(ctx context.Context, p skillpkg.ReadResourceParams) (skillpkg.ReadResourceResult, error) {
	s.cwdSeen, _ = skillpkg.RequireCWD(ctx)
	s.resourceIn = p
	if s.resourceErr != nil {
		return skillpkg.ReadResourceResult{}, s.resourceErr
	}
	return s.resourceOut, nil
}

func TestNewSkillHostTools_NilService(t *testing.T) {
	if got := NewSkillHostTools(nil); got != nil {
		t.Fatalf("NewSkillHostTools(nil) must return nil, got %#v", got)
	}
}

func TestSkillHostTools_ListHostTools_NamesAndCount(t *testing.T) {
	stub := &stubSkillService{}
	h := NewSkillHostTools(stub)
	tools := h.ListHostTools()
	if len(tools) != 2 {
		t.Fatalf("expect 2 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	if !names[skilltool.ToolNameExpandBody] || !names[skilltool.ToolNameReadResource] {
		t.Fatalf("missing tool names: %+v", names)
	}
}

func TestSkillHostTools_HasTool(t *testing.T) {
	h := NewSkillHostTools(&stubSkillService{})
	if !h.HasTool(skilltool.ToolNameExpandBody) {
		t.Fatalf("HasTool should match expand_body")
	}
	if !h.HasTool(skilltool.ToolNameReadResource) {
		t.Fatalf("HasTool should match read_resource")
	}
	if h.HasTool("read_file") {
		t.Fatalf("HasTool should NOT match peer tools")
	}
}

func TestSkillHostTools_NilReceiver_HasToolReturnsFalse(t *testing.T) {
	var h *SkillHostTools // typed nil
	if h.HasTool(skilltool.ToolNameExpandBody) {
		t.Fatalf("nil receiver must return false from HasTool")
	}
	if got := h.ListHostTools(); got != nil {
		t.Fatalf("nil receiver must return nil from ListHostTools, got %v", got)
	}
}

func TestCallHostTool_PassesApprovalMetadata(t *testing.T) {
	stub := &stubSkillService{
		expandOut: skillpkg.ExpandBodyResult{Name: "foo", Content: "body"},
	}
	h := NewSkillHostTools(stub)
	// 模型可能（恶意/错误地）发了带 cwd 字段的 arguments；应被强制覆盖为 host 解析值。
	args := json.RawMessage(`{"name":"foo","cwd":"/malicious/path"}`)
	out, err := h.CallHostTool(context.Background(), HostToolCall{
		Name:      skilltool.ToolNameExpandBody,
		Arguments: args,
		CWD:       "/real/cwd",
		AgentID:   "agent-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		CallID:    "call-1",
	})
	if err != nil {
		t.Fatalf("CallHostTool err: %v", err)
	}
	if stub.cwdSeen != "/real/cwd" {
		t.Fatalf("ctx cwd = %q, want /real/cwd", stub.cwdSeen)
	}
	if stub.expandIn.CWD != "/real/cwd" {
		t.Fatalf("params.CWD overridden to %q, want /real/cwd (model field must be ignored)", stub.expandIn.CWD)
	}
	if stub.expandIn.Name != "foo" {
		t.Fatalf("name = %q", stub.expandIn.Name)
	}
	if stub.expandIn.AgentID != "agent-1" || stub.expandIn.ThreadID != "thread-1" || stub.expandIn.TurnID != "turn-1" || stub.expandIn.CallID != "call-1" {
		t.Fatalf("metadata not propagated: %+v", stub.expandIn)
	}
	if res, ok := out.(skillpkg.ExpandBodyResult); !ok || res.Content != "body" {
		t.Fatalf("output = %#v", out)
	}
}

func TestSkillHostTools_CallReadResource_PassesPath(t *testing.T) {
	stub := &stubSkillService{
		resourceOut: skillpkg.ReadResourceResult{Name: "foo", Path: "ref.md", Content: "hi"},
	}
	h := NewSkillHostTools(stub)
	// ReadResource 和 ExpandBody 一样必须忽略模型传入的 cwd，强制使用 host 解析出的 cwd。
	args := json.RawMessage(`{"name":"foo","path":"references/ref.md","cwd":"/malicious/path"}`)
	out, err := h.CallHostTool(context.Background(), HostToolCall{
		Name:      skilltool.ToolNameReadResource,
		Arguments: args,
		CWD:       "/work",
		AgentID:   "agent-r",
		ThreadID:  "thread-r",
		CallID:    "call-r",
	})
	if err != nil {
		t.Fatalf("CallHostTool err: %v", err)
	}
	if stub.resourceIn.Path != "references/ref.md" {
		t.Fatalf("path passed: %q", stub.resourceIn.Path)
	}
	if stub.resourceIn.CWD != "/work" {
		t.Fatalf("params.CWD overridden to %q, want /work (model field must be ignored)", stub.resourceIn.CWD)
	}
	if stub.cwdSeen != "/work" {
		t.Fatalf("ctx cwd = %q", stub.cwdSeen)
	}
	if stub.resourceIn.AgentID != "agent-r" || stub.resourceIn.ThreadID != "thread-r" || stub.resourceIn.CallID != "call-r" {
		t.Fatalf("resource metadata not propagated: %+v", stub.resourceIn)
	}
	if _, ok := out.(skillpkg.ReadResourceResult); !ok {
		t.Fatalf("output type = %T", out)
	}
}

func TestSkillHostTools_CallUnknownTool(t *testing.T) {
	h := NewSkillHostTools(&stubSkillService{})
	_, err := h.CallHostTool(context.Background(), HostToolCall{Name: "skill_does_not_exist", CWD: "/cwd"})
	if err == nil {
		t.Fatalf("expect error for unknown tool")
	}
}

func TestSkillHostTools_CallEmptyArgs(t *testing.T) {
	stub := &stubSkillService{
		expandOut: skillpkg.ExpandBodyResult{Name: ""},
	}
	h := NewSkillHostTools(stub)
	// nil arguments + empty name 由 service 自己拒绝；但 host_tools 不应在解码阶段崩。
	_, err := h.CallHostTool(context.Background(), HostToolCall{Name: skilltool.ToolNameExpandBody, CWD: "/cwd"})
	if err != nil {
		t.Fatalf("nil arguments path: %v", err)
	}
}

func TestSkillHostTools_PropagatesServiceError(t *testing.T) {
	want := errors.New("approval-required")
	stub := &stubSkillService{expandErr: want}
	h := NewSkillHostTools(stub)
	_, err := h.CallHostTool(context.Background(), HostToolCall{
		Name:      skilltool.ToolNameExpandBody,
		Arguments: json.RawMessage(`{"name":"foo"}`),
		CWD:       "/cwd",
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want wrapping %v", err, want)
	}
}

func TestDedupToolsByName_FirstWins(t *testing.T) {
	in := []common.MCPTool{
		{Name: "a", Description: "first"},
		{Name: "b"},
		{Name: "a", Description: "second"}, // 重复，应被忽略
		{Name: "c"},
		{Name: "b"}, // 重复忽略
	}
	out := dedupToolsByName(in)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	want := []string{"a", "b", "c"}
	for i, tl := range out {
		if tl.Name != want[i] {
			t.Fatalf("out[%d].Name = %q, want %q", i, tl.Name, want[i])
		}
	}
	// 首个 a 应被保留，描述为 "first"
	if out[0].Description != "first" {
		t.Fatalf("first-wins: out[0].Description = %q, want \"first\"", out[0].Description)
	}
}

type stubHostToolRegistry struct {
	hasToolName string
	tools       []common.MCPTool
	result      any
	err         error
	calls       int
	last        HostToolCall
}

func (s *stubHostToolRegistry) ListHostTools() []common.MCPTool {
	if s == nil {
		return nil
	}
	return append([]common.MCPTool(nil), s.tools...)
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
		hasToolName: skilltool.ToolNameExpandBody,
		err:         deniedHostToolError{},
	}
	h := &Handler{hostTools: host}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: skilltool.ToolNameExpandBody})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if got.Success {
		t.Fatalf("Success = true, want false")
	}
	if envelope["kind"] != "approval_denied" || envelope["tool"] != skilltool.ToolNameExpandBody {
		t.Fatalf("structured denied envelope = %#v", envelope)
	}
	if envelope["error"] == "" {
		t.Fatalf("structured denied envelope missing error: %#v", envelope)
	}
}

func TestCallHostTool_ApprovalRequiredFallbackReturnsStructuredResult(t *testing.T) {
	approvalReq := contract.ApprovalRequest{
		CallID:       "call-1",
		ToolName:     skilltool.ToolNameExpandBody,
		AgentID:      "agent-1",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		Reason:       "skill artifact requires approval",
		Kind:         "skill_artifact",
		SourceMethod: skilltool.ToolNameExpandBody,
		Payload: map[string]any{
			"artifact_kind":    skillpkg.ArtifactKindBody,
			"artifact_locator": "SKILL.md",
			"callId":           "call-1",
			"toolName":         skilltool.ToolNameExpandBody,
		},
	}
	host := &stubHostToolRegistry{
		hasToolName: skilltool.ToolNameExpandBody,
		err:         skillpkg.SkillApprovalRequiredError{Request: approvalReq},
	}
	h := &Handler{hostTools: host}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: skilltool.ToolNameExpandBody})
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
	if envelope["kind"] != "approval_required" || approval["callId"] != "call-1" || approval["toolName"] != skilltool.ToolNameExpandBody || approval["agentId"] != "agent-1" || approval["threadId"] != "thread-1" || approval["turnId"] != "turn-1" {
		t.Fatalf("structured approval_required envelope = %#v", envelope)
	}
	payload, ok := approval["payload"].(map[string]any)
	if !ok || payload["artifact_kind"] != skillpkg.ArtifactKindBody || payload["callId"] != "call-1" {
		t.Fatalf("approval payload = %#v", approval["payload"])
	}
}

func TestRouteToolCall_HostToolBypassesPeer_UsesResolvedCWD(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: skilltool.ToolNameExpandBody,
		result:      skillpkg.ExpandBodyResult{Name: "foo", Content: "body"},
	}
	resolver := &stubCWDResolver{cwd: "/resolved/cwd"}
	registry := &stubRegistry{}
	h := &Handler{registry: registry, resolver: resolver, hostTools: host}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      skilltool.ToolNameExpandBody,
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

func listToolsPeer(tools []common.MCPTool, err error) *mcpcontrol.ToolInstance {
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

func blockingListToolsPeer(kind string, tools []common.MCPTool, started chan<- string, release <-chan struct{}) *mcpcontrol.ToolInstance {
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
	host := &stubHostToolRegistry{tools: []common.MCPTool{{Name: skilltool.ToolNameExpandBody, Description: "host skill"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer([]common.MCPTool{{Name: "lsp_hover", Description: "lsp"}}, nil)},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != skilltool.ToolNameExpandBody || got[1].Name != "lsp_hover" {
		t.Fatalf("tools = %+v, want host + lsp", got)
	}
}

func TestListToolsForCodex_HostToolsSurviveLSPFailure_OrchReady(t *testing.T) {
	host := &stubHostToolRegistry{tools: []common.MCPTool{{Name: skilltool.ToolNameExpandBody, Description: "host skill"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]common.MCPTool{{Name: "spawn_agent", Description: "orch"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer(nil, errors.New("lsp down"))},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != skilltool.ToolNameExpandBody || got[1].Name != "spawn_agent" {
		t.Fatalf("tools = %+v, want host + orch", got)
	}
}

func TestListToolsForCodex_HostOnlyWhenBothPeersFail(t *testing.T) {
	host := &stubHostToolRegistry{tools: []common.MCPTool{{Name: skilltool.ToolNameExpandBody, Description: "host skill"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		dto.ClientKindLSP:  {listToolsPeer(nil, errors.New("lsp down"))},
	}}
	h := &Handler{registry: registry, hostTools: host}

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != skilltool.ToolNameExpandBody {
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
	host := &stubHostToolRegistry{tools: []common.MCPTool{{Name: "dupe", Description: "host wins"}}}
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]common.MCPTool{{Name: "dupe", Description: "peer loses"}}, nil)},
		dto.ClientKindLSP:  {listToolsPeer([]common.MCPTool{{Name: "lsp_hover", Description: "lsp"}}, nil)},
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

func TestListToolsForCodex_PeerWaitIsConcurrent(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {blockingListToolsPeer(dto.ClientKindOrch, []common.MCPTool{{Name: "spawn_agent"}}, started, release)},
		dto.ClientKindLSP:  {blockingListToolsPeer(dto.ClientKindLSP, []common.MCPTool{{Name: "lsp_hover"}}, started, release)},
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
