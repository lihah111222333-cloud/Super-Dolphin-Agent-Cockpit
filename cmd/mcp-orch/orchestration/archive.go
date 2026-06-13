package orchestration

import (
	"context"
	"errors"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
// ArchiveAgent 归档代理。
func (s *service) ArchiveAgent(ctx context.Context, agentID string) error {
	ctx, agentID, err := normalizeArchiveAgentArgs(ctx, agentID)
	if err != nil {
		return err
	}
	pkglogger.Info("archive: ArchiveAgent begin", "agent_id", agentID)
	target, resolveErr := s.resolvePersistedArchiveTarget(ctx, agentID)
	remoteArchived, archiveErr := s.stopArchiveTarget(ctx, agentID, target, resolveErr)
	if archiveErr != nil && !errors.Is(archiveErr, errAgentNotFound) {
		return archiveErr
	}
	if resolveErr != nil {
		return resolveErr
	}

	archived := remoteArchived
	if !remoteArchived {
		var err error
		archived, err = s.archivePersistedArchiveTarget(ctx, target)
		if err != nil {
			return err
		}
	}
	if !archived && archiveErr != nil {
		return archiveErr
	}
	pkglogger.Info("archive: ArchiveAgent done",
		"agent_id", agentID,
		"binding_found", target.bindingFound,
		"thread_id", target.threadID,
		"archived", archived,
		"remote_archived", remoteArchived)
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

// stopArchiveTarget 停止归档target。
func (s *service) stopArchiveTarget(ctx context.Context, requestedAgentID string, target persistedArchiveTarget, resolveErr error) (bool, error) {
	stopAgentID := strings.TrimSpace(requestedAgentID)
	if resolveErr == nil && strings.TrimSpace(target.agentID) != "" {
		stopAgentID = strings.TrimSpace(target.agentID)
	}
	s.ensureRuntimeForPersistedAgent(ctx, stopAgentID)
	archived, err := s.archiveAgentViaLauncher(ctx, stopAgentID, "archived")
	if archived || err != nil {
		return archived, err
	}
	settler, ok := s.launcher.(interface{ StopSettlesAgent() bool })
	remoteThreadID := strings.TrimSpace(target.threadID)
	archiveAgentID := platformshared.FirstTrimmed(target.agentID, stopAgentID)
	if s.launcher == nil || !ok || !settler.StopSettlesAgent() || remoteThreadID == "" || archiveAgentID == "" {
		return false, nil
	}
	agent := &agentRuntime{id: archiveAgentID, requestedAgentID: archiveAgentID, threadID: remoteThreadID, remoteThreadID: remoteThreadID, remoteAgentID: archiveAgentID}
	return true, s.launcher.Archive(ctx, agent)
}

// archivePersistedArchiveTarget 归档persisted归档target。
func (s *service) archivePersistedArchiveTarget(ctx context.Context, target persistedArchiveTarget) (bool, error) {
	if target.threadID == "" && !target.bindingFound {
		pkglogger.Warn("archive: nothing to archive (binding=missing, thread=missing); runtime stopped but DB unchanged",
			"agent_id", target.agentID)
		return false, nil
	}
	now := time.Now().Unix()
	if target.threadID != "" && s.agentThreads != nil {
		pkglogger.Info("archive: marking thread archived",
			"thread_id", target.threadID,
			"agent_id", target.agentID)
		if err := s.agentThreads.UpdateStatus(ctx, PersistedThreadStatusUpdate{
			ThreadID:  target.threadID,
			Status:    persistedThreadStatusArchived,
			UpdatedAt: now,
		}); err != nil {
			pkglogger.Warn("archive: thread UpdateStatus failed",
				"thread_id", target.threadID,
				"agent_id", target.agentID,
				"error", err)
			return false, err
		}
	}
	if target.bindingFound && target.agentID != "" && s.agentBindings != nil {
		pkglogger.Info("archive: marking binding archived",
			"agent_id", target.agentID)
		if err := s.agentBindings.SetArchived(ctx, PersistedBindingArchiveUpdate{
			AgentID:   target.agentID,
			Archived:  true,
			UpdatedAt: now,
		}); err != nil {
			pkglogger.Warn("archive: binding SetArchived failed",
				"agent_id", target.agentID,
				"error", err)
			return false, err
		}
	}
	return true, nil
}

// resolvePersistedArchiveTarget 解析persisted归档target。
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

// lookupPersistedArchiveBinding 处理lookuppersisted归档binding。
func (s *service) lookupPersistedArchiveBinding(ctx context.Context, agentID string) (*PersistedBinding, error) {
	if s == nil || s.agentBindings == nil || strings.TrimSpace(agentID) == "" {
		if s != nil && s.agentBindings == nil && strings.TrimSpace(agentID) != "" {
			pkglogger.Warn("archive: agentBindings store unavailable (fx optional injection nil); cannot mark archived",
				"agent_id", strings.TrimSpace(agentID))
		}
		return nil, nil
	}
	binding, err := s.agentBindings.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if archiveLookupNotFound(err) {
		pkglogger.Warn("archive: binding lookup not found",
			"agent_id", strings.TrimSpace(agentID))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return binding, nil
}

func (s *service) lookupPersistedArchiveThread(ctx context.Context, agentID, hintedThreadID string) (*PersistedThread, error) {
	if s == nil || s.agentThreads == nil {
		pkglogger.Warn("archive: agentThreads store unavailable (fx optional injection nil); cannot update thread status",
			"agent_id", agentID)
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

// lookupPersistedArchiveThreadByList 按list处理lookuppersisted归档线程。
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
	pkglogger.Debug("archive: thread not found by id+list scan",
		"agent_id", agentID,
		"thread_count", len(threads))
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

// getPersistedArchiveThread 读取persisted归档线程。
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
