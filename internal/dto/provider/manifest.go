package provider

type ToolFamily string

const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"
)

type MCPBinary struct {
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"` // "http" or "" (stdio)
	URL         string            `json:"url,omitempty"`  // HTTP endpoint URL
	Headers     map[string]string `json:"headers,omitempty"`
	Command     []string          `json:"command,omitempty"` // stdio command
	Env         map[string]string `json:"env,omitempty"`
	AutoApprove []string          `json:"autoApprove,omitempty"`
}

type MCPManifest struct {
	Binaries []MCPBinary `json:"binaries,omitempty"`
}

type ManifestTransportMode string

const (
	ManifestTransportDefault   ManifestTransportMode = ""
	ManifestTransportStdioOnly ManifestTransportMode = "stdio-only"
)

type ManifestContext struct {
	AgentID                      string
	ThreadID                     string
	CWD                          string
	AdditionalWorkingDirectories []string
	ThreadCaps                   CapabilitySet
	BinaryDir                    string
	ProjectRoot                  string
	Env                          map[string]string
	AutoApprove                  []string
	ExtraBinaries                []MCPBinary
	ProxyHTTPAddr                string
	PeerHTTPAddrs                map[ToolFamily]string // e.g. {FamilyOrch: "127.0.0.1:9091"}
	PeerHTTPTokens               map[ToolFamily]string
	TransportMode                ManifestTransportMode
}
