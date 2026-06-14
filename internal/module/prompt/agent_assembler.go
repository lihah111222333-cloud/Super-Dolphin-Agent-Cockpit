package prompt

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// AgentType / AgentInput were promoted to `contract` so thread helpers can
// route subagent launches through AssembleAgent without pulling in this
// package. These aliases keep the package-local call sites readable.
type AgentType = contract.AgentType

const (
	// AgentTypeDefault matches Claude Code's DEFAULT_AGENT_PROMPT bucket.
	AgentTypeDefault = contract.AgentTypeDefault
	// AgentTypeExplore is the read-only exploration subagent; drops claudeMd
	// and gitStatus (mapping §7.2).
	AgentTypeExplore = contract.AgentTypeExplore
	// AgentTypePlan is the planning subagent; drops claudeMd and gitStatus.
	AgentTypePlan = contract.AgentTypePlan
)

type AgentInput = contract.AgentInput

// AssembleAgent produces a StartAssembly tailored for a subagent dispatch.
//
//   - When OverrideSystemPrompt is truthy it wins outright: the override text
//     becomes the BaseInstructions and no section computation runs. This
//     matches Claude Code's override.systemPrompt direct branch
//     (runAgent.ts:380-410).
//   - Otherwise AssembleStart is invoked as usual, then Explore/Plan agents
//     get claudeMd / gitStatus scrubbed, and the agent env-details block is
//     appended to BaseInstructions (mapping §7.2).
//
// AssembleAgent 处理assemble代理。
func (s *service) AssembleAgent(ctx context.Context, in AgentInput) (StartAssembly, error) {
	if override := strings.TrimSpace(in.OverrideSystemPrompt); override != "" {
		return s.overrideAgentAssembly(in.StartInput, override), nil
	}
	assembly, err := s.AssembleStart(ctx, in.StartInput)
	if err != nil {
		return StartAssembly{}, err
	}
	return applyAgentPostProcessing(assembly, in.AgentType), nil
}

func (s *service) overrideAgentAssembly(in StartInput, override string) StartAssembly {
	displayName := strings.TrimSpace(in.Name)
	dev := strings.TrimSpace(in.DeveloperInstructions)
	return StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      override,
		DeveloperInstructions: dev,
		Snapshot:              s.newSnapshot(displayName, override, dev, in.Provider, nil, nil),
	}
}

// applyAgentPostProcessing layers Claude Code's enhanceSystemPromptWithEnvDetails
// behavior on top of a computed StartAssembly, and applies Explore/Plan
// claudeMd/gitStatus redaction.
func applyAgentPostProcessing(assembly StartAssembly, agentType AgentType) StartAssembly {
	if redactsClaudeMd(agentType) {
		delete(assembly.UserContext, "claudeMd")
		assembly.SystemContext = nil
		assembly.UserContextText = contract.FormatUserContextText(assembly.UserContext)
	}
	assembly.BaseInstructions = strings.TrimSpace(
		joinBlocks(assembly.BaseInstructions, sectionAgentEnvDetails),
	)
	return assembly
}

func redactsClaudeMd(agentType AgentType) bool {
	switch agentType {
	case AgentTypeExplore, AgentTypePlan:
		return true
	default:
		return false
	}
}

// sectionAgentEnvDetails is the equivalent of Claude Code's
// enhanceSystemPromptWithEnvDetails (prompts.ts L760-L791). It reinforces the
// subagent-specific ground rules that are easy to miss when running under a
// short-lived shell context.
const sectionAgentEnvDetails = `# Subagent runtime guardrails
- Always use absolute paths in file arguments; agent threads reset CWD between Bash invocations, so relative paths are unreliable.
- When returning the final report, share relevant file paths as absolute paths and only include code snippets when the surrounding text needs them to make sense.
- Do not use emojis and do not add a colon before tool calls; write plain prose.
- Before invoking DiscoverSkills, verify the surfaced skills do not already cover the task; skill discovery availability alone is not a reason to call it.`
