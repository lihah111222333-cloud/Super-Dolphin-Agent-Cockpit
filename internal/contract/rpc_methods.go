package contract

// Thread RPC method names shared by the app-side thread module and remote
// orchestration launcher. Keep these in a dependency-light contract package so
// cmd/mcp-orch and internal/module/thread cannot silently drift apart.
const (
	ThreadRPCStart   = "thread/start"
	ThreadRPCStop    = "thread/stop"
	ThreadRPCArchive = "thread/archive"
	ThreadRPCNameSet = "thread/name/set"
	TurnRPCStart     = "turn/start"
)
