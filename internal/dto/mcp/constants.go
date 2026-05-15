package mcp

const (
	MethodRegister        = "ctl/register"
	MethodHeartbeat       = "ctl/heartbeat"
	MethodContext         = "ctl/context"
	MethodEvent           = "ctl/event"
	MethodLog             = "ctl/log"
	MethodApproval        = "ctl/approval/request"
	MethodReport          = "ctl/report"
	MethodShutdown        = "ctl/shutdown"
	MethodConfigChanged   = "ctl/config/changed"
	MethodLSPReleaseScope = "ctl/lsp/release_scope"

	ProtocolVersion = "ctl/v1"

	ClientKindOrch   = "orch"
	ClientKindLSP    = "lsp"
	ClientKindIDA    = "ida"
	ClientKindCustom = "custom"

	PeerKindTool          = "tool"
	PeerKindUI            = "ui"
	PeerKindSharedService = "shared-service"

	ScopeAgentRuntime   = "agent.runtime"
	ScopeThreadBinding  = "thread.binding"
	ScopeWorkspaceRun   = "workspace.run"
	ScopeConfigSnapshot = "config.snapshot"

	LSPReleaseScopeAgentThread     = "agent_thread"
	LSPReleaseScopeAgentAllThreads = "agent_all_threads"
	LSPReleaseScopeManagerKey      = "manager_key"

	ContextSourceLive         = "live"
	ContextSourceBootSnapshot = "boot_snapshot"
	ContextSourceDBRebuild    = "db_rebuild" // reserved for future use

	StatusActive       = "active"
	StatusStale        = "stale"
	StatusDisconnected = "disconnected"

	ReportVariantRuntime    = "runtime"
	ReportVariantCompletion = "completion"
	ReportVariantProgress   = "progress"
	ReportVariantDiagnostic = "diagnostic"

	DecisionSourceUI          = "ui"
	DecisionSourceAutoApprove = "auto_approve"
	DecisionSourceStatic      = "static" // reserved for future use

	// Hook methods (v2 extension)
	MethodHookSubscribe = "ctl/hook/subscribe"
	MethodHookBefore    = "ctl/hook/before"
	MethodHookCheck     = "ctl/hook/check"
	MethodHookAfter     = "ctl/hook/after"
	MethodHookResolve   = "ctl/hook/resolve"
	MethodHookPending   = "ctl/hook/pending"

	// Hook decisions
	HookDecisionAllow    = "allow"
	HookDecisionDeny     = "deny"
	HookDecisionWait     = "wait"
	HookDecisionModify   = "modify"
	HookDecisionContinue = "continue"
	HookDecisionWarn     = "warn"
	HookDecisionAbort    = "abort"
	HookDecisionApprove  = "approve"
	HookDecisionReject   = "reject"
	HookDecisionEscalate = "escalate"
)
