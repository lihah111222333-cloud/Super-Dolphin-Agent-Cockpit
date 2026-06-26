package toolbridge

// 本文件集中定义 toolbridge 对外 JSON-RPC wire 协议常量。
// 这些值会被 peer MCP server、proxy handler 和兼容性测试共同观察；
// 修改既有值会让已启动 peer、外部 MCP client 或缓存握手的会话失配，必须先规划 schema/version 迁移。

// MetadataKeyAgentID 等私有 metadata key 会注入下游 tools/call payload。
// 前导下划线用于避开工具自有参数，并标记这些字段只服务内部归因；不能单边改名。
const (
	MetadataKeyAgentID        = "_agentId"
	MetadataKeyThreadID       = "_threadId"
	MetadataKeyCallID         = "_callId"
	MetadataKeyCWD            = "_cwd"
	MetadataKeyWorkspaceRoots = "_workspaceRoots"
)

// ProxyProtocolVersion 和 ProxyServerInfo* 是 proxy initialize 响应的固定字段。
// 外部 MCP client 可能缓存握手结果，重启后必须保持稳定。
const (
	ProxyProtocolVersion    = "2025-11-25"
	ProxyServerInfoName     = "proxy"
	ProxyServerInfoVersion  = "1.0.0"
	ProxyNotificationMethod = "notifications/initialized"
)

// 支持的 proxy JSON-RPC method 名称。
// proxy 只分发这些方法，未知 method 必须返回 method-not-found，不能静默 ACK。
const (
	ProxyMethodInitialize = "initialize"
	ProxyMethodToolsList  = "tools/list"
	ProxyMethodToolsCall  = "tools/call"
)
