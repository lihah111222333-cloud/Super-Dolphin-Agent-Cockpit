package provider

// ToolFamily identifies a peer tool surface family in MCP manifests.
type ToolFamily string

const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"
)

// MCPBinary describes one MCP peer process or HTTP endpoint exposed to a
// provider runtime.
type MCPBinary struct {
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"` // "http" or "" (stdio)
	URL         string            `json:"url,omitempty"`  // HTTP endpoint URL
	Headers     map[string]string `json:"headers,omitempty"`
	Command     []string          `json:"command,omitempty"` // stdio command
	Env         map[string]string `json:"env,omitempty"`
	AutoApprove []string          `json:"autoApprove,omitempty"`
}

// MCPManifest is the complete provider-visible MCP peer manifest for a turn or
// session launch.
type MCPManifest struct {
	Binaries []MCPBinary `json:"binaries,omitempty"`
}

// ManifestTransportMode constrains how a provider should expose MCP binaries.
type ManifestTransportMode string

const (
	ManifestTransportDefault   ManifestTransportMode = ""
	ManifestTransportStdioOnly ManifestTransportMode = "stdio-only"
)

// ManifestContext is the input used to build a provider-specific MCP manifest.
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
