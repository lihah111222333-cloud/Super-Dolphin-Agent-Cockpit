package turn

import (
	"context"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
)

func ensureLocalTurnID(localID string) string {
	if localID = strings.TrimSpace(localID); localID != "" {
		return localID
	}
	return idgen.NewID("turn")
}

func isTerminalTurnState(state string) bool {
	switch TurnState(strings.TrimSpace(state)) {
	case StateCompleted, StateInterrupted, StateFailed, StateStalled:
		return true
	}
	return false
}

func waitForHandle(ctx context.Context, handle contract.TurnHandle, deadline time.Time) error {
	if handle == nil {
		return nil
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-handle.Done():
		return nil
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

// ---------------------------------------------------------------------------
// syntheticMemoryContext (was service_memory.go)
// ---------------------------------------------------------------------------

func (s *service) syntheticMemoryContext(
	ctx context.Context,
	session contract.Session,
	input PrepareInput,
	threadID, userText string,
	mcp dto.MCPManifest,
) contract.TurnContextPayload {
	if s == nil || s.turnContextProvider == nil {
		return contract.TurnContextPayload{}
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
	return s.turnContextProvider.PrepareTurnContext(ctx, session, buildCtx, threadID, userText)
}
