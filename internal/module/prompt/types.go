// Package prompt 负责系统提示的注册、组装和缓存，为 provider 提供 start/turn 所需的结构化上下文。
package prompt

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

type PromptRegion = contract.PromptRegion

const (
	PromptRegionStatic  = contract.PromptRegionStatic
	PromptRegionDynamic = contract.PromptRegionDynamic
)

type SectionContext = contract.SectionContext

type SectionComputeFunc = contract.SectionComputeFunc

type PromptSection = contract.PromptSection

type ResolvedPromptSection = contract.ResolvedPromptSection

type MCPSnapshot = contract.MCPSnapshot

type MCPAttachmentRef = contract.MCPAttachmentRef

type OutputStyleConfig = contract.OutputStyleConfig

type BuildCtx = contract.BuildCtx

type SystemContext = contract.SystemContext

type InvalidateReason = contract.InvalidateReason

const (
	InvalidateClear          = contract.InvalidateClear
	InvalidateCompact        = contract.InvalidateCompact
	InvalidateWorktree       = contract.InvalidateWorktree
	InvalidateResumeRestore  = contract.InvalidateResumeRestore
	InvalidateProviderSwitch = contract.InvalidateProviderSwitch
	InvalidateMemoryWrite    = contract.InvalidateMemoryWrite
)

const SnapshotVersion = contract.PromptAssemblySnapshotVersion

type StartInput = contract.StartInput

type TurnInput = contract.TurnInput

type StartAssembly = contract.StartAssembly

type TurnAssembly = contract.TurnAssembly

type PromptAssemblySnapshot = contract.PromptAssemblySnapshot
