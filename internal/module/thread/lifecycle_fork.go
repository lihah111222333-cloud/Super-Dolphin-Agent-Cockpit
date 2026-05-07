package thread

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

func (s *service) Fork(ctx context.Context, threadID string) (ForkResult, error) {
	ctx = util.NonNilContext(ctx)
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return ForkResult{}, err
	}
	result, err := session.ForkThread(ctx, dto.ForkRequest{ThreadID: historyTargetID(binding, threadID)})
	if err != nil {
		return ForkResult{}, err
	}
	meta := s.lookupThreadMeta(ctx, threadID)
	displayName := continuationName(strings.TrimSpace(meta.Name))
	newThreadID := strings.TrimSpace(result.NewThreadID)
	if newThreadID == "" {
		return ForkResult{}, errors.New("fork thread id is required")
	}
	provider := strings.TrimSpace(binding.Provider)
	if provider == "" {
		return ForkResult{}, errors.New("fork provider is required")
	}
	snapshot := s.resolveStablePromptSnapshot(ctx, threadID, provider, contract.PromptAssemblySnapshot{})
	agentID := newThreadID
	cwd := util.FirstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd))
	if err := s.launchAgent(
		ctx,
		agentID,
		cwd,
		displayName,
		meta.ParentAgentID,
		meta.AgentType,
		meta.AgentMemoryScope,
		provider,
		meta.Model,
	); err != nil {
		return ForkResult{}, err
	}
	forkedSession, err := s.resumeSession(ctx, ResumeRequest{
		Provider:       provider,
		AgentID:        agentID,
		ThreadID:       newThreadID,
		CWD:            cwd,
		Model:          meta.Model,
		PromptSnapshot: snapshot,
	})
	if err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	if err := s.bindSessionGeneration(ctx, agentID); err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	providerThreadID := resolvedProviderUUID(forkedSession)
	if err := s.persistThreadState(ctx, newThreadState(threadStateForkKind, threadStateFields{
		PublicThreadID:   newThreadID,
		ProviderThreadID: providerThreadID,
		OwnerThreadID:    historyTargetID(binding, threadID),
		AgentID:          agentID,
		ParentAgentID:    meta.ParentAgentID,
		AgentType:        meta.AgentType,
		AgentMemoryScope: meta.AgentMemoryScope,
		Provider:         provider,
		CWD:              cwd,
		Model:            meta.Model,
		Name:             displayName,
		Prompt:           displayName,
		RolloutPath:      forkedSession.RolloutPath(),
		SessionUUID:      resolvedProviderUUID(forkedSession),
		CreatedAt:        time.Now().Unix(),
	}), true); err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	return ForkResult{
		NewThreadID: newThreadID,
		ForkedFrom:  bindingPublicThreadID(binding, threadID),
	}, nil
}

func (s *service) Recover(ctx context.Context, threadID string) (RecoverResult, error) {
	ctx = util.NonNilContext(ctx)
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return RecoverResult{}, err
	}
	meta := s.lookupThreadMeta(ctx, threadID)
	displayName := strings.TrimSpace(meta.Name)
	agentID := strings.TrimSpace(binding.AgentID)
	provider := strings.TrimSpace(binding.Provider)
	publicThreadID := bindingPublicThreadID(binding, threadID)
	providerThreadID := historyTargetID(binding, threadID)
	mode := "restore_launch"
	if err := s.recoverAgent(
		ctx,
		strings.TrimSpace(binding.AgentID),
		util.FirstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd)),
		displayName,
		meta.ParentAgentID,
		meta.AgentType,
		meta.AgentMemoryScope,
	); err != nil {
		return RecoverResult{}, err
	}
	if _, err := s.lookupSession(agentID); err != nil {
		mode = "relaunch_resume"
		if _, err := s.resumeSession(ctx, ResumeRequest{
			Provider:         provider,
			AgentID:          agentID,
			ThreadID:         publicThreadID,
			ProviderThreadID: providerThreadID,
		}); err != nil {
			return RecoverResult{}, err
		}
		if err := s.bindSessionGeneration(ctx, binding.AgentID); err != nil {
			s.stopAgent(ctx, binding.AgentID)
			return RecoverResult{}, err
		}
	}
	session, err := s.lookupSession(agentID)
	if err != nil {
		return RecoverResult{}, err
	}
	if err := s.persistThreadState(ctx, newThreadState(threadStateRecoverKind, threadStateFields{
		RequestedThreadID: threadID,
		PublicThreadID:    publicThreadID,
		ProviderThreadID:  util.FirstNonEmpty(providerThreadID, resolvedProviderUUID(session)),
		AgentID:           agentID,
		ParentAgentID:     meta.ParentAgentID,
		AgentType:         meta.AgentType,
		AgentMemoryScope:  meta.AgentMemoryScope,
		Provider:          provider,
		CWD:               util.FirstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd)),
		Model:             meta.Model,
		Name:              displayName,
		Prompt:            displayName,
		RolloutPath:       util.FirstNonEmpty(binding.RolloutPath, session.RolloutPath()),
		SessionUUID:       util.FirstNonEmpty(binding.SessionUUID, resolvedProviderUUID(session)),
		ConfigOverride:    clone.RawMessage(meta.ConfigOverride),
		CreatedAt:         meta.CreatedAt,
	}), true); err != nil {
		return RecoverResult{}, err
	}
	restoredCWD := util.FirstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd))
	if promptResumeRestoreRequiresInvalidation(restoredCWD, restoredCWD, s.cfg) {
		if err := s.invalidatePromptAssembly(ctx, contract.InvalidateResumeRestore); err != nil {
			return RecoverResult{}, err
		}
	}
	return RecoverResult{
		ThreadID:  publicThreadID,
		Status:    "recovering",
		Recovered: true,
		Mode:      mode,
	}, nil
}
