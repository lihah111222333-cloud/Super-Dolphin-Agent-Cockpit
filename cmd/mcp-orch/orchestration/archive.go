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

// persistedThreadStatusArchived 是持久化线程归档后的状态值。
const persistedThreadStatusArchived = "archived"

// persistedArchiveTarget 汇总归档时需要同步更新的 agent/thread 记录。
type persistedArchiveTarget struct {
	agentID      string
	threadID     string
	bindingFound bool
}

// ArchiveOutcome 记录 ArchiveAgent 本次实际完成的归档动作。
type ArchiveOutcome struct {
	// RuntimeStopped 表示运行时或 launcher 已执行归档/停止动作。
	RuntimeStopped bool
	// ThreadArchived 表示本地持久化 thread 已被标记为 archived。
	ThreadArchived bool
	// BindingArchived 表示本地 provider binding 已被标记为 archived。
	BindingArchived bool
}

// Archived 返回本次归档是否至少产生了一项可观察效果。
func (o ArchiveOutcome) Archived() bool {
	return o.RuntimeStopped || o.ThreadArchived || o.BindingArchived
}

// ArchiveAgent 是 MCP 工具侧的回收入口。
// 它先停止本进程可见的 runtime，再把持久化 thread/binding 标记为 archived，避免只停进程不进回收箱。
func (s *service) ArchiveAgent(ctx context.Context, agentID string) (ArchiveOutcome, error) {
	var outcome ArchiveOutcome
	ctx, agentID, err := normalizeArchiveAgentArgs(ctx, agentID)
	if err != nil {
		return outcome, err
	}
	pkglogger.Info("archive: ArchiveAgent begin", "agent_id", agentID)
	target, resolveErr := s.resolvePersistedArchiveTarget(ctx, agentID)
	remoteArchived, archiveErr := s.stopArchiveTarget(ctx, agentID, target, resolveErr)
	if archiveErr != nil && !errors.Is(archiveErr, errAgentNotFound) {
		return outcome, archiveErr
	}
	if resolveErr != nil {
		return outcome, resolveErr
	}

	outcome.RuntimeStopped = remoteArchived
	if !remoteArchived {
		persistedOutcome, err := s.archivePersistedArchiveTarget(ctx, target)
		if err != nil {
			return outcome, err
		}
		outcome.ThreadArchived = persistedOutcome.ThreadArchived
		outcome.BindingArchived = persistedOutcome.BindingArchived
	}
	archived := outcome.Archived()
	if !archived {
		if archiveErr != nil {
			return outcome, archiveErr
		}
		return outcome, errAgentNotFound
	}
	pkglogger.Info("archive: ArchiveAgent done",
		"agent_id", agentID,
		"binding_found", target.bindingFound,
		"thread_id", target.threadID,
		"archived", archived,
		"runtime_stopped", outcome.RuntimeStopped,
		"thread_archived", outcome.ThreadArchived,
		"binding_archived", outcome.BindingArchived)
	return outcome, nil
}

// normalizeArchiveAgentArgs 补齐 nil context 并校验 agentID 非空。
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

// stopArchiveTarget 优先通过 launcher 归档远端 runtime。
// 当 launcher 的 Stop 会落库时，函数会用持久化 threadID 构造最小 agentRuntime 继续调用 Archive。
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
	settler, ok := s.lifecycle.launcher.(interface{ StopSettlesAgent() bool })
	remoteThreadID := strings.TrimSpace(target.threadID)
	archiveAgentID := platformshared.FirstTrimmed(target.agentID, stopAgentID)
	if s.lifecycle.launcher == nil || !ok || !settler.StopSettlesAgent() || remoteThreadID == "" || archiveAgentID == "" {
		return false, nil
	}
	agent := &agentRuntime{id: archiveAgentID, requestedAgentID: archiveAgentID, threadID: remoteThreadID, remoteThreadID: remoteThreadID, remoteAgentID: archiveAgentID}
	return true, s.lifecycle.launcher.Archive(ctx, agent)
}

