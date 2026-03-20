package provider

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
		bins = append(bins, MCPBinary{
			Name:    "go-agent-mcp-" + string(fam),
			Command: []string{ctx.BinaryDir + "/go-agent-mcp-" + string(fam)},
		})
	}
	return MCPManifest{Binaries: bins}
}
