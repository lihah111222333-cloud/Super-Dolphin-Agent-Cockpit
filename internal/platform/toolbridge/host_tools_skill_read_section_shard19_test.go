package toolbridge

import (
	"context"
	"encoding/json"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

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
