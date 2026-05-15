package manager

// ToolScope is the registry-facing LSP scope assembled from trusted
// server-side tool metadata plus the tool target. Model-supplied arguments are
// intentionally not identity inputs; callers should populate AgentID/ThreadID
// from common.ToolScopeFromContext.
type ToolScope struct {
	AgentID  string
	ThreadID string
	TurnID   string
	CallID   string
	CWD      string
	Family   string

	LanguageID string
	TargetPath string
	TargetURI  string

	WorkspaceRoot         string
	RootKind              string
	LanguageWorkspaceRoot string
	ProjectRoot           string
	LanguageSpecific      map[string]string
}

// ResolvedToolScope is the canonical scoped routing result returned by a
// production ManagerPool adapter. Registry/tool callers pass this value through
// for diagnostics/cache/bootstrap auditing instead of rebuilding keys.
type ResolvedToolScope struct {
	ToolScope

	ScopeKey     string
	WorkspaceKey string
	ShardKey     string
	ManagerKey   string
}

// ScopedManager couples the manager selected for a tool call with the canonical
// resolved scope produced by the pool.
type ScopedManager struct {
	Manager       Manager
	ResolvedScope ResolvedToolScope
}

// ScopedManagerResolver is implemented by the production multilsp ManagerPool
// adapter. It lets the registry route through ManagerPool.ForScope without
// importing multilsp and creating an import cycle.
type ScopedManagerResolver interface {
	ForToolScope(scope ToolScope) (ScopedManager, error)
	CurrentManagersForToolScope(scope ToolScope) ([]ScopedManager, error)
}
