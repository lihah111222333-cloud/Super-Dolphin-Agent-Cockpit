package launcherwire

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	MethodThreadStart   = contract.ThreadRPCStart
	MethodThreadFork    = contract.ThreadRPCFork
	MethodThreadStop    = contract.ThreadRPCStop
	MethodThreadArchive = contract.ThreadRPCArchive
	MethodThreadNameSet = contract.ThreadRPCNameSet
	MethodTurnStart     = contract.TurnRPCStart
	MethodTurnInterrupt = contract.TurnRPCInterrupt

	ParamAgentID              = "agent_id"
	ParamCwd                  = "cwd"
	ParamName                 = "name"
	ParamAgentType            = "agent_type"
	ParamAgentKey             = "agent_key"
	ParamPromptKey            = "prompt_key"
	ParamAgentMemoryScope     = "agent_memory_scope"
	ParamParentAgentID        = "parent_agent_id"
	ParamBaseInstructions     = "base_instructions"
	ParamProvider             = "provider"
	ParamModel                = "model"
	ParamEffort               = "effort"
	ParamLanguage             = "language"
	ParamConfig               = "config"
	ParamDisabledTools        = "disabled_tools"
	ParamThreadID             = "thread_id"
	ParamInput                = "input"
	ParamSelectedSkills       = "selected_skills"
	ParamManualSkillSelection = "manual_skill_selection"
	ParamOutputSchema         = "output_schema"
	ParamSource               = "source"

	RespThread       = "thread"
	RespThreadID     = "thread_id"
	RespThreadIDJSON = "threadId"
	RespNewThreadID  = "new_thread_id"
	RespNestedID     = "id"
	RespAgentID      = "agent_id"
	RespAgentIDJSON  = "agentId"
	RespTurnID       = "turn_id"
)

// TurnInterruptRequest is the strict wire shape for turn/interrupt. Stop
// identity fields are deliberately required by the receiving turn service.
type TurnInterruptRequest struct {
	ThreadID       string `json:"thread_id"`
	ExpectedTurnID string `json:"expected_turn_id"`
	RequestID      string `json:"request_id"`
	Source         string `json:"source,omitempty"`
}

// TurnInterruptResponse is the subset of the turn/interrupt result that the
// orchestration launcher must validate before it waits for local settlement.
type TurnInterruptResponse struct {
	Accepted       *bool  `json:"accepted"`
	RequestID      string `json:"requestId,omitempty"`
	ExpectedTurnID string `json:"expectedTurnId,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
}

// ResolveThreadStartThreadID 按兼容顺序读取 thread/start 返回的 provider thread id。
func ResolveThreadStartThreadID(nested, resp map[string]any, fallback string) string {
	return resolveAlias(nested, resp, []string{RespNestedID}, []string{RespThreadIDJSON, RespThreadID}, fallback)
}

// ResolveThreadStartAgentID 按兼容顺序读取 thread/start 返回的 provider agent id。
func ResolveThreadStartAgentID(resp map[string]any, fallback string) string {
	return resolveAlias(nil, resp, nil, []string{RespAgentIDJSON, RespAgentID}, fallback)
}

// ResolveThreadForkThreadID 按兼容顺序读取 thread/fork 返回的新 provider thread id。
func ResolveThreadForkThreadID(nested, resp map[string]any, fallback string) string {
	return resolveAlias(nested, resp, []string{RespNestedID}, []string{RespNewThreadID, RespThreadIDJSON, RespThreadID}, fallback)
}

func resolveAlias(nested, resp map[string]any, nestedKeys, topKeys []string, fallback string) string {
	for _, key := range nestedKeys {
		if v := stringValue(nested[key]); v != "" {
			return v
		}
	}
	for _, key := range topKeys {
		if v := stringValue(resp[key]); v != "" {
			return v
		}
	}
	return fallback
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
