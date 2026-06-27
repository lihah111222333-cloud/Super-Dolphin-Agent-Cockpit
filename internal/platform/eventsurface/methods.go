package eventsurface

import "strings"

// RawWireAllowlist 描述 raw provider 事件仍允许直通前端的开放方法集合。
type RawWireAllowlist struct {
	Methods  []string
	Prefixes []string
	Suffixes []string
}

// AllTypedWireMethods 返回后端拥有强类型事件面的完整 wire 方法清单。
func AllTypedWireMethods() []string {
	return []string{
		MethodUIStateChanged,
		MethodUIThreadChanged,
		MethodUISidebarChanged,
		MethodTurnStarted,
		MethodTurnCompleted,
		MethodTurnInterrupted,
		MethodTurnStalled,
		MethodTurnResumed,
		MethodTurnOutputDelta,
		MethodAgentMessageDelta,
		MethodReasoningTextDelta,
		MethodCommandOutputDelta,
		MethodToolCall,
		MethodItemCompleted,
		MethodCommandApprovalRequested,
		MethodFileApprovalRequested,
		MethodSkillApprovalRequested,
		MethodApprovalResolved,
		MethodThreadStarted,
		MethodThreadStopped,
		MethodThreadMessages,
		MethodThreadCompacted,
		MethodThreadTokenUsage,
		MethodSkillsChanged,
		MethodUIPreferencesChanged,
		MethodUIThreadPatch,
		MethodUISharedFilesChanged,
		MethodUIMemoryChanged,
		MethodUIPromptsChanged,
		MethodAgentLaunched,
		MethodAgentStopped,
		MethodAgentRecovering,
		MethodAgentFailed,
		MethodAgentRuntimeReported,
		MethodTaskNodeStatusChanged,
		MethodCronJobRunStateChanged,
	}
}

// CompatWirePrefixes 返回 source event 兼容展开用的前缀；它不是 raw provider allowlist。
func CompatWirePrefixes() []string {
	return []string{
		"workspace/run/",
	}
}

// RawWireAllowlistSpec 返回 raw provider 直通前端的显式方法、前缀和后缀。
func RawWireAllowlistSpec() RawWireAllowlist {
	return RawWireAllowlist{
		Methods: []string{
			"error",
			"configWarning",
			"deprecationNotice",
			"approval/request",
			"thread/name/updated",
			"thread/tokenUsage/updated",
			"thread/tokenusage/updated",
		},
		Prefixes: []string{
			"item/",
			"turn/plan/",
			"turn/diff/",
			"agent/event/",
			"account/",
			"app/list/",
			"fuzzyFileSearch/",
		},
		Suffixes: []string{
			"/requestApproval",
		},
	}
}

// RawWireAllowed 判断 raw provider 方法是否在显式直通清单中。
func RawWireAllowed(spec RawWireAllowlist, method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return false
	}
	for _, exact := range spec.Methods {
		if method == exact {
			return true
		}
	}
	for _, prefix := range spec.Prefixes {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	for _, suffix := range spec.Suffixes {
		if strings.HasSuffix(method, suffix) {
			return true
		}
	}
	return false
}
