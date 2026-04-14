package prompt

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type PromptRegion = contract.PromptRegion

const (
	PromptRegionStatic  = contract.PromptRegionStatic
	PromptRegionDynamic = contract.PromptRegionDynamic
)

type SectionContext struct {
	BuildCtx BuildCtx
	Start    *StartInput
	Turn     *TurnInput
}

type SectionComputeFunc func(context.Context, SectionContext) (*string, error)

type PromptSection struct {
	Name        string
	Order       int
	Region      PromptRegion
	Volatile    bool
	CachePolicy CachePolicy
	StartOnly   bool
	Compute     SectionComputeFunc
}

type ResolvedPromptSection = contract.ResolvedPromptSection

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
