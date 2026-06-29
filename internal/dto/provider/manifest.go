// Package provider 定义 provider 层的 DTO，供 agent-terminal、turn、prompt 等模块共享，不含业务逻辑。
package provider

// ToolFamily 标识 MCP binary 所属的工具族类型。
type ToolFamily string

// 内置工具族常量。
const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"
)

// MCPBinary 描述一个 MCP binary 的启动配置，支持 stdio 和 HTTP 两种传输模式。
type MCPBinary struct {
	Name            string            `json:"name"`
	TrustedServerID string            `json:"trustedServerId,omitempty"` // 受控 MCP server 配置生成的内部信任标记。
	Type            string            `json:"type,omitempty"`            // "http" 或 ""（stdio）。
	URL             string            `json:"url,omitempty"`             // HTTP 模式的端点地址。
	Headers         map[string]string `json:"headers,omitempty"`
	Command         []string          `json:"command,omitempty"` // stdio 模式的启动命令。
	Env             map[string]string `json:"env,omitempty"`
	AutoApprove     []string          `json:"autoApprove,omitempty"`
}

// MCPManifest 是一次 provider 会话所需的所有 MCP binary 列表。
type MCPManifest struct {
	Binaries []MCPBinary `json:"binaries,omitempty"`
}

// ManifestTransportMode 控制 manifest 生成时允许的传输模式。
type ManifestTransportMode string

// ManifestTransport* 是 ManifestTransportMode 的合法枚举值。
const (
	ManifestTransportDefault   ManifestTransportMode = ""           // 默认：允许所有传输模式。
	ManifestTransportStdioOnly ManifestTransportMode = "stdio-only" // 强制只生成 stdio 配置。
)

// ManifestContext 是生成 MCPManifest 时所需的运行时上下文，由 provider 驱动消费。
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
	ProxyHTTPToken               string
	PeerHTTPAddrs                map[ToolFamily]string // 各工具族的 HTTP peer 地址，如 {FamilyOrch: "127.0.0.1:9091"}。
	PeerHTTPTokens               map[ToolFamily]string // 对应各工具族 HTTP peer 的鉴权 token。
	TransportMode                ManifestTransportMode
}
