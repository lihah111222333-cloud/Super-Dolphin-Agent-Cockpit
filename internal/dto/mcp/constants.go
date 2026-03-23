package mcp

const (
	MethodRegister      = "ctl/register"
	MethodHeartbeat     = "ctl/heartbeat"
	MethodContext       = "ctl/context"
	MethodEvent         = "ctl/event"
	MethodLog           = "ctl/log"
	MethodApproval      = "ctl/approval/request"
	MethodReport        = "ctl/report"
	MethodShutdown      = "ctl/shutdown"
	MethodConfigChanged = "ctl/config/changed"

	ProtocolVersion = "ctl/v1"

	ClientKindOrch   = "orch"
	ClientKindLSP    = "lsp"
	ClientKindIDA    = "ida"
	ClientKindCustom = "custom"

	PeerKindTool = "tool"
	PeerKindUI   = "ui"

	ScopeAgentRuntime   = "agent.runtime"
	ScopeThreadBinding  = "thread.binding"
	ScopeWorkspaceRun   = "workspace.run"
	ScopeConfigSnapshot = "config.snapshot"

	ContextSourceLive         = "live"
	ContextSourceBootSnapshot = "boot_snapshot"
	ContextSourceDBRebuild    = "db_rebuild"

	StatusActive       = "active"
	StatusStale        = "stale"
	StatusDisconnected = "disconnected"

	ReportVariantRuntime    = "runtime"
	ReportVariantCompletion = "completion"
	ReportVariantProgress   = "progress"
	ReportVariantDiagnostic = "diagnostic"

	DecisionSourceUI          = "ui"
	DecisionSourceAutoApprove = "auto_approve"
	DecisionSourceStatic      = "static"

	// Hook methods (v2 extension)
	MethodHookSubscribe = "ctl/hook/subscribe"
	MethodHookBefore    = "ctl/hook/before"
	MethodHookCheck     = "ctl/hook/check"
	MethodHookAfter     = "ctl/hook/after"
	MethodHookResolve   = "ctl/hook/resolve"

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
