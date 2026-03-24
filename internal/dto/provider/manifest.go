package provider

import (
	"maps"
	"path/filepath"
	"strings"
)

type ToolFamily string

const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"
)

type MCPBinary struct {
	Name        string            `json:"name"`
	Command     []string          `json:"command"`
	Env         map[string]string `json:"env,omitempty"`
	AutoApprove []string          `json:"autoApprove,omitempty"`
}

type MCPManifest struct {
	Binaries []MCPBinary `json:"binaries,omitempty"`
}

type ManifestContext struct {
	AgentID     string
	CWD         string
	ThreadCaps  CapabilitySet
	BinaryDir   string
	Env         map[string]string
	AutoApprove []string
}

// BuildManifest returns declarative MCP binary metadata for external executors
// such as Claude CLI. The core process builds this manifest but never launches
// MCP binaries itself.
func BuildManifest(ctx ManifestContext) MCPManifest {
	families := []ToolFamily{FamilyLSP, FamilyOrch}
	if ctx.ThreadCaps.Has("ida") {
		families = append(families, FamilyIDA)
	}

	env := normalizeManifestEnv(ctx.Env)
	autoApprove := append([]string(nil), ctx.AutoApprove...)

	bins := make([]MCPBinary, 0, len(families))
	for _, fam := range families {
		name := "go-agent-mcp-" + string(fam)
		bins = append(bins, MCPBinary{
			Name:        name,
			Command:     []string{filepath.Join(ctx.BinaryDir, name)},
			Env:         cloneManifestEnv(env),
			AutoApprove: append([]string(nil), autoApprove...),
		})
	}
	return MCPManifest{Binaries: bins}
}

func cloneManifestEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func normalizeManifestEnv(in map[string]string) map[string]string {
	out := cloneManifestEnv(in)
	promoteManifestEnv(out, "GO_AGENT_CTL_RPC_ADDR", "RPC_ADDR")
	promoteManifestEnv(out, "GO_AGENT_CTL_INSTANCE_ID", "GO_AGENT_MCP_INSTANCE_ID")
	promoteManifestEnv(out, "GO_AGENT_CTL_BOOT_ID", "GO_AGENT_MCP_BOOT_ID")
	promoteManifestEnv(out, "GO_AGENT_CTL_BINARY_NAME", "GO_AGENT_MCP_BINARY_NAME")
	promoteManifestEnv(out, "GO_AGENT_CTL_CLIENT_KIND", "GO_AGENT_MCP_CLIENT_KIND")
	promoteManifestEnv(out, "GO_AGENT_CTL_AGENT_ID", "GO_AGENT_MCP_AGENT_ID")
	promoteManifestEnv(out, "GO_AGENT_CTL_THREAD_ID", "GO_AGENT_MCP_THREAD_ID")
	promoteManifestEnv(out, "GO_AGENT_CTL_SESSION_TOKEN", "GO_AGENT_MCP_SESSION_TOKEN")
	promoteManifestEnv(out, "GO_AGENT_CTL_BOOTSTRAP_JSON", "GO_AGENT_MCP_BOOT_CONTEXT")
	return out
}

func promoteManifestEnv(env map[string]string, canonical string, aliases ...string) {
	if value := strings.TrimSpace(env[canonical]); value != "" {
		env[canonical] = value
		for _, alias := range aliases {
			delete(env, alias)
		}
		return
	}
	for _, alias := range aliases {
		if value := strings.TrimSpace(env[alias]); value != "" {
			env[canonical] = value
			break
		}
	}
	for _, alias := range aliases {
		delete(env, alias)
	}
}
