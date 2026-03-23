package provider

import (
	"maps"
	"path/filepath"
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

	env := cloneManifestEnv(ctx.Env)
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
