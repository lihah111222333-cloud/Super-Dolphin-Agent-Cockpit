package turn

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	memorypkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory"
)

func (s *service) syntheticMemoryContext(
	ctx context.Context,
	session contract.Session,
	input PrepareInput,
	threadID, userText string,
	mcp dto.MCPManifest,
) memorypkg.TurnContextPayload {
	if s == nil || s.prepareMemoryContext == nil {
		return memorypkg.TurnContextPayload{}
	}
	buildCtx := contract.BuildCtx{
		CWD:                          strings.TrimSpace(input.CWD),
		GitRoot:                      strings.TrimSpace(input.GitRoot),
		IsWorktree:                   input.IsWorktree,
		Language:                     strings.TrimSpace(input.Language),
		Provider:                     strings.TrimSpace(input.Provider),
		Model:                        strings.TrimSpace(input.Model),
		EnabledTools:                 append([]string(nil), input.EnabledTools...),
		AdditionalWorkingDirectories: append([]string(nil), input.AdditionalWorkingDirectories...),
		MCPSnapshot:                  turnMCPSnapshot(input.MCPSnapshot, mcp),
		SessionFlags:                 clonePrepareFlags(input.SessionFlags),
	}
	return s.prepareMemoryContext(ctx, session, buildCtx, threadID, userText)
}
