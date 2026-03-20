package provider

import "path/filepath"

type ToolFamily string

const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"
)

type MCPBinary struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

type MCPManifest struct {
	Binaries []MCPBinary `json:"binaries,omitempty"`
}

type ManifestContext struct {
	AgentID    string
	CWD        string
	ThreadCaps CapabilitySet
	BinaryDir  string
	Env        map[string]string
}

func BuildManifest(ctx ManifestContext) MCPManifest {
	families := []ToolFamily{FamilyLSP, FamilyOrch}
	if ctx.ThreadCaps.Has("ida") {
		families = append(families, FamilyIDA)
	}

	bins := make([]MCPBinary, 0, len(families))
	for _, fam := range families {
		name := "go-agent-mcp-" + string(fam)
		bins = append(bins, MCPBinary{
			Name:    name,
			Command: []string{filepath.Join(ctx.BinaryDir, name)},
		})
	}
	return MCPManifest{Binaries: bins}
}
