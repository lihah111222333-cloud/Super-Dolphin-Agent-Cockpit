// Package mcp 定义 MCP 控制协议的所有 DTO、常量和错误码，
// 供 agent-terminal、mcp-orch、mcp-lsp 等各端共享，不含业务逻辑。
package mcp

import "strings"

// OrchCapabilities 返回 mcp-orch 与 host managed profile 共享的能力真值副本。
func OrchCapabilities() []string {
	return []string{
		"tools/orchestration",
		"tools/task",
		"tools/workspace",
		"tools/prompt",
		"tools/command",
		"tools/shared_file",
		"tools/video",
	}
}

const (
	// ctl/* 控制协议方法名。
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

	// ProtocolVersion 是当前握手协议版本标识。
	ProtocolVersion = "ctl/v1"
	// ManagedAuthorityProtocolVersion 是 shared managed authority 的显式协商版本。
	ManagedAuthorityProtocolVersion = "managed-authority/v1"

	// ClientKind* 标识注册客户端的角色类型。
	ClientKindOrch   = "orch"
	ClientKindLSP    = "lsp"
	ClientKindIDA    = "ida"
	ClientKindCustom = "custom"

	// PeerKind* 标识 peer 在协议中的能力角色。
	PeerKindTool          = "tool"
	PeerKindUI            = "ui"
	PeerKindSharedService = "shared-service"

	// Scope* 是 ctl/context 请求的合法 scope 值。
	ScopeAgentRuntime   = "agent.runtime"
	ScopeThreadBinding  = "thread.binding"
	ScopeWorkspaceRun   = "workspace.run"
	ScopeConfigSnapshot = "config.snapshot"

	// LSPReleaseScope* 是 ctl/lsp/release_scope 的 scope_kind 枚举值。
	LSPReleaseScopeAgentThread     = "agent_thread"
	LSPReleaseScopeAgentAllThreads = "agent_all_threads"
	LSPReleaseScopeManagerKey      = "manager_key"

	// ContextSource* 标识上下文响应的数据来源。
	ContextSourceLive         = "live"
	ContextSourceBootSnapshot = "boot_snapshot"
	ContextSourceDBRebuild    = "db_rebuild" // reserved for future use

	// Status* 是 peer 租约的状态枚举。
	StatusActive       = "active"
	StatusStale        = "stale"
	StatusDisconnected = "disconnected"

	// ReportVariant* 是 ctl/report 包络的类型判别值。
	ReportVariantRuntime    = "runtime"
	ReportVariantCompletion = "completion"
	ReportVariantProgress   = "progress"
	ReportVariantDiagnostic = "diagnostic"

	// DecisionSource* 标识审批决策的来源渠道。
	DecisionSourceUI          = "ui"
	DecisionSourceAutoApprove = "auto_approve"
	DecisionSourceStatic      = "static" // reserved for future use

	// Hook 协议方法名（v2 扩展）。
	MethodHookSubscribe = "ctl/hook/subscribe"
	MethodHookBefore    = "ctl/hook/before"
	MethodHookCheck     = "ctl/hook/check"
	MethodHookAfter     = "ctl/hook/after"
	MethodHookResolve   = "ctl/hook/resolve"
	MethodHookPending   = "ctl/hook/pending"

	// HookDecision* 是 hook 阶段可返回的决策值枚举。
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

// NormalizeProtocolVersion 将控制协议版本归一化为可比较的标识。
func NormalizeProtocolVersion(version string) string {
	return strings.TrimSpace(version)
}
