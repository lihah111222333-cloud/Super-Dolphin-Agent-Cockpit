package prompt

import (
	"context"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// AgentType 是 contract.AgentType 的本包别名。
// 线程模块通过 contract 传递子 agent 类型，prompt 包内部保留别名以减少跨包细节泄漏。
type AgentType = contract.AgentType

const (
	// AgentTypeDefault 表示普通子 agent，不额外删减上下文。
	AgentTypeDefault = contract.AgentTypeDefault
	// AgentTypeExplore 表示只读探索子 agent，会移除 claudeMd/gitStatus 等父线程上下文。
	AgentTypeExplore = contract.AgentTypeExplore
	// AgentTypePlan 表示规划子 agent，同样隔离 claudeMd/gitStatus，避免计划线程继承实现细节。
	AgentTypePlan = contract.AgentTypePlan
)

// AgentInput 是 contract.AgentInput 的本包别名，用于组装子 agent start prompt。
type AgentInput = contract.AgentInput

// AssembleAgent 为子 agent 启动请求组装 StartAssembly。
// OverrideSystemPrompt 非空时直接作为 BaseInstructions，跳过 section 计算；否则先复用 AssembleStart。
// Explore/Plan 类型会移除 claudeMd/gitStatus，并追加子 agent 运行时边界说明。
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

// overrideAgentAssembly 构造只使用 override system prompt 的子 agent assembly。
// 该路径不计算动态 section，但仍写入 snapshot，便于 resume/fork/recover 使用同一份 start 上下文。
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

// applyAgentPostProcessing 在普通 start assembly 上叠加子 agent 专用约束。
// Explore/Plan 会删除父线程 claudeMd/gitStatus，避免只读或规划子任务意外继承实现上下文。
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

// redactsClaudeMd 判断子 agent 类型是否需要删除父线程上下文中的 claudeMd/gitStatus。
func redactsClaudeMd(agentType AgentType) bool {
	switch agentType {
	case AgentTypeExplore, AgentTypePlan:
		return true
	default:
		return false
	}
}

// sectionAgentEnvDetails 是子 agent 运行时边界说明。
// 这里强调绝对路径、报告格式和技能发现约束，避免短生命周期 shell 中出现上下文漂移。
const sectionAgentEnvDetails = `# Subagent runtime guardrails
- Always use absolute paths in file arguments; agent threads reset CWD between Bash invocations, so relative paths are unreliable.
- When returning the final report, share relevant file paths as absolute paths and only include code snippets when the surrounding text needs them to make sense.
- Do not use emojis and do not add a colon before tool calls; write plain prose.
- Before invoking DiscoverSkills, verify the surfaced skills do not already cover the task; skill discovery availability alone is not a reason to call it.`