// archivePersistedArchiveTarget 同步归档持久化 thread 和 provider binding。
// 两类记录可能只存在其中之一，因此逐项更新并返回是否实际写入。
func (s *service) archivePersistedArchiveTarget(ctx context.Context, target persistedArchiveTarget) (ArchiveOutcome, error) {
	var outcome ArchiveOutcome
	if target.threadID == "" && !target.bindingFound {
		pkglogger.Warn("archive: nothing to archive (binding=missing, thread=missing); runtime stopped but DB unchanged",
			"agent_id", target.agentID)
		return outcome, nil
	}
	now := time.Now().Unix()
	if target.threadID != "" && s.lifecycle.agentThreads != nil {
		pkglogger.Info("archive: marking thread archived",
			"thread_id", target.threadID,
			"agent_id", target.agentID)
		if err := s.lifecycle.agentThreads.UpdateStatus(ctx, PersistedThreadStatusUpdate{
			ThreadID:  target.threadID,
			Status:    persistedThreadStatusArchived,
			UpdatedAt: now,
		}); err != nil {
			pkglogger.Warn("archive: thread UpdateStatus failed",
				"thread_id", target.threadID,
				"agent_id", target.agentID,
				"error", err)
			return outcome, err
		}
		outcome.ThreadArchived = true
	}
	if target.bindingFound && target.agentID != "" && s.lifecycle.agentBindings != nil {
		pkglogger.Info("archive: marking binding archived",
			"agent_id", target.agentID)
		if err := s.lifecycle.agentBindings.SetArchived(ctx, PersistedBindingArchiveUpdate{
			AgentID:   target.agentID,
			Archived:  true,
			UpdatedAt: now,
		}); err != nil {
			pkglogger.Warn("archive: binding SetArchived failed",
				"agent_id", target.agentID,
				"error", err)
			return outcome, err
		}
		outcome.BindingArchived = true
	}
	return outcome, nil
}

// resolvePersistedArchiveTarget 解析 agentID 对应的持久化 binding/thread。
// 它会用 binding 和 thread 互相补齐 agentID/threadID，兼容历史 provider thread 字段差异。
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

// lookupPersistedArchiveBinding 按 agentID 查 provider binding；未找到按空结果处理。
func (s *service) lookupPersistedArchiveBinding(ctx context.Context, agentID string) (*PersistedBinding, error) {
	agentID = strings.TrimSpace(agentID)
	store, ok := s.persistedArchiveBindingStore(agentID)
	if !ok {
		return nil, nil
	}
	binding, err := store.GetByAgentID(ctx, agentID)
	if archiveLookupNotFound(err) {
		pkglogger.Warn("archive: binding lookup not found",
			"agent_id", agentID)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return binding, nil
}

func (s *service) persistedArchiveBindingStore(agentID string) (AgentBindingStore, bool) {
	if agentID == "" || s == nil || s.lifecycle == nil {
		return nil, false
	}
	if s.lifecycle.agentBindings == nil {
		pkglogger.Warn("archive: agentBindings store unavailable (fx optional injection nil); cannot mark archived",
			"agent_id", agentID)
		return nil, false
	}
	return s.lifecycle.agentBindings, true
}

// lookupPersistedArchiveThread 先按候选 ID 查线程，失败后再退到全量列表匹配。
func (s *service) lookupPersistedArchiveThread(ctx context.Context, agentID, hintedThreadID string) (*PersistedThread, error) {
	if s == nil || s.lifecycle == nil || s.lifecycle.agentThreads == nil {
		pkglogger.Warn("archive: agentThreads store unavailable (fx optional injection nil); cannot update thread status",
			"agent_id", agentID)
		return nil, nil
	}
	if thread, err := s.lookupPersistedArchiveThreadByIDs(ctx, archiveThreadLookupCandidates(agentID, hintedThreadID)); thread != nil || err != nil {
		return thread, err
	}
	return s.lookupPersistedArchiveThreadByList(ctx, agentID)
}

// lookupPersistedArchiveThreadByIDs 按候选 threadID 顺序查找，命中或出错立即返回。
func (s *service) lookupPersistedArchiveThreadByIDs(ctx context.Context, candidates []string) (*PersistedThread, error) {
	for _, candidate := range candidates {
		thread, err := s.getPersistedArchiveThread(ctx, candidate)
		if err != nil || thread != nil {
			return thread, err
		}
	}
	return nil, nil
}

// lookupPersistedArchiveThreadByList 在历史数据缺少直接绑定时扫描线程列表匹配 agentID。
func (s *service) lookupPersistedArchiveThreadByList(ctx context.Context, agentID string) (*PersistedThread, error) {
	threads, err := s.lifecycle.agentThreads.ListAll(ctx)
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

// archiveThreadLookupCandidates 生成 threadID 查找候选，并按 sameAgentID 去重。
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

// archiveThreadCandidateExists 判断候选列表里是否已有等价 ID。
func archiveThreadCandidateExists(candidates []string, candidate string) bool {
	for _, existing := range candidates {
		if sameAgentID(existing, candidate) {
			return true
		}
	}
	return false
}

// getPersistedArchiveThread 按 threadID 读取持久化线程；未找到返回 nil。
func (s *service) getPersistedArchiveThread(ctx context.Context, threadID string) (*PersistedThread, error) {
	threadID = strings.TrimSpace(threadID)
	if s == nil || s.lifecycle == nil || s.lifecycle.agentThreads == nil || threadID == "" {
		return nil, nil
	}
	thread, err := s.lifecycle.agentThreads.GetByThreadID(ctx, threadID)
	if archiveLookupNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return thread, nil
}

// archiveLookupNotFound 统一归档路径里“未找到”的错误判定。
func archiveLookupNotFound(err error) bool {
	return err != nil && (errors.Is(err, errAgentNotFound) || platformdb.IsNotFound(err))
}
