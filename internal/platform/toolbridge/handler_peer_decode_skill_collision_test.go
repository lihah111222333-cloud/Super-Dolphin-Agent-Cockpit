package toolbridge

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestPrepareCodexToolSurfaceNamespacesSkillOwnerCollisions(t *testing.T) {
	skills := collisionSkillProvider()
	host := &stubHostToolRegistry{
		hasToolName: "grep",
		tools: []mcpdto.MCPTool{{
			Name: "grep", Description: "Host grep", InputSchema: strictEmptyObjectSchema(),
		}},
		result: toolCallTextResult(true, "host grep"),
	}
	h := &Handler{
		hostTools:          host,
		skillTools:         skills,
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: &fakeMCPClient{}}),
	}
	manifest := providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
		Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"},
	}}}
	assertSkillCollisionRoutes(t, h, skills, manifest)
	if host.calls != 1 || host.last.Name != "grep" {
		t.Fatalf("host calls = %d last = %#v, want one grep call", host.calls, host.last)
	}
}

func TestPrepareCodexToolSurfaceNamespacesSkillMCPNameCollision(t *testing.T) {
	skills := collisionSkillProvider()
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{
		Name: "grep", Description: "MCP grep", InputSchema: strictEmptyObjectSchema(),
	}}}
	h := &Handler{
		skillTools:         skills,
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: client}),
	}
	manifest := providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
		Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"},
	}}}
	assertSkillCollisionRoutes(t, h, skills, manifest)
	if len(client.calls) != 1 || client.calls[0] != "grep" {
		t.Fatalf("MCP calls = %#v, want one grep call", client.calls)
	}
}

func collisionSkillProvider() *fakeSkillToolProvider {
	return &fakeSkillToolProvider{
		tools: []contract.SkillToolSurfaceTool{{
			Name: "grep", Description: "Skill grep", InputSchema: strictEmptyObjectSchema(),
		}},
		content: "skill grep",
	}
}

func assertSkillCollisionRoutes(
	t *testing.T,
	h *Handler,
	skills *fakeSkillToolProvider,
	manifest providerdto.MCPManifest,
) {
	t.Helper()
	cwd := t.TempDir()
	tools, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID: "agent-collision", ProviderThreadID: "thread-collision", CWD: cwd, Manifest: manifest,
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"grep", "skill__grep"})
	callCodexSurfaceToolForTest(t, h, "agent-collision", "thread-collision", cwd, "grep")
	callCodexSurfaceToolForTest(t, h, "agent-collision", "thread-collision", cwd, "skill__grep")
	if len(skills.calls) != 1 || skills.calls[0].Name != "grep" {
		t.Fatalf("skill calls = %#v, want one real-name grep call", skills.calls)
	}
}

func TestPrepareCodexToolSurfaceFiltersDisabledSkillCanonicalAlias(t *testing.T) {
	skills := &fakeSkillToolProvider{tools: []contract.SkillToolSurfaceTool{{
		Name: "grep", Description: "Skill grep", InputSchema: strictEmptyObjectSchema(),
	}}}
	h := &Handler{
		skillTools:         skills,
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: &fakeMCPClient{}}),
	}
	cwd := t.TempDir()
	tools, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-disabled-skill",
		ProviderThreadID: "thread-disabled-skill",
		CWD:              cwd,
		DisabledTools:    []string{"skill__grep"},
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"},
		}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertNoDynamicToolName(t, tools, "grep")
	assertNoDynamicToolName(t, tools, "skill__grep")
	result := callCodexSurfaceToolForTest(t, h, "agent-disabled-skill", "thread-disabled-skill", cwd, "skill__grep")
	assertDisabledCodexSurfaceToolResult(t, result, "skill__grep")
	if len(skills.calls) != 0 {
		t.Fatalf("disabled skill calls = %#v, want none", skills.calls)
	}
}
