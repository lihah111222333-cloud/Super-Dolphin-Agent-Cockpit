// Package prompt 负责系统提示的注册、组装和缓存，为 provider 提供 start/turn 所需的结构化上下文。
package prompt

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// PromptRegion 标识 prompt section 位于 cacheable prefix 还是动态 tail。
type PromptRegion = contract.PromptRegion

const (
	PromptRegionStatic  = contract.PromptRegionStatic
	PromptRegionDynamic = contract.PromptRegionDynamic
)

// SectionContext 是 section compute 函数收到的完整组装上下文。
type SectionContext = contract.SectionContext

// SectionComputeFunc 是动态或静态 section 的内容生成函数签名。
type SectionComputeFunc = contract.SectionComputeFunc

// PromptSection 描述一个可注册的 prompt section。
type PromptSection = contract.PromptSection

// ResolvedPromptSection 是组装后带内容的 section。
type ResolvedPromptSection = contract.ResolvedPromptSection

// MCPSnapshot 描述当前 MCP server/tool/resource 状态。
type MCPSnapshot = contract.MCPSnapshot

// MCPAttachmentRef 是 MCP 附件在 prompt 中的引用信息。
type MCPAttachmentRef = contract.MCPAttachmentRef

// OutputStyleConfig 描述用户配置的输出风格。
type OutputStyleConfig = contract.OutputStyleConfig

// BuildCtx 是 prompt 组装时使用的运行上下文快照。
type BuildCtx = contract.BuildCtx

// SystemContext 是随 turn 变化的系统上下文键值集合。
type SystemContext = contract.SystemContext

// InvalidateReason 描述 prompt section 缓存失效原因。
type InvalidateReason = contract.InvalidateReason

const (
	InvalidateClear          = contract.InvalidateClear
	InvalidateCompact        = contract.InvalidateCompact
	InvalidateWorktree       = contract.InvalidateWorktree
	InvalidateResumeRestore  = contract.InvalidateResumeRestore
	InvalidateProviderSwitch = contract.InvalidateProviderSwitch
	InvalidateMemoryWrite    = contract.InvalidateMemoryWrite
)

// SnapshotVersion 是 prompt 组装快照的当前版本号。
const SnapshotVersion = contract.PromptAssemblySnapshotVersion

// StartInput 是 start 阶段 prompt 组装输入。
type StartInput = contract.StartInput

// TurnInput 是 turn 阶段 prompt 组装输入。
type TurnInput = contract.TurnInput

// StartAssembly 是 start 阶段 prompt 组装结果。
type StartAssembly = contract.StartAssembly

// TurnAssembly 是 turn 阶段 prompt 组装结果。
type TurnAssembly = contract.TurnAssembly

// PromptAssemblySnapshot 是可持久化的 prompt 组装快照。
type PromptAssemblySnapshot = contract.PromptAssemblySnapshot
