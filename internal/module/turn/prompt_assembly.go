package turn

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

// prepareTurnAssembly 准备turnassembly。
func (s *service) prepareTurnAssembly(ctx context.Context, threadID string, input PrepareInput, userText string, req dto.TurnRequest) (assembly dto.TurnAssembly, err error) {
	if s == nil || s.promptAssembly == nil {
		return dto.TurnAssembly{}, errors.New("turn prompt assembly service is required")
	}
	span := s.beginTurnTraceSpan(ctx, "turn.assembly", threadID, input.AgentID, req.LocalID, platformobs.NewCodeAnchor("internal/module/turn/prompt_assembly.go", "turn.(*service).prepareTurnAssembly", 13), nil)
	ctx = span.ctx
	defer func() { s.finishTurnTraceSpan(span, err) }()
	assembly, err = s.promptAssembly.AssembleTurn(ctx, contract.TurnInput{
		ThreadID:                     threadID,
		Provider:                     strings.TrimSpace(input.Provider),
		UserText:                     strings.TrimSpace(userText),
		PromptKey:                    strings.TrimSpace(input.PromptKey),
		Attachments:                  turnAttachmentRefs(req.Inputs),
		CurrentDate:                  time.Now().Format("2006-01-02"),
		Summary:                      strings.TrimSpace(input.Summary),
		CWD:                          strings.TrimSpace(input.CWD),
		GitRoot:                      strings.TrimSpace(input.GitRoot),
		IsWorktree:                   input.IsWorktree,
		Language:                     strings.TrimSpace(input.Language),
		Model:                        strings.TrimSpace(input.Model),
		EnabledTools:                 append([]string(nil), input.EnabledTools...),
		AdditionalWorkingDirectories: append([]string(nil), input.AdditionalWorkingDirectories...),
		MCPSnapshot:                  turnMCPSnapshot(input.MCPSnapshot, req.MCP),
		SessionFlags:                 clonePrepareFlags(input.SessionFlags),
		RuntimeUserContext:           clone.StringMap(input.RuntimeUserContext),
		OutputStyleConfig:            cloneOutputStyleConfigValue(input.OutputStyleConfig),
		ScratchpadDir:                strings.TrimSpace(input.ScratchpadDir),
		FRCConfig:                    normalizeFRCConfig(input.FRCConfig),
	})
	if err != nil {
		return dto.TurnAssembly{}, err
	}
	return assembly, nil
}
