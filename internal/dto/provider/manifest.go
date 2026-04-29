package provider

type ToolFamily string

const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"
)

type MCPBinary struct {
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"`    // "http" or "" (stdio)
	URL         string            `json:"url,omitempty"`     // HTTP endpoint URL
	Command     []string          `json:"command,omitempty"` // stdio command
	Env         map[string]string `json:"env,omitempty"`
	AutoApprove []string          `json:"autoApprove,omitempty"`
}

type MCPManifest struct {
	Binaries []MCPBinary `json:"binaries,omitempty"`
}

type ManifestContext struct {
	AgentID       string
	ThreadID      string
	CWD           string
	ThreadCaps    CapabilitySet
	BinaryDir     string
	Env           map[string]string
	AutoApprove   []string
	ProxyHTTPAddr string
	PeerHTTPAddrs map[ToolFamily]string // e.g. {FamilyOrch: "127.0.0.1:9091"}
}
