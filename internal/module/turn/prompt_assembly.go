package turn

import (
	"context"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *service) prepareTurnAssembly(ctx context.Context, threadID string, input PrepareInput, userText string, req dto.TurnRequest) (dto.TurnAssembly, error) {
	if s == nil || s.promptAssembly == nil {
		return dto.TurnAssembly{}, nil
	}
	assembly, err := s.promptAssembly.AssembleTurn(ctx, contract.TurnInput{
		ThreadID:                     threadID,
		Provider:                     strings.TrimSpace(input.Provider),
		UserText:                     strings.TrimSpace(userText),
		SkillPrompt:                  turnSkillPrompt(req.Skills),
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
		OutputStyleConfig:            cloneOutputStyleConfigValue(input.OutputStyleConfig),
		ScratchpadDir:                strings.TrimSpace(input.ScratchpadDir),
		FRCConfig:                    normalizeFRCConfig(input.FRCConfig),
	})
	if err != nil {
		return dto.TurnAssembly{}, err
	}
	return assembly, nil
}
