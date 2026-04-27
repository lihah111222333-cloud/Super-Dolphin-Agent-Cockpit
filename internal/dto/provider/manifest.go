package provider

type ToolFamily string

type MCPLaunchKind string

const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"

	LaunchKindSameBinarySkill MCPLaunchKind = "same-binary-skill"

	MCPEnvSkillCWD      = "GO_AGENT_SKILL_MCP_CWD"
	MCPEnvSkillAgentID  = "GO_AGENT_SKILL_MCP_AGENT_ID"
	MCPEnvSkillThreadID = "GO_AGENT_SKILL_MCP_THREAD_ID"
)

type MCPBinary struct {
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"` // "http" or "" (stdio)
	URL         string            `json:"url,omitempty"`  // HTTP endpoint URL
	LaunchKind  MCPLaunchKind     `json:"launch_kind,omitempty"`
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
