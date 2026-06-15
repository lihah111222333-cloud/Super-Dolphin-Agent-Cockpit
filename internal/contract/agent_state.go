package contract

import (
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

// IsActiveAgentState is the authoritative predicate for deciding whether
// an agent state counts as "active" from a UI / lifecycle perspective.
//
// P22 P4 §74: the predicate used to live as a private helper in
// internal/ui/wails (isActiveAgentState), driving NewActiveAgentCounter
// via state-negative enumeration. That formed a hidden contract: the
// meaning of "active" was implicitly co-owned by ui/wails and
// orchestration without a shared declaration. Upgrading it to a public
// contract helper makes the invariant explicit and lets any other
// consumer (app lifecycle, future CLI, tests) share the same definition.
//
// Semantics: a state is active iff it is neither empty, archived, nor a
// terminal state (StateStopped / StateFailed). Keep it as a pure function
// — zero allocations, safe to call from tight loops.
// IsActiveAgentState 判断active代理状态是否可用。
func IsActiveAgentState(state string) bool {
	switch agentdto.AgentState(strings.TrimSpace(state)) {
	case "", agentdto.StateStopped, agentdto.StateFailed, "archived":
		return false
	default:
		return true
	}
}
