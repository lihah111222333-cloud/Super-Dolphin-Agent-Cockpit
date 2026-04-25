package orchestration

import (
	"context"
	"errors"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const persistedThreadStatusArchived = "archived"

type persistedArchiveTarget struct {
	agentID      string
	threadID     string
	bindingFound bool
}

// ArchiveAgent is the MCP-tool recycle path: stop the live runtime when it is
// known to this process, then mark the persisted thread/binding archived so the
// agent lands in the recycle-bin lifecycle rather than only becoming stopped.
func (s *service) ArchiveAgent(ctx context.Context, agentID string) error {
	ctx, agentID, err := normalizeArchiveAgentArgs(ctx, agentID)
	if err != nil {
		return err
	}
	target, resolveErr := s.resolvePersistedArchiveTarget(ctx, agentID)
	stopErr := s.stopArchiveTarget(ctx, agentID, target, resolveErr)
	if stopErr != nil && !errors.Is(stopErr, errAgentNotFound) {
		return stopErr
	}
	if resolveErr != nil {
		return resolveErr
	}

	archived, err := s.archivePersistedArchiveTarget(ctx, target)
	if err != nil {
		return err
	}
	if !archived && stopErr != nil {
		return stopErr
	}
	return nil
}

func normalizeArchiveAgentArgs(ctx context.Context, agentID string) (context.Context, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ctx, "", errAgentNotFound
	}
	return ctx, agentID, nil
}

func (s *service) stopArchiveTarget(ctx context.Context, requestedAgentID string, target persistedArchiveTarget, resolveErr error) error {
	stopAgentID := strings.TrimSpace(requestedAgentID)
	if resolveErr == nil && strings.TrimSpace(target.agentID) != "" {
		stopAgentID = strings.TrimSpace(target.agentID)
	}
	s.ensureRuntimeForPersistedAgent(ctx, stopAgentID)
	return s.stopAgentViaLauncher(ctx, stopAgentID, "archived")
}

func (s *service) archivePersistedArchiveTarget(ctx context.Context, target persistedArchiveTarget) (bool, error) {
	if target.threadID == "" && !target.bindingFound {
		return false, nil
	}
	now := time.Now().Unix()
	if target.threadID != "" && s.agentThreads != nil {
		if err := s.agentThreads.UpdateStatus(ctx, PersistedThreadStatusUpdate{
			ThreadID:  target.threadID,
			Status:    persistedThreadStatusArchived,
			UpdatedAt: now,
		}); err != nil {
			return false, err
		}
	}
	if target.bindingFound && target.agentID != "" && s.agentBindings != nil {
		if err := s.agentBindings.SetArchived(ctx, PersistedBindingArchiveUpdate{
			AgentID:   target.agentID,
			Archived:  true,
			UpdatedAt: now,
		}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *service) resolvePersistedArchiveTarget(ctx context.Context, agentID string) (persistedArchiveTarget, error) {
	target := persistedArchiveTarget{agentID: strings.TrimSpace(agentID)}
	binding, err := s.lookupPersistedArchiveBinding(ctx, target.agentID)
	if err != nil {
		return target, err
	}
	if binding != nil {
		target.bindingFound = true
		target.agentID = platformshared.FirstTrimmed(binding.AgentID, target.agentID)
		target.threadID = platformshared.FirstTrimmed(binding.CodexThreadID, binding.ProviderThreadID)
	}

	thread, err := s.lookupPersistedArchiveThread(ctx, agentID, target.threadID)
	if err != nil {
		return target, err
	}
	if thread != nil {
		target.threadID = strings.TrimSpace(thread.ThreadID)
		target.agentID = platformshared.FirstTrimmed(thread.AgentID, target.agentID, persistedThreadAgentID(*thread))
	}

	if binding == nil && target.agentID != "" && !sameAgentID(target.agentID, agentID) {
		binding, err = s.lookupPersistedArchiveBinding(ctx, target.agentID)
		if err != nil {
			return target, err
		}
		if binding != nil {
			target.bindingFound = true
			target.agentID = platformshared.FirstTrimmed(binding.AgentID, target.agentID)
			target.threadID = platformshared.FirstTrimmed(target.threadID, binding.CodexThreadID, binding.ProviderThreadID)
		}
	}
	return target, nil
}

func (s *service) lookupPersistedArchiveBinding(ctx context.Context, agentID string) (*PersistedBinding, error) {
	if s == nil || s.agentBindings == nil || strings.TrimSpace(agentID) == "" {
		return nil, nil
	}
	binding, err := s.agentBindings.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if archiveLookupNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return binding, nil
}

func (s *service) lookupPersistedArchiveThread(ctx context.Context, agentID, hintedThreadID string) (*PersistedThread, error) {
	if s == nil || s.agentThreads == nil {
		return nil, nil
	}
	if thread, err := s.lookupPersistedArchiveThreadByIDs(ctx, archiveThreadLookupCandidates(agentID, hintedThreadID)); thread != nil || err != nil {
		return thread, err
	}
	return s.lookupPersistedArchiveThreadByList(ctx, agentID)
}

func (s *service) lookupPersistedArchiveThreadByIDs(ctx context.Context, candidates []string) (*PersistedThread, error) {
	for _, candidate := range candidates {
		thread, err := s.getPersistedArchiveThread(ctx, candidate)
		if err != nil || thread != nil {
			return thread, err
		}
	}
	return nil, nil
}

func (s *service) lookupPersistedArchiveThreadByList(ctx context.Context, agentID string) (*PersistedThread, error) {
	threads, err := s.agentThreads.ListAll(ctx)
	if archiveLookupNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, thread := range threads {
		if sameAgentID(thread.ThreadID, agentID) || sameAgentID(thread.AgentID, agentID) || sameAgentID(persistedThreadAgentID(thread), agentID) {
			found := thread
			return &found, nil
		}
	}
	return nil, nil
}

func archiveThreadLookupCandidates(agentID, hintedThreadID string) []string {
	candidates := make([]string, 0, 2)
	for _, candidate := range []string{hintedThreadID, agentID} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && !archiveThreadCandidateExists(candidates, candidate) {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func archiveThreadCandidateExists(candidates []string, candidate string) bool {
	for _, existing := range candidates {
		if sameAgentID(existing, candidate) {
			return true
		}
	}
	return false
}

func (s *service) getPersistedArchiveThread(ctx context.Context, threadID string) (*PersistedThread, error) {
	threadID = strings.TrimSpace(threadID)
	if s == nil || s.agentThreads == nil || threadID == "" {
		return nil, nil
	}
	thread, err := s.agentThreads.GetByThreadID(ctx, threadID)
	if archiveLookupNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return thread, nil
}

func archiveLookupNotFound(err error) bool {
	return err != nil && (errors.Is(err, errAgentNotFound) || platformdb.IsNotFound(err))
}
